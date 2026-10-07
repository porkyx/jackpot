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

	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

type productEntropy struct {
	calls   atomic.Int32
	fail    atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (entropy *productEntropy) Intn(ctx context.Context, bound uint64) (uint64, error) {
	entropy.calls.Add(1)
	if entropy.entered != nil {
		entropy.entered <- struct{}{}
		select {
		case <-entropy.release:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	if entropy.fail.Load() {
		return 0, errors.New("entropy unavailable")
	}
	return 0, nil
}
func productRequest(now time.Time, mode string) rl.CreateCollectionRequest {
	return rl.CreateCollectionRequest{OperationID: "create-op", Context: contracts.DraftContext{BackendSessionID: "session-one", DraftID: "draft-one", Revision: 4, ArticleGeneration: 2}, Collection: rl.FrozenCollection{SourceDraftID: "draft-one", FinalizedDraftRevision: 4, ArticleGeneration: 2, Snapshot: contracts.SnapshotSummary{SnapshotID: "snapshot-one", CollectedAt: now, Complete: true, Pages: 1}, Article: rl.ArticleSnapshot{URL: "https://gall.dcinside.com/board/view/?id=test&no=1", Title: "actual snapshot"}, Participants: []rl.ParticipantSnapshot{{ID: "p1", Nickname: "one", Included: true}, {ID: "p2", Nickname: "two", Included: true}, {ID: "p3", Nickname: "three", Included: true}}}, Input: rl.RoundInput{CandidateIDs: []contracts.ParticipantID{"p1", "p2", "p3"}, Prizes: []rl.Prize{{ID: "prize-one", Name: "상품", Count: 1}}, Message: "축하", Mode: mode}}
}
func productService(t *testing.T, store *Store, entropy *productEntropy, clock func() time.Time, session contracts.BackendSessionID, publish func(context.Context, contracts.StateNotice) error) *rl.Service {
	t.Helper()
	var counter atomic.Int32
	service, err := rl.NewService(rl.ServiceOptions{Storage: store, Session: session, Entropy: entropy, Clock: clock, NewID: func() string { return fmt.Sprintf("id-%d", counter.Add(1)) }, Publish: publish, AppVersion: "test-product-1"})
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
func productRoundContext(round rl.RoundRecord, session contracts.BackendSessionID) contracts.RoundContext {
	return contracts.RoundContext{CollectionContext: contracts.CollectionContext{BackendSessionID: session, CollectionID: round.CollectionID, Revision: round.Revision}, RoundID: round.ID, Version: round.Version}
}
func requireFault(t *testing.T, err error, code contracts.ErrorCode) {
	t.Helper()
	var fault contracts.Fault
	if !errors.As(err, &fault) || fault.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}
func TestProductFirstCreateCommitsBeforePublishingAndReplaysWithoutEntropy(t *testing.T) {
	store := migratedTestStore(t)
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)

	entropy := new(productEntropy)
	notices := make([]contracts.StateNotice, 0)
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", func(ctx context.Context, notice contracts.StateNotice) error {
		rounds, err := store.ListRounds(ctx, contracts.CollectionID(notice.EntityID))
		if err != nil {
			return err
		}
		if len(rounds) != 1 || rounds[0].Revision != notice.Revision {
			t.Errorf("published before committed: %+v", notice)
		}
		notices = append(notices, notice)
		return errors.New("notification delivery lost")
	})
	request := productRequest(now, rl.ImmediateMode)
	result, err := service.CreateCollection(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != contracts.Completed || result.Revision != 3 || result.Version != 3 || result.Outcome == nil || result.Outcome.Winners[0].ParticipantID != "p1" || result.Outcome.AppVersion != "test-product-1" {
		t.Fatalf("actual result: %+v", result)
	}
	if entropy.calls.Load() != 1 || len(notices) != 2 {
		t.Fatalf("side effects entropy=%d notices=%d", entropy.calls.Load(), len(notices))
	}
	again, err := service.CreateCollection(context.Background(), request)
	if err != nil || !reflect.DeepEqual(result, again) || entropy.calls.Load() != 1 {
		t.Fatalf("replay %+v/%v", again, err)
	}

	conflict := request
	conflict.Input.Message = "different"
	if _, err = service.CreateCollection(context.Background(), conflict); !errors.Is(err, rl.ErrOperationConflict) {
		t.Fatal(err)
	}
	request.OperationID = "new-create-op"
	_, err = service.CreateCollection(context.Background(), request)
	var finalized *rl.FinalizedError
	if !errors.As(err, &finalized) || finalized.CollectionID != result.CollectionID {
		t.Fatalf("finalized source must not create another collection: %v", err)
	}
	frozen, rounds, revision, err := store.ReadCollectionState(context.Background(), result.CollectionID)
	if err != nil || len(frozen.Participants) != 3 || len(rounds) != 1 || revision != 3 || rounds[0].Revision != revision {
		t.Fatalf("atomic query %+v/%v", rounds, err)
	}
	count, err := store.CollectionCount(context.Background())
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
	history, err := store.ListCollections(context.Background(), 0, 100)
	if err != nil || len(history) != 1 || history[0].LatestRound.ID != result.ID || history[0].Title != "actual snapshot" {
		t.Fatalf("history %+v/%v", history, err)
	}
	assertNoHandles(t, store)
}
func TestProductRerunSubtractsAllPreviousWinnersIncludingSingleRemaining(t *testing.T) {
	store := migratedTestStore(t)
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
	first, err := service.CreateCollection(context.Background(), productRequest(now, rl.ImmediateMode))
	if err != nil {
		t.Fatal(err)
	}
	previous := first
	for index := 2; index <= 3; index++ {
		request := rl.RerunRequest{OperationID: contracts.OperationID(fmt.Sprintf("rerun-%d", index)), Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: first.CollectionID, Revision: previous.Revision}, Prizes: []rl.Prize{{ID: rl.PrizeID(fmt.Sprintf("prize-%d", index)), Name: "상품", Count: 1}}, Mode: rl.ImmediateMode}
		next, err := service.Rerun(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if next.Number != uint32(index) || next.Outcome.Winners[0].ParticipantID != contracts.ParticipantID(fmt.Sprintf("p%d", index)) || len(next.Input.CandidateIDs) != 4-index {
			t.Fatalf("remaining frozen candidates: %+v", next)
		}
		replay, err := service.Rerun(context.Background(), request)
		if err != nil || replay.ID != next.ID || entropy.calls.Load() != int32(index) {
			t.Fatalf("rerun replay %+v/%v", replay, err)
		}
		previous = next
	}
	_, err = service.Rerun(context.Background(), rl.RerunRequest{OperationID: "empty-rerun", Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: first.CollectionID, Revision: previous.Revision}, Prizes: []rl.Prize{{ID: "p", Count: 1}}, Mode: rl.ImmediateMode})
	requireFault(t, err, contracts.InvalidInput)
	rounds, err := store.ListRounds(context.Background(), first.CollectionID)
	if err != nil || len(rounds) != 3 {
		t.Fatal(rounds, err)
	}
	assertNoHandles(t, store)
}
func TestProductReservationUsesExactTenSecondBoundariesAndDueExecutesOnce(t *testing.T) {
	store := migratedTestStore(t)
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
	pending, err := service.CreateCollection(context.Background(), productRequest(now, rl.ReservationMode))
	if err != nil || pending.State != contracts.PendingSchedule || entropy.calls.Load() != 0 {
		t.Fatalf("pending %+v/%v", pending, err)
	}
	input := rl.SetScheduleRequest{OperationID: "schedule", Context: productRoundContext(pending, "session-one"), ScheduledAt: now.Add(10*time.Second - time.Nanosecond), Timezone: "Asia/Seoul"}
	_, err = service.SetSchedule(context.Background(), input)
	requireFault(t, err, contracts.InvalidInput)
	quick := uint32(10)
	input.ScheduledAt = time.Time{}
	input.QuickDelaySeconds = &quick
	scheduled, err := service.SetSchedule(context.Background(), input)
	if err != nil || scheduled.State != contracts.Scheduled {
		t.Fatal(scheduled, err)
	}
	_, err = service.CancelSchedule(context.Background(), rl.CancelRequest{OperationID: "cancel-too-late", Context: productRoundContext(scheduled, "session-one")})
	requireFault(t, err, contracts.InvalidState)
	early, err := service.ExecuteDue(context.Background(), rl.ExecuteDueRequest{RoundID: scheduled.ID})
	if err != nil || early.State != contracts.Scheduled || entropy.calls.Load() != 0 {
		t.Fatal(early, err)
	}
	now = now.Add(10 * time.Second)
	completed, err := service.ExecuteDue(context.Background(), rl.ExecuteDueRequest{RoundID: scheduled.ID})
	if err != nil || completed.State != contracts.Completed || entropy.calls.Load() != 1 {
		t.Fatalf("due %+v/%v", completed, err)
	}
	again, err := service.ExecuteDue(context.Background(), rl.ExecuteDueRequest{RoundID: scheduled.ID})
	if err != nil || again.State != contracts.Completed || entropy.calls.Load() != 1 {
		t.Fatal(again, err)
	}
	op, err := store.ReadOperation(context.Background(), contracts.OperationID(fmt.Sprintf("due:%s:1", scheduled.ID)))
	if err != nil || op.Status != contracts.OperationSucceeded {
		t.Fatal(op, err)
	}
	assertNoHandles(t, store)
}
func TestProductEntropyFailureIsDurableAndRetryReusesImmutableRound(t *testing.T) {
	store := migratedTestStore(t)
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	entropy := new(productEntropy)
	entropy.fail.Store(true)
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
	failed, err := service.CreateCollection(context.Background(), productRequest(now, rl.ImmediateMode))
	requireFault(t, err, contracts.InvalidState)
	if failed.State != contracts.Failed || failed.Outcome != nil {
		t.Fatal(failed)
	}
	entropy.fail.Store(false)
	retried, err := service.RetryRound(context.Background(), rl.RetryRequest{OperationID: "retry", Context: productRoundContext(failed, "session-one")})
	if err != nil || retried.State != contracts.Completed || retried.ID != failed.ID || retried.Attempt != 2 || !reflect.DeepEqual(retried.Input, failed.Input) {
		t.Fatal(retried, err)
	}
	var attempts, results, winners int
	if err = store.db.QueryRow("SELECT count(*) FROM round_attempts").Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	store.db.QueryRow("SELECT count(*) FROM results").Scan(&results)
	store.db.QueryRow("SELECT count(*) FROM winners").Scan(&winners)
	if attempts != 2 || results != 1 || winners != 1 || entropy.calls.Load() != 2 {
		t.Fatalf("atomic rows attempts=%d results=%d winners=%d", attempts, results, winners)
	}
	assertNoHandles(t, store)
}
func TestProductResultWriteRollbackNeverPublishesWinnerAndCanRetry(t *testing.T) {
	store := migratedTestStore(t)
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
	if _, err := store.db.Exec("CREATE TRIGGER product_result_fault BEFORE INSERT ON results BEGIN SELECT RAISE(ABORT,'injected result write'); END"); err != nil {
		t.Fatal(err)
	}
	failed, err := service.CreateCollection(context.Background(), productRequest(now, rl.ImmediateMode))
	requireFault(t, err, contracts.StorageUnavailable)
	if failed.State != contracts.Failed || failed.Outcome != nil {
		t.Fatal(failed)
	}
	var count int
	store.db.QueryRow("SELECT count(*) FROM results").Scan(&count)
	if count != 0 {
		t.Fatal("partial result committed")
	}
	store.db.QueryRow("SELECT count(*) FROM winners").Scan(&count)
	if count != 0 {
		t.Fatal("partial winners committed")
	}
	if _, err = store.db.Exec("DROP TRIGGER product_result_fault"); err != nil {
		t.Fatal(err)
	}
	result, err := service.RetryRound(context.Background(), rl.RetryRequest{OperationID: "retry-after-storage", Context: productRoundContext(failed, "session-one")})
	if err != nil || result.State != contracts.Completed || entropy.calls.Load() != 2 {
		t.Fatal(result, err)
	}
	assertNoHandles(t, store)
}
func TestProductPriorSessionRecoveryConsumesNoEntropyAndLocalRetryIsAccepted(t *testing.T) {
	store := migratedTestStore(t)
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	old := productService(t, store, new(productEntropy), func() time.Time { return now }, "session-one", nil)
	request := productRequest(now, rl.ImmediateMode)
	prepared, err := old.PrepareCreate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := old.AdmitCreate(context.Background(), prepared, request.Collection)
	if err != nil {
		t.Fatal(err)
	}
	entropy := new(productEntropy)
	fresh := productService(t, store, entropy, func() time.Time { return now }, "session-two", nil)
	report, err := fresh.Recover(context.Background(), rl.RecoverRequest{})
	if err != nil || len(report.Failed) != 1 || report.Failed[0] != admitted.Round.ID || entropy.calls.Load() != 0 {
		t.Fatal(report, err)
	}
	failed, err := store.ReadRound(context.Background(), admitted.Round.ID)
	if err != nil || failed.State != contracts.Failed {
		t.Fatal(failed, err)
	}

	result, err := fresh.RetryRound(context.Background(), rl.RetryRequest{OperationID: "recovery-retry", Context: productRoundContext(failed, "session-two")})
	if err != nil || result.State != contracts.Completed || entropy.calls.Load() != 1 {
		t.Fatal(result, err)
	}
	again, err := fresh.Recover(context.Background(), rl.RecoverRequest{})
	if err != nil || len(again.Failed) != 0 || entropy.calls.Load() != 1 {
		t.Fatal(again, err)
	}
	assertNoHandles(t, store)
}
func TestProductDuplicateAdmissionExecutorsConsumeEntropyOnce(t *testing.T) {
	store := migratedTestStore(t)
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	entropy := new(productEntropy)
	entropy.entered = make(chan struct{}, 1)
	entropy.release = make(chan struct{})
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
	request := productRequest(now, rl.ImmediateMode)
	prepared, err := service.PrepareCreate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := service.AdmitCreate(context.Background(), prepared, request.Collection)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := service.ExecuteAdmission(context.Background(), admitted); done <- err }()
	<-entropy.entered
	var group sync.WaitGroup
	group.Add(16)
	for index := 0; index < 16; index++ {
		go func() {
			defer group.Done()
			round, err := service.ExecuteAdmission(context.Background(), admitted)
			if err != nil || round.Outcome != nil || round.State != contracts.Executing {
				t.Errorf("duplicate claim %+v/%v", round, err)
			}
		}()
	}
	group.Wait()
	close(entropy.release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if entropy.calls.Load() != 1 {
		t.Fatal("duplicate entropy")
	}
	assertNoHandles(t, store)
}

func TestProductFailedOperationReplayReturnsSameFailureWithoutRetry(t *testing.T) {
	store := migratedTestStore(t)
	now := storageNow
	entropy := new(productEntropy)
	entropy.fail.Store(true)
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
	request := productRequest(now, rl.ImmediateMode)
	first, err := service.CreateCollection(context.Background(), request)
	requireFault(t, err, contracts.InvalidState)
	entropy.fail.Store(false)
	again, err := service.CreateCollection(context.Background(), request)
	requireFault(t, err, contracts.InvalidState)
	if again.ID != first.ID || again.Attempt != first.Attempt || again.State != contracts.Failed || again.Outcome != nil || entropy.calls.Load() != 1 {
		t.Fatal(again)
	}
	assertNoHandles(t, store)
}
func TestProductRecoverDoesNotRetireLiveAdmissionOrCurrentClaim(t *testing.T) {
	store := migratedTestStore(t)
	now := storageNow
	entropy := new(productEntropy)
	entropy.entered = make(chan struct{}, 1)
	entropy.release = make(chan struct{})
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
	request := productRequest(now, rl.ImmediateMode)
	prepared, err := service.PrepareCreate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := service.AdmitCreate(context.Background(), prepared, request.Collection)
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Recover(context.Background(), rl.RecoverRequest{})
	if err != nil || len(report.Failed) != 0 || entropy.calls.Load() != 0 {
		t.Fatal(report, err)
	}
	done := make(chan error, 1)
	go func() { _, err := service.ExecuteAdmission(context.Background(), admission); done <- err }()
	<-entropy.entered
	report, err = service.Recover(context.Background(), rl.RecoverRequest{})
	if err != nil || len(report.Failed) != 0 || entropy.calls.Load() != 1 {
		t.Fatal(report, err)
	}
	close(entropy.release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	assertNoHandles(t, store)
}
func TestProductAdmissionFirstNthWriteFailuresRollbackAllRows(t *testing.T) {
	for _, entry := range []struct{ name, table, condition string }{{"collection", "collections", "1"}, {"participant first", "participants", "NEW.position=0"}, {"participant third", "participants", "NEW.position=2"}, {"operation", "operations", "1"}, {"round", "rounds", "1"}, {"candidate second", "round_candidates", "NEW.position=1"}, {"prize", "round_prizes", "1"}, {"attempt", "round_attempts", "1"}} {
		t.Run(entry.name, func(t *testing.T) {
			store := migratedTestStore(t)
			now := storageNow

			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
			execSQL(t, store.db, fmt.Sprintf("CREATE TRIGGER product_admission_fault BEFORE INSERT ON %s WHEN %s BEGIN SELECT RAISE(ABORT,'injected admission write'); END", entry.table, entry.condition))
			request := productRequest(now, rl.ImmediateMode)
			result, err := service.CreateCollection(context.Background(), request)
			requireFault(t, err, contracts.StorageUnavailable)
			if result.ID != "" || result.Outcome != nil || entropy.calls.Load() != 0 {
				t.Fatal(result)
			}
			for _, table := range []string{"collections", "participants", "operations", "rounds", "round_candidates", "round_prizes", "round_attempts", "results", "winners"} {
				var count int
				if err = store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("partial admission %s=%d/%v", table, count, err)
				}
			}
			op, err := store.ReadOperation(context.Background(), request.OperationID)
			if err != nil || op.Status != contracts.OperationUnknown {
				t.Fatal(op, err)
			}
			assertNoHandles(t, store)
			execSQL(t, store.db, "DROP TRIGGER product_admission_fault")
			result, err = service.CreateCollection(context.Background(), request)
			if err != nil || result.State != contracts.Completed || entropy.calls.Load() != 1 {
				t.Fatal(result, err)
			}
			assertNoHandles(t, store)
		})
	}
}
func TestProductNthWinnerWriteFailureRollsBackPartialResults(t *testing.T) {
	store := migratedTestStore(t)
	now := storageNow
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
	request := productRequest(now, rl.ImmediateMode)
	request.Input.Prizes[0].Count = 2
	execSQL(t, store.db, "CREATE TRIGGER product_winner_fault BEFORE INSERT ON winners WHEN NEW.slot=2 BEGIN SELECT RAISE(ABORT,'second winner fault'); END")
	failed, err := service.CreateCollection(context.Background(), request)
	requireFault(t, err, contracts.StorageUnavailable)
	if failed.State != contracts.Failed || failed.Outcome != nil {
		t.Fatal(failed)
	}
	for _, table := range []string{"results", "winners"} {
		var count int
		store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count)
		if count != 0 {
			t.Fatal("partial commit", table, count)
		}
	}
	execSQL(t, store.db, "DROP TRIGGER product_winner_fault")
	result, err := service.RetryRound(context.Background(), rl.RetryRequest{OperationID: "retry-two-winners", Context: productRoundContext(failed, "session-one")})
	if err != nil || len(result.Outcome.Winners) != 2 || entropy.calls.Load() != 4 {
		t.Fatal(result, err)
	}
	assertNoHandles(t, store)
}
func TestProductFailureRecordFaultRetainsExecutingUntilFreshRecoveryWithoutEntropy(t *testing.T) {
	store := migratedTestStore(t)
	now := storageNow
	entropy := new(productEntropy)
	entropy.fail.Store(true)
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
	execSQL(t, store.db, "CREATE TRIGGER product_failure_fault BEFORE UPDATE ON operations WHEN NEW.status='failed' BEGIN SELECT RAISE(ABORT,'failure recording fault'); END")
	result, err := service.CreateCollection(context.Background(), productRequest(now, rl.ImmediateMode))
	requireFault(t, err, contracts.StorageUnavailable)
	if result.ID != "" || result.Outcome != nil {
		t.Fatal("uncommitted failure returned")
	}
	operation, err := store.ReadOperation(context.Background(), "create-op")
	if err != nil || operation.Status != contracts.OperationPending {
		t.Fatal(operation, err)
	}
	round, err := store.ReadRound(context.Background(), operation.RoundID)
	if err != nil || round.State != contracts.Executing || round.Revision != 2 || round.Version != 2 {
		t.Fatal(round, err)
	}
	execSQL(t, store.db, "DROP TRIGGER product_failure_fault")
	report, err := service.Recover(context.Background(), rl.RecoverRequest{})
	if err != nil || len(report.Failed) != 1 || entropy.calls.Load() != 1 {
		t.Fatal(report, err)
	}
	round, err = store.ReadRound(context.Background(), operation.RoundID)
	if err != nil || round.State != contracts.Failed {
		t.Fatal(round, err)
	}
	assertNoHandles(t, store)
}
func TestProductCloseCancelsBlockedExecutionWithoutFabricatingFailure(t *testing.T) {
	store := migratedTestStore(t)
	now := storageNow
	entropy := new(productEntropy)
	entropy.entered = make(chan struct{}, 1)
	entropy.release = make(chan struct{})
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
	done := make(chan error, 1)
	go func() {
		_, err := service.CreateCollection(context.Background(), productRequest(now, rl.ImmediateMode))
		done <- err
	}()
	<-entropy.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	closingCtx := &productDeadlineContext{Context: context.Background(), done: make(chan struct{}), observed: make(chan struct{}, 1)}
	closeDone := make(chan error, 1)
	go func() { closeDone <- service.Close(closingCtx) }()
	<-closingCtx.observed
	closingCtx.expired.Store(true)
	close(closingCtx.done)
	if err := <-closeDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	operation, err := store.ReadOperation(context.Background(), "create-op")
	if err != nil || operation.Status != contracts.OperationPending {
		t.Fatal(operation, err)
	}
	round, err := store.ReadRound(context.Background(), operation.RoundID)
	if err != nil || round.State != contracts.Executing || round.Outcome != nil {
		t.Fatal(round, err)
	}
	_, err = service.CreateCollection(context.Background(), productRequest(now, rl.ImmediateMode))
	requireFault(t, err, contracts.InvalidState)
	assertNoHandles(t, store)
}
func TestProductReopenedDatabaseRecoversUnfinishedClaimWithoutDuplicateWinners(t *testing.T) {
	store := migratedTestStore(t)
	now := storageNow
	old := productService(t, store, new(productEntropy), func() time.Time { return now }, "session-one", nil)
	request := productRequest(now, rl.ImmediateMode)
	prepared, err := old.PrepareCreate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := old.AdmitCreate(context.Background(), prepared, request.Collection)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimAttempt(context.Background(), rl.ClaimRequest{CollectionID: admission.Round.CollectionID, RoundID: admission.Round.ID, OperationID: admission.Operation.Operation.ID, ExpectedRevision: admission.Round.Revision, ExpectedVersion: admission.Round.Version, Attempt: 1, Session: "session-one"})
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	if err = old.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := store.path
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	entropy := new(productEntropy)
	fresh := productService(t, reopened, entropy, func() time.Time { return now }, "session-two", nil)
	report, err := fresh.Recover(context.Background(), rl.RecoverRequest{})
	if err != nil || len(report.Failed) != 1 || entropy.calls.Load() != 0 {
		t.Fatal(report, err)
	}
	failed, err := reopened.ReadRound(context.Background(), admission.Round.ID)
	if err != nil || failed.State != contracts.Failed {
		t.Fatal(failed, err)
	}

	result, err := fresh.RetryRound(context.Background(), rl.RetryRequest{OperationID: "restart-retry", Context: productRoundContext(failed, "session-two")})
	if err != nil || result.State != contracts.Completed || entropy.calls.Load() != 1 {
		t.Fatal(result, err)
	}
	assertNoHandles(t, reopened)
}

