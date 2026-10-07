package desktop_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/desktop"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	"strings"
	"testing"
	"time"
)

type operationReaderFunc func(context.Context, contracts.OperationID) (rl.OperationRecord, error)

func (f operationReaderFunc) ReadOperation(ctx context.Context, id contracts.OperationID) (rl.OperationRecord, error) {
	return f(ctx, id)
}
func operationRecord(state contracts.OperationState) rl.OperationRecord {
	record := rl.OperationRecord{Operation: rl.OperationIdentity{ID: "operation", Kind: "Rerun"}, Status: state, CollectionID: "collection", RoundID: "round", Revision: 1}
	if state == contracts.OperationUnknown {
		return rl.OperationRecord{Operation: rl.OperationIdentity{ID: "operation"}, Status: state}
	}
	record.Operation.PublicFingerprint[0] = 13
	if state == contracts.OperationFailed {
		record.FailureCode = contracts.StorageUnavailable
	}
	return record
}
func TestRecoveryServiceRejectsMissingDependenciesAndCancelledContext(t *testing.T) {
	reader := operationReaderFunc(func(context.Context, contracts.OperationID) (rl.OperationRecord, error) {
		t.Fatal("unexpected read")
		return rl.OperationRecord{}, nil
	})
	for _, test := range []struct {
		session contracts.BackendSessionID
		now     func() time.Time
		reader  desktop.OperationReader
	}{{"", validNow, reader}, {"session", nil, reader}, {"session", validNow, nil}} {
		if service, err := desktop.NewRecoveryService(test.session, test.now, test.reader); err == nil || service != nil {
			t.Fatal("invalid constructor accepted")
		}
	}
	service, err := desktop.NewRecoveryService("session", validNow, reader)
	if err != nil {
		t.Fatal(err)
	}
	contexts := []context.Context{nil}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	contexts = append(contexts, cancelled)
	deadline, stop := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer stop()
	contexts = append(contexts, deadline)
	for _, ctx := range contexts {
		response, err := service.GetOperation(ctx, contracts.OperationLookupRequest{OperationID: "operation"})
		if err == nil || response.Data != nil {
			t.Fatal("invalid context returned data")
		}
	}
}
func TestRecoveryServiceReturnsStoredObservationWithoutPrivateFingerprint(t *testing.T) {
	for _, state := range []contracts.OperationState{contracts.OperationUnknown, contracts.OperationPending, contracts.OperationSucceeded, contracts.OperationFailed} {
		t.Run(string(state), func(t *testing.T) {
			calls := 0
			record := operationRecord(state)
			service, err := desktop.NewRecoveryService("session", validNow, operationReaderFunc(func(_ context.Context, id contracts.OperationID) (rl.OperationRecord, error) {
				calls++
				if id != "operation" {
					t.Fatal("wrong query identity")
				}
				return record, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				response, err := service.GetOperation(context.Background(), contracts.OperationLookupRequest{OperationID: "operation"})
				if err != nil || !response.OK || response.Data == nil || response.Data.State != state {
					t.Fatalf("response: %+v %v", response, err)
				}
				raw, err := json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				for _, private := range []string{"fingerprint", "password", "claim", "request"} {
					if strings.Contains(string(raw), private) {
						t.Fatal("private field exposed")
					}
				}
			}
			if calls != 2 || record.Operation.PublicFingerprint != operationRecord(state).Operation.PublicFingerprint {
				t.Fatal("query changed private record")
			}
		})
	}
}
func TestRecoveryServiceNeverTurnsReadFaultIntoUnknownOrRetries(t *testing.T) {
	for _, failAt := range []int{1, 3, 0} {
		calls := 0
		service, err := desktop.NewRecoveryService("session", validNow, operationReaderFunc(func(context.Context, contracts.OperationID) (rl.OperationRecord, error) {
			calls++
			if failAt == 0 || calls == failAt {
				return operationRecord(contracts.OperationPending), errors.New("SECRET password and file path")
			}
			return operationRecord(contracts.OperationPending), nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= 4; i++ {
			response, err := service.GetOperation(context.Background(), contracts.OperationLookupRequest{OperationID: "operation"})
			if err != nil {
				t.Fatal(err)
			}
			failed := failAt == 0 || i == failAt
			if response.OK == failed {
				t.Fatal("wrong failure result")
			}
			if failed && (response.Data != nil || response.Code != contracts.StorageUnavailable || strings.Contains(response.MessageKey, "SECRET")) {
				t.Fatal("fault exposed partial data or raw cause")
			}
		}
		if calls != 4 {
			t.Fatal("hidden retry")
		}
	}
}
func TestRecoveryServiceRejectsInvalidRequestClockAndStorageMetadata(t *testing.T) {
	calls := 0
	reader := operationReaderFunc(func(context.Context, contracts.OperationID) (rl.OperationRecord, error) {
		calls++
		return operationRecord(contracts.OperationPending), nil
	})
	service, _ := desktop.NewRecoveryService("session", validNow, reader)
	response, err := service.GetOperation(context.Background(), contracts.OperationLookupRequest{})
	if err != nil || response.OK || response.Code != contracts.InvalidInput || calls != 0 {
		t.Fatal("invalid request dispatched")
	}
	for _, now := range []time.Time{{}, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		service, _ := desktop.NewRecoveryService("session", func() time.Time { return now }, reader)
		response, err := service.GetOperation(context.Background(), contracts.OperationLookupRequest{OperationID: "operation"})
		if err == nil || response.Data != nil || calls != 0 {
			t.Fatal("invalid clock dispatched")
		}
	}
	for _, change := range []func(*rl.OperationRecord){func(r *rl.OperationRecord) { r.Operation.ID = "other" }, func(r *rl.OperationRecord) { r.Status = "invalid" }, func(r *rl.OperationRecord) { r.CollectionID = "" }, func(r *rl.OperationRecord) { r.RoundID = "" }, func(r *rl.OperationRecord) { r.Revision = contracts.Revision(contracts.MaxSafeInteger + 1) }, func(r *rl.OperationRecord) { r.Operation.Kind = "invalid" }, func(r *rl.OperationRecord) { r.FailureCode = contracts.StorageUnavailable }} {
		record := operationRecord(contracts.OperationPending)
		change(&record)
		service, _ := desktop.NewRecoveryService("session", validNow, operationReaderFunc(func(context.Context, contracts.OperationID) (rl.OperationRecord, error) { return record, nil }))
		response, err := service.GetOperation(context.Background(), contracts.OperationLookupRequest{OperationID: "operation"})
		if err != nil || response.OK || response.Data != nil || response.Code != contracts.InvalidState {
			t.Fatal("corrupt metadata accepted")
		}
	}
}
func TestRecoveryServiceReadsActualSQLiteUnknownAndClosedFailure(t *testing.T) {
	store := pendingStore(t)
	service, err := desktop.NewRecoveryService("session", validNow, store)
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.GetOperation(context.Background(), contracts.OperationLookupRequest{OperationID: "missing"})
	if err != nil || !response.OK || response.Data.State != contracts.OperationUnknown {
		t.Fatal("actual no-row query not unknown")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	response, err = service.GetOperation(context.Background(), contracts.OperationLookupRequest{OperationID: "missing"})
	if err != nil || response.OK || response.Data != nil || response.Code != contracts.StorageUnavailable {
		t.Fatal("closed DB disguised as unknown")
	}
}

func TestRecoveryServiceRejectsEachUnknownStorageMetadataContradiction(t *testing.T) {
	for _, change := range []func(*rl.OperationRecord){
		func(r *rl.OperationRecord) { r.Operation.Kind = "Rerun" },
		func(r *rl.OperationRecord) { r.CollectionID = "collection" },
		func(r *rl.OperationRecord) { r.RoundID = "round" },
		func(r *rl.OperationRecord) { r.Revision = 1 },
		func(r *rl.OperationRecord) { r.FailureCode = contracts.StorageUnavailable },
		func(r *rl.OperationRecord) { r.Operation.PublicFingerprint[0] = 1 },
	} {
		record := operationRecord(contracts.OperationUnknown)
		change(&record)
		service, err := desktop.NewRecoveryService("session", validNow, operationReaderFunc(func(context.Context, contracts.OperationID) (rl.OperationRecord, error) { return record, nil }))
		if err != nil {
			t.Fatal(err)
		}
		response, err := service.GetOperation(context.Background(), contracts.OperationLookupRequest{OperationID: "operation"})
		if err != nil || response.OK || response.Data != nil || response.Code != contracts.InvalidState {
			t.Fatal("inconsistent no-row observation accepted")
		}
	}
}
