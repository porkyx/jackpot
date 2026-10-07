package sqlite

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/draw"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

// Err is the first dependency call after beginWork. These barriers expose the
// registered-work/Close boundary without sleeps or a production-only test hook.
type createShutdownContext struct {
	context.Context
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (ctx *createShutdownContext) Err() error {
	ctx.once.Do(func() {
		close(ctx.entered)
		select {
		case <-ctx.release:
		case <-ctx.Context.Done():
		}
	})
	return ctx.Context.Err()
}

type shutdownSelectContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (ctx *shutdownSelectContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.entered) })
	return ctx.Context.Done()
}
func awaitShutdownBarrier(t *testing.T, ctx context.Context, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("bounded shutdown barrier", ctx.Err())
	}
}
func receiveShutdown[T any](t *testing.T, ctx context.Context, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-ctx.Done():
		t.Fatal("bounded shutdown result", ctx.Err())
		var zero T
		return zero
	}
}

type createShutdownRead struct {
	*Store
	entered, release chan struct{}
	calls            atomic.Int32
}

func (reader *createShutdownRead) ReadOperation(ctx context.Context, id contracts.OperationID) (rl.OperationRecord, error) {
	if reader.calls.Add(1) == 1 {
		close(reader.entered)
		select {
		case <-reader.release:
		case <-ctx.Done():
			return rl.OperationRecord{}, ctx.Err()
		}
	}
	return reader.Store.ReadOperation(ctx, id)
}
func newShutdownService(t *testing.T, storage rl.Storage, entropy draw.Entropy, session contracts.BackendSessionID) *rl.Service {
	t.Helper()
	var id atomic.Uint32
	service, err := rl.NewService(rl.ServiceOptions{Storage: storage, Entropy: entropy, Session: session, Clock: func() time.Time { return storageNow }, NewID: func() string { return fmt.Sprintf("shutdown-%s-%d", session, id.Add(1)) }, AppVersion: "shutdown-regression"})
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

func TestRoundCreateConcurrentPublicPayloadAndOperationPairsHaveOneDurableSource(t *testing.T) {
	for _, kind := range []string{"different operation same public", "different operation different public", "same operation same public", "same operation different public"} {
		t.Run(kind, func(t *testing.T) {
			store := migratedTestStore(t)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			preparation := &createShutdownRead{Store: store, entered: make(chan struct{}), release: make(chan struct{})}
			entropy := &productEntropy{entered: make(chan struct{}, 1), release: make(chan struct{})}
			service := newShutdownService(t, preparation, entropy, "session-one")
			firstRequest := productRequest(storageNow, rl.ImmediateMode)
			secondRequest := productRequest(storageNow, rl.ImmediateMode)
			differentOperation := kind[:9] == "different"
			if differentOperation {
				secondRequest.OperationID = "other-create-operation"
			}
			switch kind {
			case "different operation different public", "same operation different public":
				secondRequest.Input.Message = "changed public"
			}
			secondContext := &createShutdownContext{Context: ctx, entered: make(chan struct{}), release: make(chan struct{})}
			firstDone, secondDone := make(chan roundCommandResult, 1), make(chan roundCommandResult, 1)
			var workers sync.WaitGroup
			workers.Add(2)
			finished := make(chan struct{})
			var waiterStarted bool
			var preparationRelease, secondRelease, entropyRelease sync.Once
			var secondStarted bool
			defer func() {
				if !secondStarted {
					workers.Done()
				}
				if !waiterStarted {
					go func() { workers.Wait(); close(finished) }()
				}
				preparationRelease.Do(func() { close(preparation.release) })
				secondRelease.Do(func() { close(secondContext.release) })
				entropyRelease.Do(func() { close(entropy.release) })
				cancel()
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				awaitShutdownBarrier(t, cleanup, finished)
			}()
			go func() {
				defer workers.Done()
				round, err := service.CreateCollection(ctx, firstRequest)
				firstDone <- roundCommandResult{round, err}
			}()
			awaitShutdownBarrier(t, ctx, preparation.entered)
			secondStarted = true
			go func() {
				defer workers.Done()
				round, err := service.CreateCollection(secondContext, secondRequest)
				secondDone <- roundCommandResult{round, err}
			}()
			waiterStarted = true
			go func() { workers.Wait(); close(finished) }()
			awaitShutdownBarrier(t, ctx, secondContext.entered)
			// Both calls have registered work before the first reaches admission/claim.
			preparationRelease.Do(func() { close(preparation.release) })
			awaitShutdownBarrier(t, ctx, entropy.entered)
			pending, err := store.ReadOperation(ctx, firstRequest.OperationID)
			if err != nil || pending.Status != contracts.OperationPending {
				t.Fatal(pending, err)
			}
			before := corruptionDurableSnapshot(t, store)
			secondRelease.Do(func() { close(secondContext.release) })
			second := receiveShutdown(t, ctx, secondDone)
			if differentOperation {
				var finalized *rl.FinalizedError
				if !errors.As(second.err, &finalized) || finalized.CollectionID != pending.CollectionID {
					t.Fatal("duplicate source did not reference first admission", second)
				}
			} else if kind == "same operation same public" {
				if second.err != nil || second.round.State != contracts.Executing || second.round.Outcome != nil || second.round.ID != pending.RoundID {
					t.Fatal("live replay altered execution", second)
				}
			} else if !errors.Is(second.err, rl.ErrOperationConflict) {
				t.Fatal("different public replay was accepted", second)
			}
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 1 || roundTableCount(t, store, "collections") != 1 || roundTableCount(t, store, "operations") != 1 || roundTableCount(t, store, "results") != 0 {
				t.Fatal("loser wrote/re-executed")
			}

			entropyRelease.Do(func() { close(entropy.release) })
			first := receiveShutdown(t, ctx, firstDone)
			if first.err != nil || first.round.State != contracts.Completed || first.round.Outcome == nil || first.round.Outcome.Winners[0].ParticipantID != "p1" {
				t.Fatal(first)
			}
			frozen, rounds, revision, err := store.ReadCollectionState(ctx, pending.CollectionID)
			if err != nil || frozen.SourceDraftID != firstRequest.Context.DraftID || frozen.FinalizedDraftRevision != 4 || revision != 3 || len(rounds) != 1 || rounds[0].ID != pending.RoundID || roundTableCount(t, store, "round_attempts") != 1 || roundTableCount(t, store, "results") != 1 || roundTableCount(t, store, "winners") != 1 || entropy.calls.Load() != 1 {
				t.Fatal("source/round/result uniqueness lost", frozen, rounds, err)
			}
			awaitShutdownBarrier(t, ctx, finished)
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}

func callShutdownEntry(service *rl.Service, ctx context.Context, kind string, initial rl.RoundRecord) error {
	var err error
	switch kind {
	case "prepare":
		request := productRequest(storageNow, rl.ImmediateMode)
		request.OperationID = "post-close-prepare"
		prepared, prepareErr := service.PrepareCreate(ctx, request)
		err = prepareErr
		if prepared != nil {
			prepared.Close()
			if err == nil {
				err = errors.New("closed prepare unexpectedly returned a lease")
			}
		}
	case "rerun":
		_, err = service.Rerun(ctx, rl.RerunRequest{OperationID: "post-close-rerun", Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: initial.CollectionID, Revision: initial.Revision}, Prizes: []rl.Prize{{ID: "prize", Count: 1}}, Mode: rl.ImmediateMode})
	case "schedule":
		delay := uint32(30)
		_, err = service.SetSchedule(ctx, rl.SetScheduleRequest{OperationID: "post-close-schedule", Context: productRoundContext(initial, "session-one"), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
	case "cancel":
		_, err = service.CancelSchedule(ctx, rl.CancelRequest{OperationID: "post-close-cancel", Context: productRoundContext(initial, "session-one")})
	case "retry":
		_, err = service.RetryRound(ctx, rl.RetryRequest{OperationID: "post-close-retry", Context: productRoundContext(initial, "session-one")})
	case "due":
		_, err = service.ExecuteDue(ctx, rl.ExecuteDueRequest{RoundID: initial.ID})
	case "recover":
		_, err = service.Recover(ctx, rl.RecoverRequest{})
	default:
		panic("Unknown controlled shutdown entry")
	}
	return err
}
func TestRoundRegisteredWorkBeforeCloseRejectsEveryNewEntryWithoutDurableMutation(t *testing.T) {
	for _, kind := range []string{"prepare", "rerun", "schedule", "cancel", "retry", "due", "recover"} {
		t.Run(kind, func(t *testing.T) {
			store := migratedTestStore(t)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			entropy := new(productEntropy)
			service := newShutdownService(t, store, entropy, "session-one")
			mode := rl.ImmediateMode
			if kind == "schedule" || kind == "cancel" || kind == "due" {
				mode = rl.ReservationMode
			}
			if kind == "retry" {
				entropy.fail.Store(true)
			}
			initial, err := service.CreateCollection(ctx, productRequest(storageNow, mode))
			if kind == "retry" {
				requireFault(t, err, contracts.InvalidState)
				entropy.fail.Store(false)
			} else if err != nil {
				t.Fatal(err)
			}
			before := corruptionDurableSnapshot(t, store)
			rngBefore := entropy.calls.Load()
			workContext := &createShutdownContext{Context: ctx, entered: make(chan struct{}), release: make(chan struct{})}
			workDone, closeDone := make(chan error, 1), make(chan error, 1)
			workerFinished, closerFinished := make(chan struct{}), make(chan struct{})
			var closerStarted bool
			var release sync.Once
			closeContext := &shutdownSelectContext{Context: ctx, entered: make(chan struct{})}
			defer func() {
				release.Do(func() { close(workContext.release) })
				cancel()
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				awaitShutdownBarrier(t, cleanup, workerFinished)
				if closerStarted {
					awaitShutdownBarrier(t, cleanup, closerFinished)
				}
			}()
			go func() {
				defer close(workerFinished)
				workDone <- callShutdownEntry(service, workContext, kind, initial)
			}()
			awaitShutdownBarrier(t, ctx, workContext.entered)
			closerStarted = true
			go func() { defer close(closerFinished); closeDone <- service.Close(closeContext) }()
			awaitShutdownBarrier(t, ctx, closeContext.entered)
			for _, freshKind := range []string{"prepare", "rerun", "schedule", "cancel", "retry", "due", "recover"} {
				requireFault(t, callShutdownEntry(service, ctx, freshKind, initial), contracts.InvalidState)
			}
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != rngBefore {
				t.Fatal("close admitted fresh work")
			}
			release.Do(func() { close(workContext.release) })
			requireFault(t, receiveShutdown(t, ctx, workDone), contracts.InvalidState)
			if err := receiveShutdown(t, ctx, closeDone); err != nil {
				t.Fatal(err)
			}
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != rngBefore {
				t.Fatal("shutdown fabricated durable work")
			}
			if err := service.Close(ctx); err != nil {
				t.Fatal("repeated close", err)
			}
			awaitShutdownBarrier(t, ctx, workerFinished)
			awaitShutdownBarrier(t, ctx, closerFinished)
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}

func TestRoundIndependentPreparedSnapshotsCloseWithoutSerialGateAndCannotAdmitAfterServiceClose(t *testing.T) {
	store := migratedTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entropy := new(productEntropy)
	service := newShutdownService(t, store, entropy, "session-one")
	request := productRequest(storageNow, rl.ImmediateMode)
	first, err := service.PrepareCreate(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	other := request
	other.OperationID = "independent-preparation"
	second, err := service.PrepareCreate(ctx, other)
	if err != nil {
		t.Fatal("a prepared snapshot serialized another local preparation", err)
	}
	defer second.Close()
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	for _, value := range []*rl.PreparedCreate{first, second} {
		admission, err := service.AdmitCreate(ctx, value, request.Collection)
		requireFault(t, err, contracts.InvalidState)
		if !reflect.DeepEqual(admission, rl.Admission{}) {
			t.Fatal("closed service admitted prepared snapshot")
		}
		value.Close()
		value.Close()
	}
	if entropy.calls.Load() != 0 || roundTableCount(t, store, "operations") != 0 || roundTableCount(t, store, "collections") != 0 {
		t.Fatal("closed prepared snapshots produced side effects")
	}
	assertRoundSQLIntegrity(t, store)
	assertNoHandles(t, store)
}

type multiShutdownEntropy struct {
	calls    atomic.Int32
	entered  chan int
	releases map[int]chan struct{}
}

func (entropy *multiShutdownEntropy) Intn(ctx context.Context, _ uint64) (uint64, error) {
	call := int(entropy.calls.Add(1))
	entropy.entered <- call
	select {
	case <-entropy.releases[call]:
		return 0, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
func TestRoundMultiCollectionShutdownWaitsBothClaimsOrPreservesBothForRecovery(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(fmt.Sprintf("deadline=%v", expire), func(t *testing.T) {
			store := migratedTestStore(t)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			entropy := &multiShutdownEntropy{entered: make(chan int, 2), releases: map[int]chan struct{}{1: make(chan struct{}), 2: make(chan struct{})}}
			service := newShutdownService(t, store, entropy, "session-one")
			admissions := make([]rl.Admission, 2)
			for index := range admissions {
				request := productRequest(storageNow, rl.ImmediateMode)
				request.OperationID = contracts.OperationID(fmt.Sprintf("multi-create-%d", index))
				request.Context.DraftID = contracts.DraftID(fmt.Sprintf("multi-draft-%d", index))
				request.Collection.SourceDraftID = request.Context.DraftID
				prepared, err := service.PrepareCreate(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				admissions[index], err = service.AdmitCreate(ctx, prepared, request.Collection)
				prepared.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			outputs := []chan roundCommandResult{make(chan roundCommandResult, 1), make(chan roundCommandResult, 1)}
			var workers sync.WaitGroup
			workers.Add(2)
			finished := make(chan struct{})
			var waiterStarted bool
			closeDone := make(chan error, 1)
			closerFinished := make(chan struct{})
			var closerStarted bool
			var firstRelease, secondRelease sync.Once
			var workerStarted [2]bool
			closingBase, expireClose := context.WithCancel(ctx)
			defer expireClose()
			closing := &shutdownSelectContext{Context: closingBase, entered: make(chan struct{})}
			defer func() {
				for _, started := range workerStarted {
					if !started {
						workers.Done()
					}
				}
				if !waiterStarted {
					go func() { workers.Wait(); close(finished) }()
				}
				firstRelease.Do(func() { close(entropy.releases[1]) })
				secondRelease.Do(func() { close(entropy.releases[2]) })
				expireClose()
				cancel()
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				awaitShutdownBarrier(t, cleanup, finished)
				if closerStarted {
					awaitShutdownBarrier(t, cleanup, closerFinished)
				}
			}()
			for index := range admissions {
				workerStarted[index] = true
				go func(index int) {
					defer workers.Done()
					round, err := service.ExecuteAdmission(ctx, admissions[index])
					outputs[index] <- roundCommandResult{round, err}
				}(index)
				if entered := receiveShutdown(t, ctx, entropy.entered); entered != index+1 {
					t.Fatal("claim entry order", entered)
				}
			}
			waiterStarted = true
			go func() { workers.Wait(); close(finished) }()
			before := corruptionDurableSnapshot(t, store)
			closerStarted = true
			go func() { defer close(closerFinished); closeDone <- service.Close(closing) }()
			awaitShutdownBarrier(t, ctx, closing.entered)
			_, err := service.CreateCollection(ctx, productRequest(storageNow, rl.ImmediateMode))
			requireFault(t, err, contracts.InvalidState)
			if entropy.calls.Load() != 2 || before != corruptionDurableSnapshot(t, store) {
				t.Fatal("shutdown accepted third collection")
			}
			if expire {
				expireClose()
				if err := receiveShutdown(t, ctx, closeDone); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				for _, output := range outputs {
					answer := receiveShutdown(t, ctx, output)
					if !errors.Is(answer.err, context.Canceled) || answer.round.Outcome != nil {
						t.Fatal("cancelled claim fabricated outcome", answer)
					}
				}
				if before != corruptionDurableSnapshot(t, store) || roundTableCount(t, store, "results") != 0 || roundTableCount(t, store, "winners") != 0 {
					t.Fatal("deadline rewrote durable claims")
				}
			} else {
				firstRelease.Do(func() { close(entropy.releases[1]) })
				first := receiveShutdown(t, ctx, outputs[0])
				if first.err != nil || first.round.State != contracts.Completed {
					t.Fatal(first)
				}
				second, err := store.ReadRound(ctx, admissions[1].Round.ID)
				if err != nil || second.State != contracts.Executing || second.Outcome != nil {
					t.Fatal("close must wait unfinished collection", second, err)
				}
				secondRelease.Do(func() { close(entropy.releases[2]) })
				secondAnswer := receiveShutdown(t, ctx, outputs[1])
				if secondAnswer.err != nil || secondAnswer.round.State != contracts.Completed || !reflect.DeepEqual(first.round.Outcome.Winners, secondAnswer.round.Outcome.Winners) {
					t.Fatal("second collection did not finish independently", secondAnswer)
				}
				if err := receiveShutdown(t, ctx, closeDone); err != nil {
					t.Fatal(err)
				}
			}
			awaitShutdownBarrier(t, ctx, finished)
			awaitShutdownBarrier(t, ctx, closerFinished)
			if err := service.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if expire {
				path := store.path
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := Open(ctx, path)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				recoveryEntropy := new(productEntropy)
				next := productService(t, reopened, recoveryEntropy, func() time.Time { return storageNow }, "new-session", nil)
				report, err := next.Recover(ctx, rl.RecoverRequest{})
				if err != nil || len(report.Failed) != 2 || recoveryEntropy.calls.Load() != 0 {
					t.Fatal("both interrupted collections must recover without drawing", report, err)
				}
				for _, admission := range admissions {
					round, err := reopened.ReadRound(ctx, admission.Round.ID)
					if err != nil || round.State != contracts.Failed || round.Outcome != nil {
						t.Fatal(round, err)
					}
				}
				assertRoundSQLIntegrity(t, reopened)
				assertNoHandles(t, reopened)
				if err := next.Close(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				if roundTableCount(t, store, "results") != 2 || roundTableCount(t, store, "winners") != 2 || roundTableCount(t, store, "collections") != 2 || entropy.calls.Load() != 2 {
					t.Fatal("multi collection uniqueness lost")
				}
				assertRoundSQLIntegrity(t, store)
				assertNoHandles(t, store)
			}
		})
	}
}
