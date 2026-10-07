package sqlite

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	driverSQLite "modernc.org/sqlite"
)

func hintCreate(t *testing.T, service *rl.Service, key, mode string) rl.RoundRecord {
	t.Helper()
	request := productRequest(storageNow, mode)
	request.OperationID = contracts.OperationID("hint-create-" + key)
	request.Context.DraftID = contracts.DraftID("hint-draft-" + key)
	request.Collection.SourceDraftID = request.Context.DraftID
	round, err := service.CreateCollection(context.Background(), request)
	if err != nil {
		t.Fatal(round, err)
	}
	return round
}
func hintSchedule(t *testing.T, service *rl.Service, round rl.RoundRecord, delay uint32) rl.RoundRecord {
	t.Helper()
	result, err := service.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: contracts.OperationID("hint-schedule-" + string(round.ID)), Context: productRoundContext(round, "session-one"), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
	if err != nil {
		t.Fatal(result, err)
	}
	return result
}
func requireHintEmpty(t *testing.T, report rl.RecoveryReport, err error) {
	t.Helper()
	if err != nil || report.Completed == nil || report.Failed == nil || report.Due == nil || len(report.Completed)+len(report.Failed)+len(report.Due) != 0 {
		t.Fatal("expected confirmed empty timer report", report, err)
	}
}
func TestRecoveryHintActualSQLiteSixStatesAreReadOnlyAndOnlyScheduledOrExecutingMatch(t *testing.T) {
	for _, state := range []contracts.RoundState{contracts.PendingSchedule, contracts.Cancelled, contracts.Completed, contracts.Failed, contracts.Scheduled, contracts.Executing} {
		t.Run(string(state), func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			var round rl.RoundRecord
			if state == contracts.Executing {
				request := productRequest(storageNow, rl.ImmediateMode)
				prepared, err := service.PrepareCreate(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				defer prepared.Close()
				admission, err := service.AdmitCreate(context.Background(), prepared, request.Collection)
				if err != nil {
					t.Fatal(err)
				}
				round = admission.Round
			} else if state == contracts.Failed {
				entropy.fail.Store(true)
				var err error
				round, err = service.CreateCollection(context.Background(), productRequest(storageNow, rl.ImmediateMode))
				requireFault(t, err, contracts.InvalidState)
			} else {
				mode := rl.ReservationMode
				if state == contracts.Completed {
					mode = rl.ImmediateMode
				}
				round = hintCreate(t, service, "state", mode)
				if state == contracts.Scheduled || state == contracts.Cancelled {
					round = hintSchedule(t, service, round, 30)
					if state == contracts.Cancelled {
						var err error
						round, err = service.CancelSchedule(context.Background(), rl.CancelRequest{OperationID: "hint-cancel", Context: productRoundContext(round, "session-one")})
						if err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if round.State != state {
				t.Fatal("fixture state", round.State, state)
			}
			before := corruptionDurableSnapshot(t, store)
			calls := entropy.calls.Load()
			for n := 0; n < 3; n++ {
				present, err := store.HasRecoveryWork(context.Background())
				if err != nil || present != (state == contracts.Scheduled || state == contracts.Executing) {
					t.Fatal(present, err, state)
				}
			}
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != calls {
				t.Fatal("hint changed durable state or RNG")
			}
			assertNoHandles(t, store)
			assertRoundSQLIntegrity(t, store)
		})
	}
	t.Run("empty", func(t *testing.T) {
		store := migratedTestStore(t)
		before := corruptionDurableSnapshot(t, store)
		present, err := store.HasRecoveryWork(context.Background())
		if present || err != nil {
			t.Fatal(present, err)
		}
		if before != corruptionDurableSnapshot(t, store) {
			t.Fatal("empty hint wrote")
		}
		assertNoHandles(t, store)
	})
}
func TestRecoveryHintActualSQLiteContextClosedAndMissingSchemaNeverConfirmNoWork(t *testing.T) {
	for _, name := range []string{"nil", "cancelled", "deadline", "closed", "missing schema"} {
		t.Run(name, func(t *testing.T) {
			store := migratedTestStore(t)
			ctx := context.Background()
			switch name {
			case "nil":
				ctx = nil
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Unix(0, 0))
				defer cancel()
			case "closed":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			case "missing schema":
				execSQL(t, store.db, "DROP TABLE rounds")
			}
			present, err := store.HasRecoveryWork(ctx)
			if present || err == nil {
				t.Fatal("unknown DB state confirmed as no work", present, err)
			}
			if name == "nil" {
				requireFault(t, err, contracts.InvalidInput)
			}
			if name == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if name == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			assertNoHandles(t, store)
		})
	}
}

type hintReadProbe struct {
	calls      atomic.Int32
	at         int32
	continuous bool
	cancel     context.CancelFunc
}

var hintReadRegistration sync.Once
var hintReadRegistrationError error
var activeHintReadProbe atomic.Pointer[hintReadProbe]

func registerHintReadProbe(t *testing.T) {
	t.Helper()
	hintReadRegistration.Do(func() {
		hintReadRegistrationError = driverSQLite.RegisterScalarFunction("jackpot_timer_hint_probe", 1, func(_ *driverSQLite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if p := activeHintReadProbe.Load(); p != nil {
				call := p.calls.Add(1)
				if call == p.at || p.continuous && call >= p.at {
					if p.cancel != nil {
						p.cancel()
					} else {
						return nil, errors.New("actual SQLite hint read failure")
					}
				}
			}
			return args[0], nil
		})
	})
	if hintReadRegistrationError != nil {
		t.Fatal(hintReadRegistrationError)
	}
}
func TestRecoveryHintActualSQLFirstNthContinuousAndCancelLeaveEveryTableUnchanged(t *testing.T) {
	for _, name := range []string{"first", "Nth after two successful rows", "continuous", "cancel after two rows"} {
		t.Run(name, func(t *testing.T) {
			registerHintReadProbe(t)
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			for i := 0; i < 3; i++ {
				hintCreate(t, service, fmt.Sprint(i), rl.ImmediateMode)
			}
			// Real SQLite invokes this scalar during the EXISTS scan. No lifecycle result is mocked.
			execSQL(t, store.db, `ALTER TABLE rounds RENAME TO timer_hint_round_rows;
 CREATE VIEW rounds AS SELECT collection_id,id,number,jackpot_timer_hint_probe(state) AS state,version,attempt,input_json,active_operation_id,scheduled_at,timezone,claim_json,failure_code FROM timer_hint_round_rows`)
			before := corruptionDurableSnapshot(t, store)
			calls := entropy.calls.Load()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &hintReadProbe{at: 1}
			if name == "Nth after two successful rows" || name == "cancel after two rows" {
				probe.at = 3
			}
			probe.continuous = name == "continuous"
			if name == "cancel after two rows" {
				probe.cancel = cancel
			}
			activeHintReadProbe.Store(probe)
			defer activeHintReadProbe.Store(nil)
			report, err := service.RecoverTimer(ctx, rl.RecoverRequest{})
			if err == nil || !reflect.DeepEqual(report, rl.RecoveryReport{}) || probe.calls.Load() < probe.at {
				t.Fatal("partial SQL scan confirmed no work", report, err, probe.calls.Load())
			}
			if probe.cancel == nil {
				requireFault(t, err, contracts.StorageUnavailable)
			} else if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if probe.continuous {
				report, err = service.RecoverTimer(context.Background(), rl.RecoverRequest{})
				if err == nil || !reflect.DeepEqual(report, rl.RecoveryReport{}) {
					t.Fatal("continuous failure recovered", report, err)
				}
			}
			activeHintReadProbe.Store(nil)
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != calls {
				t.Fatal("hint fault wrote rows or used RNG")
			}
			report, err = service.RecoverTimer(context.Background(), rl.RecoverRequest{})
			requireHintEmpty(t, report, err)
			assertNoHandles(t, store)
		})
	}
}

// Capture the committed SQL hint and hold only the return value, with no open DB
// cursor/transaction. Other admissions/cancellations cross a deterministic barrier.
type barrierHintStore struct {
	*Store
	entered chan bool
	release chan struct{}
	once    atomic.Bool
}

func (store *barrierHintStore) HasRecoveryWork(ctx context.Context) (bool, error) {
	present, err := store.Store.HasRecoveryWork(ctx)
	if err != nil {
		return false, err
	}
	if store.once.CompareAndSwap(false, true) {
		store.entered <- present
		select {
		case <-store.release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return present, nil
}
func hintBarrierService(t *testing.T, store *Store, entropy *productEntropy, clock func() time.Time) (*rl.Service, *barrierHintStore) {
	t.Helper()
	wrapped := &barrierHintStore{Store: store, entered: make(chan bool, 1), release: make(chan struct{})}
	var ids atomic.Int32
	service, err := rl.NewService(rl.ServiceOptions{Storage: wrapped, Session: "session-one", Entropy: entropy, Clock: clock, NewID: func() string { return fmt.Sprintf("hint-id-%d", ids.Add(1)) }, AppVersion: "hint-test"})
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
	return service, wrapped
}
func TestRecoveryHintFalseThenAdmissionCannotSuppressImmediateOrNextTickDueExecution(t *testing.T) {
	for _, mode := range []string{rl.ImmediateMode, rl.ReservationMode} {
		t.Run(mode, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			var clock atomic.Int64
			clock.Store(storageNow.UnixNano())
			service, wrapped := hintBarrierService(t, store, entropy, func() time.Time { return time.Unix(0, clock.Load()).UTC() })
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			type reply struct {
				report rl.RecoveryReport
				err    error
			}
			done := make(chan reply, 1)
			go func() { report, err := service.RecoverTimer(ctx, rl.RecoverRequest{}); done <- reply{report, err} }()
			if <-wrapped.entered {
				t.Fatal("empty durable hint unexpectedly true")
			}
			round := hintCreate(t, service, "cross-hint", mode)
			if mode == rl.ReservationMode {
				round = hintSchedule(t, service, round, 30)
			}
			close(wrapped.release)
			r := <-done
			requireHintEmpty(t, r.report, r.err)
			if mode == rl.ImmediateMode && (round.State != contracts.Completed || entropy.calls.Load() != 1) {
				t.Fatal("false hint suppressed direct execution", round, entropy.calls.Load())
			}
			clock.Store(storageNow.Add(40 * time.Second).UnixNano())
			report, err := service.RecoverTimer(ctx, rl.RecoverRequest{})
			if err != nil {
				t.Fatal(report, err)
			}
			if mode == rl.ReservationMode && (len(report.Completed) != 1 || len(report.Due) != 1 || report.Completed[0] != round.ID) {
				t.Fatal("next tick lost newly admitted schedule", report)
			}
			if entropy.calls.Load() != 1 {
				t.Fatal("duplicate or missing execution", entropy.calls.Load())
			}
			report, err = service.RecoverTimer(ctx, rl.RecoverRequest{})
			requireHintEmpty(t, report, err)
			actual, err := store.ReadRound(ctx, round.ID)
			if err != nil || actual.State != contracts.Completed || actual.Outcome == nil || len(actual.Outcome.Winners) != 1 {
				t.Fatal(actual, err)
			}
			assertNoHandles(t, store)
			assertRoundSQLIntegrity(t, store)
		})
	}
}
func TestRecoveryHintTrueThenCancellationUsesCurrentFullRecoveryStateAndNoEntropy(t *testing.T) {
	store := migratedTestStore(t)
	entropy := new(productEntropy)
	service, wrapped := hintBarrierService(t, store, entropy, func() time.Time { return storageNow })
	round := hintSchedule(t, service, hintCreate(t, service, "cancel-hint", rl.ReservationMode), 30)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := service.RecoverTimer(ctx, rl.RecoverRequest{}); done <- err }()
	if !<-wrapped.entered {
		t.Fatal("scheduled durable hint unexpectedly false")
	}
	cancelled, err := service.CancelSchedule(ctx, rl.CancelRequest{OperationID: "hint-cancel-race", Context: productRoundContext(round, "session-one")})
	if err != nil {
		t.Fatal(err)
	}
	before := corruptionDurableSnapshot(t, store)
	close(wrapped.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if cancelled.State != contracts.Cancelled || entropy.calls.Load() != 0 || before != corruptionDurableSnapshot(t, store) {
		t.Fatal("stale true hint executed cancelled round")
	}
	assertNoHandles(t, store)
	assertRoundSQLIntegrity(t, store)
}
func TestRecoveryHintSeveralDueRoundsAndDuplicateTimersPreserveOrderAndExactlyOnce(t *testing.T) {
	store := migratedTestStore(t)
	entropy := new(productEntropy)
	var clock atomic.Int64
	clock.Store(storageNow.UnixNano())
	var completed []contracts.RoundID
	var mu sync.Mutex
	service := productService(t, store, entropy, func() time.Time { return time.Unix(0, clock.Load()).UTC() }, "session-one", func(ctx context.Context, notice contracts.StateNotice) error {
		rounds, err := store.ListRounds(ctx, contracts.CollectionID(notice.EntityID))
		if err != nil {
			return err
		}
		if rounds[len(rounds)-1].State == contracts.Completed {
			mu.Lock()
			completed = append(completed, rounds[len(rounds)-1].ID)
			mu.Unlock()
		}
		return nil
	})
	later := hintSchedule(t, service, hintCreate(t, service, "later", rl.ReservationMode), 30)
	earlier := hintSchedule(t, service, hintCreate(t, service, "earlier", rl.ReservationMode), 10)
	clock.Store(storageNow.Add(40 * time.Second).UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := make(chan struct{})
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; _, err := service.RecoverTimer(ctx, rl.RecoverRequest{}); done <- err }()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(completed, []contracts.RoundID{earlier.ID, later.ID}) || entropy.calls.Load() != 2 {
		t.Fatal("unordered or duplicate due execution", completed, entropy.calls.Load())
	}
	var results, winners int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM results").Scan(&results); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT COUNT(*) FROM winners").Scan(&winners); err != nil {
		t.Fatal(err)
	}
	if results != 2 || winners != 2 {
		t.Fatal(results, winners)
	}
	report, err := service.RecoverTimer(ctx, rl.RecoverRequest{})
	requireHintEmpty(t, report, err)
	assertNoHandles(t, store)
	assertRoundSQLIntegrity(t, store)
}
func TestRecoveryHintIdleDoesNotChangeExplicitCorruptionGuardAndActiveTimerValidatesAllPast(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			var clock atomic.Int64
			clock.Store(storageNow.UnixNano())
			service := productService(t, store, entropy, func() time.Time { return time.Unix(0, clock.Load()).UTC() }, "session-one", nil)
			completed := hintCreate(t, service, "past", rl.ImmediateMode)
			if active {
				hintSchedule(t, service, hintCreate(t, service, "future", rl.ReservationMode), 30)
			}
			corruptionMode(t, store)
			execSQL(t, store.db, "UPDATE results SET outcome_json=json_set(outcome_json,'$.AppVersion','') WHERE round_id=?", completed.ID)
			before := corruptionDurableSnapshot(t, store)
			calls := entropy.calls.Load()
			clock.Store(storageNow.Add(40 * time.Second).UnixNano())
			report, err := service.RecoverTimer(context.Background(), rl.RecoverRequest{})
			if active {
				if err == nil || !reflect.DeepEqual(report, rl.RecoveryReport{}) {
					t.Fatal("active timer skipped damaged completed history", report, err)
				}
			} else {
				requireHintEmpty(t, report, err)
			}
			report, err = service.Recover(context.Background(), rl.RecoverRequest{})
			if err == nil || !reflect.DeepEqual(report, rl.RecoveryReport{}) {
				t.Fatal("explicit recovery skipped corrupted idle history", report, err)
			}
			snapshot, revision, err := store.ReadCollectionSnapshot(context.Background(), completed.CollectionID)
			if err == nil || revision != 0 || !reflect.DeepEqual(snapshot, rl.FrozenCollection{}) {
				t.Fatal("snapshot guard weakened", snapshot, revision, err)
			}
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != calls {
				t.Fatal("corruption accepted writes or RNG")
			}
			assertNoHandles(t, store)
		})
	}
}

