package application

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	"github.com/porkyx/jackpot/internal/storage/sqlite"
)

// The embedded Store preserves LifecycleReader as well as the real SQLite ports.
// Only the first preparation read is paused; subsequent replays use the same DB.
type finalizePreparation struct {
	*sqlite.Store
	entered     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
	calls       atomic.Int32
	readFailure func(int32) error
}

var _ rl.Storage = (*finalizePreparation)(nil)
var _ rl.LifecycleReader = (*finalizePreparation)(nil)

func (preparation *finalizePreparation) ReadOperation(ctx context.Context, id contracts.OperationID) (rl.OperationRecord, error) {
	call := preparation.calls.Add(1)
	if preparation.entered != nil && call == 1 {
		close(preparation.entered)
		select {
		case <-preparation.release:
		case <-ctx.Done():
			return rl.OperationRecord{}, ctx.Err()
		}
	}
	if preparation.readFailure != nil {
		if err := preparation.readFailure(call); err != nil {
			return rl.OperationRecord{}, err
		}
	}
	return preparation.Store.ReadOperation(ctx, id)
}
func (preparation *finalizePreparation) unblock() {
	if preparation.release != nil {
		preparation.releaseOnce.Do(func() { close(preparation.release) })
	}
}
func finalizeAwait[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case value := <-channel:
		return value
	case <-timer.C:
		t.Fatal("controlled finalization barrier did not complete within five seconds")
		var zero T
		return zero
	}
}

