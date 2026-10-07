package sqlite

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

type roundAmbiguousStorage struct {
	*Store
	admitAckLost            bool
	commitAckLost           bool
	readAfterCommitFailures atomic.Int32
	configuredReadFailures  int32
	claimReadLost           bool
	claimObserved           atomic.Bool
	failureWrites           atomic.Int32
}

func (storage *roundAmbiguousStorage) AdmitRound(ctx context.Context, request rl.AdmitRoundRequest) (rl.Admission, error) {
	admission, err := storage.Store.AdmitRound(ctx, request)
	if err == nil && storage.admitAckLost {
		return rl.Admission{}, errors.New("private lost admission acknowledgement")
	}
	return admission, err
}
func (storage *roundAmbiguousStorage) ClaimAttempt(ctx context.Context, request rl.ClaimRequest) (*rl.ClaimToken, error) {
	claim, err := storage.Store.ClaimAttempt(ctx, request)
	if err == nil && claim != nil && storage.claimReadLost {
		storage.claimObserved.Store(true)
	}
	return claim, err
}
func (storage *roundAmbiguousStorage) ReadRound(ctx context.Context, id contracts.RoundID) (rl.RoundRecord, error) {
	if storage.claimObserved.CompareAndSwap(true, false) {
		return rl.RoundRecord{}, errors.New("private post-claim read failed")
	}
	remaining := storage.readAfterCommitFailures.Load()
	if remaining != 0 {
		if remaining > 0 {
			storage.readAfterCommitFailures.Add(-1)
		}
		return rl.RoundRecord{}, errors.New("private connection needs cleanup")
	}
	return storage.Store.ReadRound(ctx, id)
}
func (storage *roundAmbiguousStorage) CommitOutcome(ctx context.Context, request rl.CommitOutcomeRequest) (rl.RoundRecord, error) {
	result, err := storage.Store.CommitOutcome(ctx, request)
	if err == nil && storage.commitAckLost {
		storage.readAfterCommitFailures.Store(storage.configuredReadFailures)
		return rl.RoundRecord{}, errors.New("private lost commit acknowledgement")
	}
	return result, err
}
func (storage *roundAmbiguousStorage) RecordAttemptFailure(ctx context.Context, request rl.AttemptFailureRequest) (rl.RoundRecord, error) {
	storage.failureWrites.Add(1)
	return storage.Store.RecordAttemptFailure(ctx, request)
}
func TestRoundAmbiguousAdmissionAndCommitAcknowledgementsReconcileOnlyStoredResults(t *testing.T) {
	for _, entry := range []struct {
		name          string
		admit, commit bool
		reads         int32
	}{{"admission ack", true, false, 0}, {"commit ack", false, true, 0}, {"commit ack first read fails", false, true, 1}, {"commit ack continuous read fails", false, true, -1}} {
		t.Run(entry.name, func(t *testing.T) {
			store := migratedTestStore(t)
			ports := &roundAmbiguousStorage{Store: store, admitAckLost: entry.admit, commitAckLost: entry.commit, configuredReadFailures: entry.reads}
			entropy := new(productEntropy)
			service := lifecycleWithStorage(t, ports, entropy, func() time.Time { return storageNow })
			request := productRequest(storageNow, rl.ImmediateMode)
			returned, err := service.CreateCollection(context.Background(), request)
			if entry.reads != 0 {
				requireFault(t, err, contracts.StorageUnavailable)
				if returned.ID != "" || returned.Outcome != nil {
					t.Fatal("unconfirmed result exposed", returned)
				}
			} else if err != nil || returned.State != contracts.Completed || returned.Outcome == nil {
				t.Fatal(returned, err)
			}
			durable, readErr := store.ReadRound(context.Background(), "transition-002")
			if readErr != nil || durable.State != contracts.Completed || durable.Outcome == nil || ports.failureWrites.Load() != 0 || entropy.calls.Load() != 1 || roundTableCount(t, store, "results") != 1 || roundTableCount(t, store, "winners") != 1 {
				t.Fatal("lost acknowledgement changed committed state", durable, readErr)
			}
			operation, readErr := store.ReadOperation(context.Background(), request.OperationID)
			if readErr != nil || operation.Status != contracts.OperationSucceeded {
				t.Fatal(operation, readErr)
			}
			// Reconnection is explicit: only after the read path recovers do we accept
			// the persisted response. Neither recovery nor replay samples new winners.
			ports.readAfterCommitFailures.Store(0)
			replay, err := service.CreateCollection(context.Background(), request)
			if err != nil || !reflect.DeepEqual(replay, durable) || entropy.calls.Load() != 1 || ports.failureWrites.Load() != 0 {
				t.Fatal("replay after connection cleanup", replay, err)
			}
			report, err := service.Recover(context.Background(), rl.RecoverRequest{})
			if err != nil || len(report.Failed) != 0 || len(report.Due) != 0 || entropy.calls.Load() != 1 {
				t.Fatal(report, err)
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}
func TestRoundPostClaimReadFailureReleasesLiveOwnershipForSameSessionRecovery(t *testing.T) {
	store := migratedTestStore(t)
	ports := &roundAmbiguousStorage{Store: store, claimReadLost: true}
	entropy := new(productEntropy)
	service := lifecycleWithStorage(t, ports, entropy, func() time.Time { return storageNow })
	result, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ImmediateMode))
	requireFault(t, err, contracts.StorageUnavailable)
	if result.Outcome != nil || entropy.calls.Load() != 0 {
		t.Fatal(result)
	}
	current, err := store.ReadRound(context.Background(), "transition-002")
	if err != nil || current.State != contracts.Executing || current.Claim == nil {
		t.Fatal(current, err)
	}
	ports.claimReadLost = false
	report, err := service.Recover(context.Background(), rl.RecoverRequest{})
	if err != nil || len(report.Failed) != 1 || report.Failed[0] != current.ID || entropy.calls.Load() != 0 {
		t.Fatal("failed read retained stale live ownership", report, err)
	}
	failed, err := store.ReadRound(context.Background(), current.ID)
	if err != nil || failed.State != contracts.Failed || failed.Outcome != nil || ports.failureWrites.Load() != 1 {
		t.Fatal(failed, err)
	}
	retry, err := service.RetryRound(context.Background(), rl.RetryRequest{OperationID: "retry-after-read-failure", Context: productRoundContext(failed, "session-one")})
	if err != nil || retry.ID != failed.ID || retry.State != contracts.Completed || entropy.calls.Load() != 1 {
		t.Fatal(retry, err)
	}
	assertNoHandles(t, store)
}