func TestRecoveryHintExecutingAdmissionRemainsLiveAndRestartIsRecoveredWithoutRNG(t *testing.T) {
	store := migratedTestStore(t)
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
	request := productRequest(storageNow, rl.ImmediateMode)
	prepared, err := service.PrepareCreate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	admission, err := service.AdmitCreate(context.Background(), prepared, request.Collection)
	if err != nil {
		t.Fatal(err)
	}
	before := corruptionDurableSnapshot(t, store)
	report, err := service.RecoverTimer(context.Background(), rl.RecoverRequest{})
	requireHintEmpty(t, report, err)
	if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 0 {
		t.Fatal("timer retired live admission")
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := productService(t, store, entropy, func() time.Time { return storageNow }, "session-two", nil)
	report, err = restarted.RecoverTimer(context.Background(), rl.RecoverRequest{})
	if err != nil || !reflect.DeepEqual(report.Failed, []contracts.RoundID{admission.Round.ID}) || len(report.Completed)+len(report.Due) != 0 {
		t.Fatal(report, err)
	}
	if entropy.calls.Load() != 0 {
		t.Fatal("restart recovered by draw or granted authority")
	}
	after := corruptionDurableSnapshot(t, store)
	report, err = restarted.RecoverTimer(context.Background(), rl.RecoverRequest{})
	requireHintEmpty(t, report, err)
	if after != corruptionDurableSnapshot(t, store) {
		t.Fatal("duplicate retirement mutated state")
	}
	round, err := store.ReadRound(context.Background(), admission.Round.ID)
	if err != nil || round.State != contracts.Failed || round.Outcome != nil {
		t.Fatal(round, err)
	}
	assertNoHandles(t, store)
	assertRoundSQLIntegrity(t, store)
}

type blockingHintProbe struct {
	entered chan struct{}
	release chan struct{}
}

var blockingHintRegistration sync.Once
var blockingHintRegistrationError error
var activeBlockingHintProbe atomic.Pointer[blockingHintProbe]

func TestRecoveryHintCloseWaitsForAcceptedActualSQLReadThenRefusesWithoutWrites(t *testing.T) {
	blockingHintRegistration.Do(func() {
		blockingHintRegistrationError = driverSQLite.RegisterScalarFunction("jackpot_timer_hint_block", 1, func(_ *driverSQLite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if p := activeBlockingHintProbe.Load(); p != nil {
				close(p.entered)
				<-p.release
			}
			return args[0], nil
		})
	})
	if blockingHintRegistrationError != nil {
		t.Fatal(blockingHintRegistrationError)
	}
	store := migratedTestStore(t)
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
	hintCreate(t, service, "blocking-read", rl.ImmediateMode)
	execSQL(t, store.db, `ALTER TABLE rounds RENAME TO timer_hint_block_rows;
 CREATE VIEW rounds AS SELECT collection_id,id,number,jackpot_timer_hint_block(state) AS state,version,attempt,input_json,active_operation_id,scheduled_at,timezone,claim_json,failure_code FROM timer_hint_block_rows`)
	before := corruptionDurableSnapshot(t, store)
	calls := entropy.calls.Load()
	probe := &blockingHintProbe{entered: make(chan struct{}), release: make(chan struct{})}
	activeBlockingHintProbe.Store(probe)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(probe.release) }) }
	defer release()
	defer activeBlockingHintProbe.Store(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := service.RecoverTimer(ctx, rl.RecoverRequest{}); done <- err }()
	select {
	case <-probe.entered:
	case <-ctx.Done():
		t.Fatal("SQL read not entered", ctx.Err())
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- service.Close(ctx) }()
	// A cancelled explicit Recover is safe to use as a public closed-admission
	// probe: before Close it is Canceled; afterwards beginWork is InvalidState.
	cancelled, cancelProbe := context.WithCancel(context.Background())
	cancelProbe()
	for {
		_, err := service.Recover(cancelled, rl.RecoverRequest{})
		var fault contracts.Fault
		if errors.As(err, &fault) && fault.Code == contracts.InvalidState {
			break
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("unexpected close admission", err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("Close admission not reached", ctx.Err())
		default:
			runtime.Gosched()
		}
	}
	select {
	case err := <-closeDone:
		t.Fatal("Close returned with accepted SQL read", err)
	default:
	}
	release()
	requireFault(t, <-done, contracts.InvalidState)
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	activeBlockingHintProbe.Store(nil)
	if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != calls {
		t.Fatal("close gap wrote or drew")
	}
	assertNoHandles(t, store)
}