type finalizeEntropy struct {
	calls       atomic.Int32
	fail        atomic.Bool
	entered     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

func (entropy *finalizeEntropy) Intn(ctx context.Context, bound uint64) (uint64, error) {
	call := entropy.calls.Add(1)
	if entropy.entered != nil && call == 1 {
		close(entropy.entered)
		select {
		case <-entropy.release:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	if entropy.fail.Load() {
		return 0, errors.New("entropy fault")
	}
	return 0, nil
}
func (entropy *finalizeEntropy) unblock() {
	if entropy.release != nil {
		entropy.releaseOnce.Do(func() { close(entropy.release) })
	}
}
func finalizeStore(t *testing.T) (*sqlite.Store, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "finalize.sqlite3")
	store, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return store, db
}
func finalizeLifecycle(t *testing.T, store *sqlite.Store, preparation *finalizePreparation, entropy *finalizeEntropy) *rl.Service {
	t.Helper()
	preparation.Store = store
	var ids atomic.Uint32
	service, err := rl.NewService(rl.ServiceOptions{Storage: preparation, Session: "session", Entropy: entropy, Clock: func() time.Time { return draftNow }, NewID: func() string { return fmt.Sprintf("finalized-%d", ids.Add(1)) }})
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
func finalizeRequest(service *DraftService, operation string) contracts.CreateCollectionRequest {
	return contracts.CreateCollectionRequest{DraftMutationHeader: draftHeader(service, operation), Mode: rl.ImmediateMode}
}
func finalizeFault(t *testing.T, err error, code contracts.ErrorCode) {
	t.Helper()
	var fault contracts.Fault
	if !errors.As(err, &fault) || fault.Code != code {
		t.Fatal(code, err)
	}
}
func TestFinalizeRealSQLiteRejectsDraftEditDuringPreparationWithoutPersistence(t *testing.T) {
	draft := completedDraft(t)
	before := getDraft(t, draft)
	store, _ := finalizeStore(t)
	preparation := &finalizePreparation{entered: make(chan struct{}), release: make(chan struct{})}
	defer preparation.unblock()
	entropy := new(finalizeEntropy)
	lifecycle := finalizeLifecycle(t, store, preparation, entropy)
	request := finalizeRequest(draft, "finalize-stale")
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, err := draft.CreateCollection(ctx, request, lifecycle); done <- err }()
	finalizeAwait(t, preparation.entered)
	filters := before.Filters
	filters.ExcludeAnonymous = true
	if _, err := draft.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(draft, "edit-during-preparation"), Kind: "UpdateFilters", Filters: &filters}); err != nil {
		t.Fatal(err)
	}
	preparation.unblock()
	finalizeFault(t, finalizeAwait(t, done), contracts.StaleRevision)
	count, err := store.CollectionCount(context.Background())
	if err != nil || count != 0 || entropy.calls.Load() != 0 || preparation.calls.Load() != 1 {
		t.Fatal(count, err)
	}
	operation, err := store.ReadOperation(context.Background(), request.OperationID)
	if err != nil || operation.Status != contracts.OperationUnknown {
		t.Fatal(operation, err)
	}
	after := getDraft(t, draft)
	if after.Summary.State != contracts.DraftReady || after.Summary.CollectionID != nil || after.Summary.Revision != before.Summary.Revision+1 || !after.Filters.ExcludeAnonymous {
		t.Fatal(after)
	}
}
func TestFinalizeRealSQLiteSealsDraftBeforeEntropyAndRejectsAllEditing(t *testing.T) {
	draft := completedDraft(t)
	before := getDraft(t, draft)
	store, _ := finalizeStore(t)
	entropy := &finalizeEntropy{entered: make(chan struct{}), release: make(chan struct{})}
	defer entropy.unblock()
	lifecycle := finalizeLifecycle(t, store, new(finalizePreparation), entropy)
	request := finalizeRequest(draft, "finalize-seal")
	draft.mu.Lock()
	frozen, input, freezeErr := draft.freeze(request)
	draft.mu.Unlock()
	if freezeErr != nil {
		t.Fatal(freezeErr)
	}
	result := make(chan rl.RoundRecord, 1)
	done := make(chan error, 1)
	go func() {
		round, err := draft.CreateCollection(context.Background(), request, lifecycle)
		result <- round
		done <- err
	}()
	finalizeAwait(t, entropy.entered)
	sealed := getDraft(t, draft)
	if sealed.Summary.State != contracts.DraftFinalized || sealed.Summary.CollectionID == nil || sealed.Summary.Revision != before.Summary.Revision+1 {
		t.Fatal(sealed)
	}
	for _, kind := range []string{"ResetArticle", "ResetFilters", "ResetManual", "ToggleParticipant"} {
		_, err := draft.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(draft, "deny-"+kind), Kind: kind})
		finalizeFault(t, err, contracts.InvalidState)
	}
	_, err := draft.Load(context.Background(), contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(draft, "deny-load"), URL: "other"})
	finalizeFault(t, err, contracts.InvalidState)
	entropy.unblock()
	if err = finalizeAwait(t, done); err != nil {
		t.Fatal(err)
	}
	round := finalizeAwait(t, result)
	if round.State != contracts.Completed || round.Outcome == nil {
		t.Fatal(round)
	}
	replay, err := draft.CreateCollection(context.Background(), request, lifecycle)
	if err != nil || !reflect.DeepEqual(round, replay) || entropy.calls.Load() != 1 {
		t.Fatal(replay, err)
	}
	// The draft's public snapshot is immutable after admission. Exercise the
	// lifecycle's public-payload guard directly with the captured original input.
	input.Message = "changed message"
	_, err = lifecycle.CreateCollection(context.Background(), rl.CreateCollectionRequest{
		OperationID: request.OperationID, Context: before.Summary.DraftContext,
		Collection: frozen, Input: input,
	})
	if !errors.Is(err, rl.ErrOperationConflict) {
		t.Fatal(err)
	}
	stored, readErr := store.ReadRound(context.Background(), round.ID)
	count, countErr := store.CollectionCount(context.Background())
	if readErr != nil || countErr != nil || count != 1 || !reflect.DeepEqual(stored, round) || entropy.calls.Load() != 1 || !reflect.DeepEqual(sealed, getDraft(t, draft)) {
		t.Fatal("changed public payload changed sealed draft or durable result", readErr, countErr)
	}
}
func TestFinalizeRealSQLiteExecutionFailureKeepsCollectionAndDraftSealed(t *testing.T) {
	draft := completedDraft(t)
	store, _ := finalizeStore(t)
	entropy := new(finalizeEntropy)
	entropy.fail.Store(true)
	lifecycle := finalizeLifecycle(t, store, new(finalizePreparation), entropy)
	request := finalizeRequest(draft, "finalize-failure")
	failed, err := draft.CreateCollection(context.Background(), request, lifecycle)
	finalizeFault(t, err, contracts.InvalidState)
	if failed.State != contracts.Failed || failed.Outcome != nil || failed.CollectionID == "" {
		t.Fatal(failed)
	}
	sealed := getDraft(t, draft)
	if sealed.Summary.State != contracts.DraftFinalized || sealed.Summary.CollectionID == nil || *sealed.Summary.CollectionID != failed.CollectionID {
		t.Fatal(sealed)
	}
	stored, err := store.ReadRound(context.Background(), failed.ID)
	if err != nil || stored.State != contracts.Failed {
		t.Fatal(stored, err)
	}
	entropy.fail.Store(false)
	_, err = draft.CreateCollection(context.Background(), request, lifecycle)
	finalizeFault(t, err, contracts.InvalidState)
	if entropy.calls.Load() != 1 {
		t.Fatal("failed operation replay executed entropy")
	}
}
func TestFinalizeRealSQLiteAdmissionRollbackPreservesEditableDraftAndCanRetrySameID(t *testing.T) {
	draft := completedDraft(t)
	before := getDraft(t, draft)
	store, db := finalizeStore(t)
	entropy := new(finalizeEntropy)
	lifecycle := finalizeLifecycle(t, store, new(finalizePreparation), entropy)
	request := finalizeRequest(draft, "finalize-rollback")
	if _, err := db.Exec("CREATE TRIGGER finalize_admission_fault BEFORE INSERT ON round_candidates BEGIN SELECT RAISE(ABORT,'admission fault'); END"); err != nil {
		t.Fatal(err)
	}
	_, err := draft.CreateCollection(context.Background(), request, lifecycle)
	finalizeFault(t, err, contracts.StorageUnavailable)
	after := getDraft(t, draft)
	if !reflect.DeepEqual(before, after) || entropy.calls.Load() != 0 {
		t.Fatal("rollback changed draft", after)
	}
	count, err := store.CollectionCount(context.Background())
	if err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if _, err = db.Exec("DROP TRIGGER finalize_admission_fault"); err != nil {
		t.Fatal(err)
	}
	result, err := draft.CreateCollection(context.Background(), request, lifecycle)
	if err != nil || result.State != contracts.Completed || entropy.calls.Load() != 1 {
		t.Fatal(result, err)
	}
}

