package sqlite

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

type capturedRoundBarrier struct {
	entered   chan struct{}
	release   chan struct{}
	remaining atomic.Int32
}

func newCapturedRoundBarrier() *capturedRoundBarrier {
	barrier := &capturedRoundBarrier{entered: make(chan struct{}, 2), release: make(chan struct{})}
	barrier.remaining.Store(2)
	return barrier
}
func (barrier *capturedRoundBarrier) wait(ctx context.Context) error {
	if barrier == nil || barrier.remaining.Add(-1) < 0 {
		return nil
	}
	barrier.entered <- struct{}{}
	select {
	case <-barrier.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type entryConcurrencyStorage struct {
	*Store
	rerun, retry *capturedRoundBarrier
	target       contracts.RoundID
}

func (storage *entryConcurrencyStorage) ListRounds(ctx context.Context, id contracts.CollectionID) ([]rl.RoundRecord, error) {
	rounds, err := storage.Store.ListRounds(ctx, id)
	if err == nil {
		err = storage.rerun.wait(ctx)
	}
	return rounds, err
}
func (storage *entryConcurrencyStorage) ReadRound(ctx context.Context, id contracts.RoundID) (rl.RoundRecord, error) {
	round, err := storage.Store.ReadRound(ctx, id)
	if err == nil && id == storage.target && round.State == contracts.Failed {
		err = storage.retry.wait(ctx)
	}
	return round, err
}
func assertWinningEntryOperation(t *testing.T, store *Store, winner, loser contracts.OperationID) {
	t.Helper()
	successful, err := store.ReadOperation(context.Background(), winner)
	if err != nil || successful.Status != contracts.OperationSucceeded {
		t.Fatal(successful, err)
	}
	absent, err := store.ReadOperation(context.Background(), loser)
	if err != nil || absent.Status != contracts.OperationUnknown {
		t.Fatal("losing entry persisted", absent, err)
	}
}
func TestRoundEntryConcurrentRerunsCreateOnlyOneNewNumberAndPreservePastResult(t *testing.T) {
	store := migratedTestStore(t)
	ports := &entryConcurrencyStorage{Store: store}
	entropy := new(productEntropy)
	service := lifecycleWithStorage(t, ports, entropy, func() time.Time { return storageNow })
	first, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ImmediateMode))
	if err != nil {
		t.Fatal(err)
	}
	frozenBefore, err := store.ReadCollection(context.Background(), first.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	ports.rerun = newCapturedRoundBarrier()
	entropy.entered = make(chan struct{}, 1)
	entropy.release = make(chan struct{})
	results := make(chan struct {
		roundCommandResult
		operation contracts.OperationID
	}, 2)
	for _, operation := range []contracts.OperationID{"rerun-one", "rerun-two"} {
		go func(operation contracts.OperationID) {
			round, err := service.Rerun(context.Background(), rl.RerunRequest{OperationID: operation, Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: first.CollectionID, Revision: first.Revision}, Prizes: []rl.Prize{{ID: "second-prize", Name: "같은 이름", Count: 1}}, Mode: rl.ImmediateMode})
			results <- struct {
				roundCommandResult
				operation contracts.OperationID
			}{roundCommandResult{round, err}, operation}
		}(operation)
	}
	<-ports.rerun.entered
	<-ports.rerun.entered
	close(ports.rerun.release)
	<-entropy.entered
	loser := <-results
	checkLosingRoundTransition(t, loser.err)
	close(entropy.release)
	winner := <-results
	if winner.err != nil || winner.round.Number != 2 || winner.round.State != contracts.Completed || winner.round.Outcome == nil || winner.round.Outcome.Winners[0].ParticipantID != "p2" || entropy.calls.Load() != 2 {
		t.Fatal(winner)
	}
	assertWinningEntryOperation(t, store, winner.operation, loser.operation)
	rounds, err := store.ListRounds(context.Background(), first.CollectionID)
	if err != nil || len(rounds) != 2 || roundTableCount(t, store, "results") != 2 || roundTableCount(t, store, "winners") != 2 {
		t.Fatal(rounds, err)
	}
	preserved := rounds[0]
	first.Revision = preserved.Revision // revision is a current collection projection.
	if !reflect.DeepEqual(first, preserved) {
		t.Fatal("past round/result changed", first, preserved)
	}
	frozenAfter, err := store.ReadCollection(context.Background(), first.CollectionID)
	if err != nil || !reflect.DeepEqual(frozenBefore, frozenAfter) {
		t.Fatal("rerun changed frozen selection", err)
	}
	assertRoundSQLIntegrity(t, store)
	assertNoHandles(t, store)
}
func TestRoundEntryConcurrentRetriesReuseOneRoundAndAdmitOnlyOneNewAttempt(t *testing.T) {
	store := migratedTestStore(t)
	ports := &entryConcurrencyStorage{Store: store}
	entropy := new(productEntropy)
	entropy.fail.Store(true)
	service := lifecycleWithStorage(t, ports, entropy, func() time.Time { return storageNow })
	failed, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ImmediateMode))
	requireFault(t, err, contracts.InvalidState)
	ports.target = failed.ID
	ports.retry = newCapturedRoundBarrier()
	entropy.fail.Store(false)
	entropy.entered = make(chan struct{}, 1)
	entropy.release = make(chan struct{})
	results := make(chan struct {
		roundCommandResult
		operation contracts.OperationID
	}, 2)
	for _, operation := range []contracts.OperationID{"retry-one", "retry-two"} {
		go func(operation contracts.OperationID) {
			round, err := service.RetryRound(context.Background(), rl.RetryRequest{OperationID: operation, Context: productRoundContext(failed, "session-one")})
			results <- struct {
				roundCommandResult
				operation contracts.OperationID
			}{roundCommandResult{round, err}, operation}
		}(operation)
	}
	<-ports.retry.entered
	<-ports.retry.entered
	close(ports.retry.release)
	<-entropy.entered
	loser := <-results
	checkLosingRoundTransition(t, loser.err)
	close(entropy.release)
	winner := <-results
	if winner.err != nil || winner.round.ID != failed.ID || winner.round.Number != 1 || winner.round.Attempt != 2 || winner.round.State != contracts.Completed || !reflect.DeepEqual(winner.round.Input, failed.Input) || entropy.calls.Load() != 2 {
		t.Fatal(winner)
	}
	assertWinningEntryOperation(t, store, winner.operation, loser.operation)
	if roundTableCount(t, store, "rounds") != 1 || roundTableCount(t, store, "round_attempts") != 2 || roundTableCount(t, store, "results") != 1 || roundTableCount(t, store, "winners") != 1 {
		t.Fatal("duplicate retry resources")
	}
	assertRoundSQLIntegrity(t, store)
	assertNoHandles(t, store)
}
func TestRoundEntryEveryUnresolvedStateBlocksNewRerunWithoutSideEffects(t *testing.T) {
	for _, state := range []contracts.RoundState{contracts.PendingSchedule, contracts.Scheduled, contracts.Executing, contracts.Failed} {
		t.Run(string(state), func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			mode := rl.ReservationMode
			if state == contracts.Executing || state == contracts.Failed {
				mode = rl.ImmediateMode
			}
			request := productRequest(storageNow, mode)
			var current rl.RoundRecord
			var err error
			if state == contracts.Executing {
				prepared, err := service.PrepareCreate(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				admission, err := service.AdmitCreate(context.Background(), prepared, request.Collection)
				if err != nil {
					t.Fatal(err)
				}
				current = admission.Round
			} else {
				if state == contracts.Failed {
					entropy.fail.Store(true)
				}
				current, err = service.CreateCollection(context.Background(), request)
				if state == contracts.Failed {
					requireFault(t, err, contracts.InvalidState)
				} else if err != nil {
					t.Fatal(err)
				}
			}
			if state == contracts.Scheduled {
				delay := uint32(30)
				current, err = service.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: "set-before-blocked-rerun", Context: productRoundContext(current, "session-one"), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
				if err != nil {
					t.Fatal(err)
				}
			}
			beforeCalls := entropy.calls.Load()
			beforeRows := roundTableCount(t, store, "operations")
			_, err = service.Rerun(context.Background(), rl.RerunRequest{OperationID: "blocked-rerun", Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: current.CollectionID, Revision: current.Revision}, Prizes: []rl.Prize{{ID: "prize", Count: 1}}, Mode: rl.ImmediateMode})
			requireFault(t, err, contracts.InvalidState)
			after, err := store.ReadRound(context.Background(), current.ID)
			if err != nil || !reflect.DeepEqual(current, after) || entropy.calls.Load() != beforeCalls || roundTableCount(t, store, "operations") != beforeRows || roundTableCount(t, store, "rounds") != 1 {
				t.Fatal("blocked rerun changed state", after, err)
			}
			assertNoHandles(t, store)
		})
	}
}
