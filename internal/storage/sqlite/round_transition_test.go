package sqlite

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

type roundPortBarrier struct {
	entered chan struct{}
	release chan struct{}
}

func newRoundPortBarrier() *roundPortBarrier {
	return &roundPortBarrier{entered: make(chan struct{}), release: make(chan struct{})}
}
func (barrier *roundPortBarrier) wait(ctx context.Context) error {
	if barrier == nil {
		return nil
	}
	close(barrier.entered)
	select {
	case <-barrier.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type transitionStorage struct {
	*Store
	schedule, cancel, due *roundPortBarrier
	mu                    sync.Mutex
	dueOrder              []contracts.RoundID
	recoveryCalls         atomic.Int32
	secondRecovery        chan struct{}
}

func (storage *transitionStorage) SetSchedule(ctx context.Context, request rl.ScheduleRequest) (rl.RoundRecord, error) {
	if err := storage.schedule.wait(ctx); err != nil {
		return rl.RoundRecord{}, err
	}
	return storage.Store.SetSchedule(ctx, request)
}
func (storage *transitionStorage) CancelSchedule(ctx context.Context, request rl.CancelScheduleRequest) (rl.RoundRecord, error) {
	if err := storage.cancel.wait(ctx); err != nil {
		return rl.RoundRecord{}, err
	}
	return storage.Store.CancelSchedule(ctx, request)
}
func (storage *transitionStorage) AdmitRound(ctx context.Context, request rl.AdmitRoundRequest) (rl.Admission, error) {
	if request.Operation.Kind == "ExecuteDue" {
		if err := storage.due.wait(ctx); err != nil {
			return rl.Admission{}, err
		}
	}
	result, err := storage.Store.AdmitRound(ctx, request)
	if err == nil && request.Operation.Kind == "ExecuteDue" && !result.Replay {
		storage.mu.Lock()
		storage.dueOrder = append(storage.dueOrder, result.Round.ID)
		storage.mu.Unlock()
	}
	return result, err
}
func (storage *transitionStorage) ListRecoverableRounds(ctx context.Context) ([]rl.RoundRecord, error) {
	if storage.recoveryCalls.Add(1) == 2 && storage.secondRecovery != nil {
		close(storage.secondRecovery)
	}
	return storage.Store.ListRecoverableRounds(ctx)
}
func lifecycleWithStorage(t *testing.T, storage rl.Storage, entropy *productEntropy, clock func() time.Time) *rl.Service {
	t.Helper()
	var ids atomic.Uint32
	service, err := rl.NewService(rl.ServiceOptions{Storage: storage, Session: "session-one", Entropy: entropy, Clock: clock, NewID: func() string { return fmt.Sprintf("transition-%03d", ids.Add(1)) }, AppVersion: "transition-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return service
}

type roundCommandResult struct {
	round rl.RoundRecord
	err   error
}

func checkLosingRoundTransition(t *testing.T, err error) {
	t.Helper()
	var fault contracts.Fault
	if !errors.As(err, &fault) || (fault.Code != contracts.StaleRevision && fault.Code != contracts.InvalidState) {
		t.Fatalf("losing transition error=%v", err)
	}
}
func TestRoundTransitionSetScheduleAndCancelCompeteAtOneDurableRevision(t *testing.T) {
	for _, order := range []string{"schedule-first", "cancel-first", "simultaneous"} {
		t.Run(order, func(t *testing.T) {
			store := migratedTestStore(t)
			ports := &transitionStorage{Store: store}
			entropy := new(productEntropy)
			service := lifecycleWithStorage(t, ports, entropy, func() time.Time { return storageNow })
			pending, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ReservationMode))
			if err != nil {
				t.Fatal(err)
			}
			ports.schedule, ports.cancel = newRoundPortBarrier(), newRoundPortBarrier()
			setResult, cancelResult := make(chan roundCommandResult, 1), make(chan roundCommandResult, 1)
			delay := uint32(30)
			go func() {
				round, err := service.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: "schedule-race", Context: productRoundContext(pending, "session-one"), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
				setResult <- roundCommandResult{round, err}
			}()
			go func() {
				round, err := service.CancelSchedule(context.Background(), rl.CancelRequest{OperationID: "cancel-race", Context: productRoundContext(pending, "session-one")})
				cancelResult <- roundCommandResult{round, err}
			}()
			<-ports.schedule.entered
			<-ports.cancel.entered
			var set, cancel roundCommandResult
			switch order {
			case "schedule-first":
				close(ports.schedule.release)
				set = <-setResult
				close(ports.cancel.release)
				cancel = <-cancelResult
			case "cancel-first":
				close(ports.cancel.release)
				cancel = <-cancelResult
				close(ports.schedule.release)
				set = <-setResult
			default:
				close(ports.schedule.release)
				close(ports.cancel.release)
				set = <-setResult
				cancel = <-cancelResult
			}
			if (set.err == nil) == (cancel.err == nil) {
				t.Fatal("exactly one transition must commit", set, cancel)
			}
			stored, err := store.ReadRound(context.Background(), pending.ID)
			if err != nil || stored.Revision != pending.Revision+1 || stored.Version != pending.Version+1 || stored.Outcome != nil || entropy.calls.Load() != 0 {
				t.Fatal(stored, err)
			}
			winner, loser := contracts.OperationID("schedule-race"), contracts.OperationID("cancel-race")
			if set.err == nil {
				checkLosingRoundTransition(t, cancel.err)
				if stored.State != contracts.Scheduled {
					t.Fatal(stored)
				}
			} else {
				checkLosingRoundTransition(t, set.err)
				winner, loser = loser, winner
				if stored.State != contracts.Cancelled {
					t.Fatal(stored)
				}
			}
			winning, _ := store.ReadOperation(context.Background(), winner)
			losing, _ := store.ReadOperation(context.Background(), loser)
			if winning.Status != contracts.OperationSucceeded || winning.Revision != stored.Revision || losing.Status != contracts.OperationUnknown || roundTableCount(t, store, "operations") != 2 {
				t.Fatal(winning, losing)
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}
func TestRoundTransitionCancelAcceptedBeforeDeadlineCompetesWithDueClaim(t *testing.T) {
	for _, order := range []string{"cancel-first", "due-first", "simultaneous"} {
		t.Run(order, func(t *testing.T) {
			store := migratedTestStore(t)
			ports := &transitionStorage{Store: store}
			entropy := new(productEntropy)
			var clock atomic.Int64
			clock.Store(storageNow.UnixNano())
			now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
			service := lifecycleWithStorage(t, ports, entropy, now)
			pending, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ReservationMode))
			if err != nil {
				t.Fatal(err)
			}
			delay := uint32(30)
			scheduled, err := service.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: "set-before-race", Context: productRoundContext(pending, "session-one"), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
			if err != nil {
				t.Fatal(err)
			}
			clock.Store(scheduled.ScheduledAt.Add(-10*time.Second - time.Nanosecond).UnixNano())
			ports.cancel = newRoundPortBarrier()
			cancelResult := make(chan roundCommandResult, 1)
			go func() {
				round, err := service.CancelSchedule(context.Background(), rl.CancelRequest{OperationID: "cancel-due-race", Context: productRoundContext(scheduled, "session-one")})
				cancelResult <- roundCommandResult{round, err}
			}()
			<-ports.cancel.entered
			clock.Store(scheduled.ScheduledAt.UnixNano())
			ports.due = newRoundPortBarrier()
			dueResult := make(chan roundCommandResult, 1)
			go func() {
				round, err := service.ExecuteDue(context.Background(), rl.ExecuteDueRequest{RoundID: scheduled.ID})
				dueResult <- roundCommandResult{round, err}
			}()
			<-ports.due.entered
			var cancel, due roundCommandResult
			switch order {
			case "cancel-first":
				close(ports.cancel.release)
				cancel = <-cancelResult
				close(ports.due.release)
				due = <-dueResult
			case "due-first":
				close(ports.due.release)
				due = <-dueResult
				close(ports.cancel.release)
				cancel = <-cancelResult
			default:
				close(ports.cancel.release)
				close(ports.due.release)
				cancel = <-cancelResult
				due = <-dueResult
			}
			if (cancel.err == nil) == (due.err == nil) {
				t.Fatal("exactly one cancel/claim transition must commit", cancel, due)
			}
			stored, err := store.ReadRound(context.Background(), pending.ID)
			if err != nil {
				t.Fatal(err)
			}
			if cancel.err == nil {
				checkLosingRoundTransition(t, due.err)
				if stored.State != contracts.Cancelled || stored.Outcome != nil || entropy.calls.Load() != 0 || roundTableCount(t, store, "results") != 0 || stored.Revision != scheduled.Revision+1 {
					t.Fatal(stored)
				}
			} else {
				checkLosingRoundTransition(t, cancel.err)
				if stored.State != contracts.Completed || stored.Outcome == nil || entropy.calls.Load() != 1 || roundTableCount(t, store, "results") != 1 || stored.Revision != scheduled.Revision+2 {
					t.Fatal(stored)
				}
			}
			loser := contracts.OperationID("cancel-due-race")
			if cancel.err == nil {
				loser = contracts.OperationID(fmt.Sprintf("due:%s:1", scheduled.ID))
			}
			losing, _ := store.ReadOperation(context.Background(), loser)
			if losing.Status != contracts.OperationUnknown {
				t.Fatal("losing operation partially persisted", losing)
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}
func TestRoundTransitionCancelBoundaryMCDCIsStrictAndReplayDoesNotChangeRevision(t *testing.T) {
	for _, delta := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
		t.Run(delta.String(), func(t *testing.T) {
			store := migratedTestStore(t)
			now := storageNow
			service := productService(t, store, new(productEntropy), func() time.Time { return now }, "session-one", nil)
			pending, err := service.CreateCollection(context.Background(), productRequest(now, rl.ReservationMode))
			if err != nil {
				t.Fatal(err)
			}
			delay := uint32(30)
			scheduled, err := service.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: "set", Context: productRoundContext(pending, "session-one"), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
			if err != nil {
				t.Fatal(err)
			}
			now = scheduled.ScheduledAt.Add(-10*time.Second + delta)
			request := rl.CancelRequest{OperationID: "cancel", Context: productRoundContext(scheduled, "session-one")}
			result, err := service.CancelSchedule(context.Background(), request)
			if delta < 0 {
				if err != nil || result.State != contracts.Cancelled {
					t.Fatal(result, err)
				}
				replay, err := service.CancelSchedule(context.Background(), request)
				if err != nil || !reflect.DeepEqual(result, replay) {
					t.Fatal("cancel replay changed state", replay, err)
				}
			} else {
				requireFault(t, err, contracts.InvalidState)
				actual, err := store.ReadRound(context.Background(), scheduled.ID)
				if err != nil || !reflect.DeepEqual(actual, scheduled) || roundTableCount(t, store, "operations") != 2 {
					t.Fatal("boundary denial changed state", actual, err)
				}
			}
			assertNoHandles(t, store)
		})
	}
}
func scheduledRoundFixtures(t *testing.T, service *rl.Service) []rl.RoundRecord {
	t.Helper()
	result := make([]rl.RoundRecord, 0, 3)
	for index, seconds := range []uint32{60, 30, 30} {
		request := productRequest(storageNow, rl.ReservationMode)
		request.OperationID = contracts.OperationID(fmt.Sprintf("due-create-%d", index))
		request.Context.DraftID = contracts.DraftID(fmt.Sprintf("due-draft-%d", index))
		request.Collection.SourceDraftID = request.Context.DraftID
		pending, err := service.CreateCollection(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		round, err := service.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: contracts.OperationID(fmt.Sprintf("due-set-%d", index)), Context: productRoundContext(pending, "session-one"), QuickDelaySeconds: &seconds, Timezone: "Asia/Seoul"})
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, round)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ScheduledAt.Equal(*result[j].ScheduledAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].ScheduledAt.Before(*result[j].ScheduledAt)
	})
	return result
}
func TestRoundTransitionMultipleExpiredReservationsRecoverSequentiallyByDeadlineAndID(t *testing.T) {
	store := migratedTestStore(t)
	ports := &transitionStorage{Store: store}
	entropy := new(productEntropy)
	now := storageNow
	service := lifecycleWithStorage(t, ports, entropy, func() time.Time { return now })
	scheduled := scheduledRoundFixtures(t, service)
	now = now.Add(2 * time.Minute)
	report, err := service.Recover(context.Background(), rl.RecoverRequest{})
	want := []contracts.RoundID{scheduled[0].ID, scheduled[1].ID, scheduled[2].ID}
	if err != nil || !reflect.DeepEqual(report.Due, want) || !reflect.DeepEqual(report.Completed, want) || !reflect.DeepEqual(ports.dueOrder, want) || entropy.calls.Load() != 3 {
		t.Fatal(report, ports.dueOrder, err)
	}
	second, err := service.Recover(context.Background(), rl.RecoverRequest{})
	if err != nil || len(second.Due) != 0 || entropy.calls.Load() != 3 || roundTableCount(t, store, "results") != 3 || roundTableCount(t, store, "winners") != 3 {
		t.Fatal(second, err)
	}
	for _, old := range scheduled {
		current, err := store.ReadRound(context.Background(), old.ID)
		if err != nil || current.State != contracts.Completed || current.ScheduledAt == nil || !current.ScheduledAt.Equal(*old.ScheduledAt) || current.Outcome == nil || !current.Outcome.ExecutedAt.Equal(now) {
			t.Fatal(current, err)
		}
	}
	assertRoundSQLIntegrity(t, store)
	assertNoHandles(t, store)
}

type recoveryObservedContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (ctx *recoveryObservedContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}
func TestRoundTransitionDuplicateRecoveryWaitsForCurrentOrderedBatch(t *testing.T) {
	store := migratedTestStore(t)
	ports := &transitionStorage{Store: store, secondRecovery: make(chan struct{})}
	entropy := &productEntropy{entered: make(chan struct{}, 3), release: make(chan struct{})}
	now := storageNow
	service := lifecycleWithStorage(t, ports, entropy, func() time.Time { return now })
	scheduled := scheduledRoundFixtures(t, service)
	now = now.Add(2 * time.Minute)
	firstResult := make(chan error, 1)
	go func() { _, err := service.Recover(context.Background(), rl.RecoverRequest{}); firstResult <- err }()
	<-entropy.entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	observed := &recoveryObservedContext{Context: ctx, observed: make(chan struct{})}
	secondResult := make(chan rl.RecoveryReport, 1)
	secondError := make(chan error, 1)
	go func() {
		report, err := service.Recover(observed, rl.RecoverRequest{})
		secondResult <- report
		secondError <- err
	}()
	<-observed.observed
	// Without an ordered-recovery gate, the second ListRecoverableRounds is
	// reached before the first blocked entropy call finishes. Done() observation
	// is a deterministic entry barrier; no delay or polling is needed.
	reachedSecondRead := false
	select {
	case <-ports.secondRecovery:
		reachedSecondRead = true
	default:
	}
	close(entropy.release)
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
	report := <-secondResult
	if err := <-secondError; err != nil {
		t.Fatal(err)
	}
	if reachedSecondRead || len(report.Due) != 0 || entropy.calls.Load() != 3 {
		t.Fatal("duplicate wake ran an overlapping recovery batch", reachedSecondRead, report)
	}
	want := []contracts.RoundID{scheduled[0].ID, scheduled[1].ID, scheduled[2].ID}
	if !reflect.DeepEqual(ports.dueOrder, want) || roundTableCount(t, store, "results") != 3 {
		t.Fatal("recovery order changed", ports.dueOrder)
	}
	assertNoHandles(t, store)
}

func TestRoundTransitionCancelledDuplicateRecoveryDoesNotReadOrRetainGate(t *testing.T) {
	store := migratedTestStore(t)
	ports := &transitionStorage{Store: store, secondRecovery: make(chan struct{})}
	entropy := &productEntropy{entered: make(chan struct{}, 3), release: make(chan struct{})}
	now := storageNow
	service := lifecycleWithStorage(t, ports, entropy, func() time.Time { return now })
	scheduledRoundFixtures(t, service)
	now = now.Add(2 * time.Minute)
	first := make(chan error, 1)
	go func() { _, err := service.Recover(context.Background(), rl.RecoverRequest{}); first <- err }()
	<-entropy.entered
	ctx, cancel := context.WithCancel(context.Background())
	observed := &recoveryObservedContext{Context: ctx, observed: make(chan struct{})}
	second := make(chan error, 1)
	go func() { _, err := service.Recover(observed, rl.RecoverRequest{}); second <- err }()
	<-observed.observed
	cancel()
	if err := <-second; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if ports.recoveryCalls.Load() != 1 {
		t.Fatal("cancelled waiting recovery reached SQL", ports.recoveryCalls.Load())
	}
	close(entropy.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	report, err := service.Recover(context.Background(), rl.RecoverRequest{})
	if err != nil || len(report.Due) != 0 || entropy.calls.Load() != 3 {
		t.Fatal("cancelled waiter leaked recovery gate", report, err)
	}
	assertNoHandles(t, store)
}