type productDeadlineContext struct {
	context.Context
	done     chan struct{}
	observed chan struct{}
	expired  atomic.Bool
}

func (ctx *productDeadlineContext) Done() <-chan struct{} {
	select {
	case ctx.observed <- struct{}{}:
	default:
	}
	return ctx.done
}
func (ctx *productDeadlineContext) Err() error {
	if ctx.expired.Load() {
		return context.DeadlineExceeded
	}
	return nil
}

func TestProductHistorySearchesGloballyWithUnicodeAndAtomicPageTotal(t *testing.T) {
	store := migratedTestStore(t)
	now := storageNow
	service := productService(t, store, new(productEntropy), func() time.Time { return now }, "session-one", nil)
	ids := make([]contracts.CollectionID, 0)
	for index, entry := range []struct{ title, gallery, mode string }{{"Alpha old", "Gallery A", rl.ImmediateMode}, {"Beta 100%", "ÖGG Ü", rl.ImmediateMode}, {"ALPHA recent", "Gallery C", rl.ReservationMode}} {
		request := productRequest(now, entry.mode)
		request.OperationID = contracts.OperationID(fmt.Sprintf("history-op-%d", index))
		request.Context.DraftID = contracts.DraftID(fmt.Sprintf("history-draft-%d", index))
		request.Collection.SourceDraftID = request.Context.DraftID
		request.Collection.Article.Title = entry.title
		request.Collection.Article.GalleryName = entry.gallery
		result, err := service.CreateCollection(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, result.CollectionID)
		now = now.Add(time.Second)
	}
	first, total, err := store.ListCollectionPage(context.Background(), 0, 1, "aLpHa", "all")
	if err != nil || total != 2 || len(first) != 1 || first[0].ID != ids[2] || first[0].LatestRound.State != contracts.PendingSchedule {
		t.Fatal(first, total, err)
	}
	second, total, err := store.ListCollectionPage(context.Background(), 1, 1, "ALPHA", "")
	if err != nil || total != 2 || len(second) != 1 || second[0].ID != ids[0] {
		t.Fatal(second, total, err)
	}
	gallery, total, err := store.ListCollectionPage(context.Background(), 0, 100, "ögg ü", "completed")
	if err != nil || total != 1 || len(gallery) != 1 || gallery[0].ID != ids[1] || gallery[0].GalleryName != "ÖGG Ü" {
		t.Fatal(gallery, total, err)
	}
	literal, total, err := store.ListCollectionPage(context.Background(), 0, 100, "%", "all")
	if err != nil || total != 1 || literal[0].ID != ids[1] {
		t.Fatal(literal, total, err)
	}
	for _, state := range []string{"all", "pending_schedule", "scheduled", "executing", "completed", "cancelled", "failed"} {
		page, total, err := store.ListCollectionPage(context.Background(), 0, 100, "", state)
		if err != nil {
			t.Fatal(state, err)
		}
		want := uint32(0)
		switch state {
		case "all":
			want = 3
		case "pending_schedule":
			want = 1
		case "completed":
			want = 2
		}
		if total != want || len(page) != int(want) {
			t.Fatal(state, page, total)
		}
	}
	empty, total, err := store.ListCollectionPage(context.Background(), ^uint32(0), 100, "", "all")
	if err != nil || total != 3 || len(empty) != 0 {
		t.Fatal(empty, total, err)
	}
	for _, entry := range []struct {
		limit        uint32
		query, state string
	}{{0, "", "all"}, {101, "", "all"}, {1, "", "invalid"}, {1, string([]byte{255}), "all"}, {1, string(make([]byte, 4097)), "all"}} {
		_, _, err := store.ListCollectionPage(context.Background(), 0, entry.limit, entry.query, entry.state)
		requireFault(t, err, contracts.InvalidInput)
	}
	if first[0].LatestRound.Input.CandidateIDs != nil || first[0].LatestRound.Outcome != nil || first[0].LatestRound.Claim != nil {
		t.Fatal("history retained detailed private collection values")
	}
	second[0].LatestRound.Input.Prizes[0].Name = "mutation"
	actual, err := store.ReadRound(context.Background(), second[0].LatestRound.ID)
	if err != nil || actual.Input.Prizes[0].Name == "mutation" || actual.Outcome == nil {
		t.Fatal(actual, err)
	}
	assertNoHandles(t, store)
}
func TestProductQuickScheduleUsesOneAcceptedClockAndReplaysOriginalTime(t *testing.T) {
	store := migratedTestStore(t)
	now := storageNow
	clockCalls := 0
	service := productService(t, store, new(productEntropy), func() time.Time { clockCalls++; return now }, "session-one", nil)
	pending, err := service.CreateCollection(context.Background(), productRequest(now, rl.ReservationMode))
	if err != nil {
		t.Fatal(err)
	}
	before := clockCalls
	delay := uint32(10)
	request := rl.SetScheduleRequest{OperationID: "quick-ten", Context: productRoundContext(pending, "session-one"), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"}
	result, err := service.SetSchedule(context.Background(), request)
	if err != nil || clockCalls-before != 1 || result.ScheduledAt == nil || !result.ScheduledAt.Equal(now.Add(10*time.Second)) {
		t.Fatal(result, err, clockCalls-before)
	}
	original := *result.ScheduledAt
	now = now.Add(24 * time.Hour)
	before = clockCalls
	replay, err := service.SetSchedule(context.Background(), request)
	if err != nil || replay.ScheduledAt == nil || !replay.ScheduledAt.Equal(original) || clockCalls != before {
		t.Fatal(replay, err, "replay called clock")
	}
	delay = 30
	if _, err = service.SetSchedule(context.Background(), request); !errors.Is(err, rl.ErrOperationConflict) {
		t.Fatal("quick payload conflict", err)
	}
	assertNoHandles(t, store)
}
func TestProductCancelledReservationAllowsNewRoundWithoutConsumingParticipants(t *testing.T) {
	store := migratedTestStore(t)
	now := storageNow
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
	pending, err := service.CreateCollection(context.Background(), productRequest(now, rl.ReservationMode))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := service.CancelSchedule(context.Background(), rl.CancelRequest{OperationID: "cancel-pending", Context: productRoundContext(pending, "session-one")})
	if err != nil || cancelled.State != contracts.Cancelled || cancelled.Outcome != nil || entropy.calls.Load() != 0 {
		t.Fatal(cancelled, err)
	}
	request := rl.RerunRequest{OperationID: "new-after-cancel", Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: cancelled.CollectionID, Revision: cancelled.Revision}, Prizes: []rl.Prize{{ID: "new-prize", Count: 1}}, Mode: rl.ReservationMode}
	next, err := service.Rerun(context.Background(), request)
	if err != nil || next.State != contracts.PendingSchedule || len(next.Input.CandidateIDs) != 3 || next.Number != 2 || entropy.calls.Load() != 0 {
		t.Fatal(next, err)
	}
	delay := uint32(30)
	scheduled, err := service.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: "schedule-cancellable", Context: productRoundContext(next, "session-one"), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err = service.CancelSchedule(context.Background(), rl.CancelRequest{OperationID: "cancel-before-boundary", Context: productRoundContext(scheduled, "session-one")})
	if err != nil || cancelled.State != contracts.Cancelled || entropy.calls.Load() != 0 {
		t.Fatal(cancelled, err)
	}
	assertNoHandles(t, store)
}
func TestProductUIRequestCancellationAfterAdmissionDoesNotCancelExecution(t *testing.T) {
	store := migratedTestStore(t)
	now := storageNow
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
	request := productRequest(now, rl.ImmediateMode)
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := service.PrepareCreate(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := service.AdmitCreate(ctx, prepared, request.Collection)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	result, err := service.ExecuteAdmission(ctx, admission)
	if err != nil || result.State != contracts.Completed || entropy.calls.Load() != 1 {
		t.Fatal(result, err)
	}
	assertNoHandles(t, store)
}