func TestFinalizePreparationReadFirstNthAndContinuousFailuresPreserveDraftAndDurableReplay(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		failCalls   map[int32]bool
		lastFailure int32
		fault       error
	}{
		{name: "first", failCalls: map[int32]bool{1: true}, lastFailure: 1, fault: errors.New("read fault")},
		{name: "nth-replay", failCalls: map[int32]bool{2: true}, lastFailure: 2, fault: errors.New("read fault")},
		{name: "continuous", failCalls: map[int32]bool{1: true, 2: true, 3: true}, lastFailure: 3, fault: errors.New("read fault")},
		{name: "deadline", failCalls: map[int32]bool{1: true}, lastFailure: 1, fault: context.DeadlineExceeded},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			draft := completedDraft(t)
			original := getDraft(t, draft)
			store, db := finalizeStore(t)
			preparation := &finalizePreparation{readFailure: func(call int32) error {
				if scenario.failCalls[call] {
					return scenario.fault
				}
				return nil
			}}
			entropy := new(finalizeEntropy)
			lifecycle := finalizeLifecycle(t, store, preparation, entropy)
			request := finalizeRequest(draft, "read-failure-"+scenario.name)
			var accepted *rl.RoundRecord
			for call := int32(1); call <= scenario.lastFailure+1; call++ {
				before := getDraft(t, draft)
				round, err := draft.CreateCollection(context.Background(), request, lifecycle)
				if scenario.failCalls[call] {
					if errors.Is(scenario.fault, context.DeadlineExceeded) {
						if !errors.Is(err, context.DeadlineExceeded) {
							t.Fatal("deadline identity lost", err)
						}
					} else {
						finalizeFault(t, err, contracts.StorageUnavailable)
					}
					if !reflect.DeepEqual(round, rl.RoundRecord{}) || !reflect.DeepEqual(before, getDraft(t, draft)) {
						t.Fatal("failed preparation changed owner or exposed a round")
					}
					if accepted == nil {
						assertFinalizeFenceStorageEmpty(t, store, db, request.OperationID)
						if !reflect.DeepEqual(original, getDraft(t, draft)) || entropy.calls.Load() != 0 {
							t.Fatal("failed preparation sealed or executed draft")
						}
					} else {
						stored, readErr := store.ReadRound(context.Background(), accepted.ID)
						count, countErr := store.CollectionCount(context.Background())
						operation, operationErr := store.ReadOperation(context.Background(), request.OperationID)
						if readErr != nil || countErr != nil || operationErr != nil || count != 1 || operation.Status != contracts.OperationSucceeded || !reflect.DeepEqual(stored, *accepted) || entropy.calls.Load() != 1 {
							t.Fatal("failed replay changed durable result", readErr, countErr, operationErr)
						}
					}
					continue
				}
				if err != nil || round.State != contracts.Completed {
					t.Fatal("same operation could not complete", round, err)
				}
				if accepted != nil && !reflect.DeepEqual(round, *accepted) {
					t.Fatal("replay changed accepted round")
				}
				accepted = &round
			}
			replay, err := draft.CreateCollection(context.Background(), request, lifecycle)
			if err != nil || accepted == nil || !reflect.DeepEqual(replay, *accepted) || entropy.calls.Load() != 1 || preparation.calls.Load() != scenario.lastFailure+2 || db.Stats().InUse != 0 {
				t.Fatal("recovery duplicated execution or retained resources", err)
			}
		})
	}
}
