package application

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	"github.com/porkyx/jackpot/internal/storage/sqlite"
)

type finalizeFenceEffects struct{ ids, notices atomic.Int32 }

func newFinalizeFenceLifecycle(t *testing.T, store *sqlite.Store, preparation *finalizePreparation, entropy *finalizeEntropy) (*rl.Service, *finalizeFenceEffects) {
	t.Helper()
	preparation.Store = store
	effects := new(finalizeFenceEffects)
	service, err := rl.NewService(rl.ServiceOptions{Storage: preparation, Session: "session", Entropy: entropy, Clock: func() time.Time { return draftNow }, NewID: func() string { return fmt.Sprintf("fence-%d", effects.ids.Add(1)) }, Publish: func(context.Context, contracts.StateNotice) error { effects.notices.Add(1); return nil }})
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
	return service, effects
}
func assertFinalizeFenceStorageEmpty(t *testing.T, store *sqlite.Store, db *sql.DB, operation contracts.OperationID) {
	t.Helper()
	for _, table := range []string{"collections", "participants", "rounds", "round_candidates", "round_prizes", "round_attempts", "results", "winners", "operations"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained%d rows", table, count)
		}
	}
	observed, err := store.ReadOperation(context.Background(), operation)
	if err != nil || observed.Status != contracts.OperationUnknown {
		t.Fatalf("operation was consumed: status=%s err=%v", observed.Status, err)
	}
	if db.Stats().InUse != 0 {
		t.Fatal("observer connection retained")
	}
}

func TestFinalizeClosedDuringPreparationRejectsAdmissionAndReturnsPreparedLeaseWithoutPersistence(t *testing.T) {
	draft := completedDraft(t)
	before := getDraft(t, draft)
	store, db := finalizeStore(t)
	entered, release := make(chan struct{}), make(chan struct{})
	preparation := &finalizePreparation{entered: entered, release: release}
	defer preparation.unblock()
	entropy := new(finalizeEntropy)
	lifecycle, effects := newFinalizeFenceLifecycle(t, store, preparation, entropy)
	var draftNotices atomic.Int32
	draft.options.Publish = func(contracts.StateNotice) error { draftNotices.Add(1); return nil }
	request := finalizeRequest(draft, "close-during-preparation")
	draft.mu.Lock()
	frozen, input, err := draft.freeze(request)
	draft.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	type answer struct {
		round rl.RoundRecord
		err   error
	}
	done := make(chan answer, 1)
	go func() {
		round, err := draft.CreateCollection(context.Background(), request, lifecycle)
		done <- answer{round, err}
	}()
	finalizeAwait(t, entered)
	if err := draft.Close(); err != nil {
		t.Fatal(err)
	}
	preparation.unblock()
	result := finalizeAwait(t, done)
	finalizeFault(t, result.err, contracts.InvalidState)
	if !reflect.DeepEqual(result.round, rl.RoundRecord{}) {
		t.Fatal("closed owner exposed a round")
	}
	draft.mu.Lock()
	after := cloneDraft(draft.draft)
	closed := draft.closed
	draft.mu.Unlock()
	if !closed || !reflect.DeepEqual(before, after) {
		t.Fatal("close/finalize changed snapshot, revision or sealing")
	}
	assertFinalizeFenceStorageEmpty(t, store, db, request.OperationID)
	if preparation.calls.Load() != 1 || entropy.calls.Load() != 0 || effects.ids.Load() != 0 || effects.notices.Load() != 0 || draftNotices.Load() != 0 {
		t.Fatal("rejected admission had effects")
	}
	// Draft.Close does not close the app lifecycle; the rejected caller must
	// release its preparation lease even though no later draft command is legal.
	preparation.entered = nil
	preparation.release = nil
	next, err := lifecycle.PrepareCreate(context.Background(), rl.CreateCollectionRequest{OperationID: "probe-returned-lease", Context: before.Summary.DraftContext, Collection: frozen, Input: input})
	if err != nil {
		t.Fatal("refused application admission retained lifecycle lease", err)
	}
	next.Close()
	next.Close()
	if preparation.calls.Load() != 2 {
		t.Fatal("successor preparation did not execute once")
	}
	assertFinalizeFenceStorageEmpty(t, store, db, request.OperationID)
	if err := draft.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFinalizeMaximumRevisionRejectsBeforeDurableAdmissionAndMaximumMinusOneCompletes(t *testing.T) {
	draft := completedDraft(t)
	// Seed a legal counter boundary; MaxSafeInteger is accepted by ValidateCounter
	// and Context.Validate. This does not claim2^53 real edits were executed.
	draft.mu.Lock()
	draft.draft.Summary.Revision = contracts.Revision(contracts.MaxSafeInteger)
	draft.mu.Unlock()
	before := getDraft(t, draft)
	store, db := finalizeStore(t)
	preparation := new(finalizePreparation)
	entropy := new(finalizeEntropy)
	lifecycle, effects := newFinalizeFenceLifecycle(t, store, preparation, entropy)
	var draftNotices atomic.Int32
	draft.options.Publish = func(contracts.StateNotice) error { draftNotices.Add(1); return nil }
	request := finalizeRequest(draft, "maximum-revision")
	if err := request.Validate(); err != nil {
		t.Fatal("maximum legal request was invalid", err)
	}
	round, err := draft.CreateCollection(context.Background(), request, lifecycle)
	finalizeFault(t, err, contracts.InvalidInput)
	if !reflect.DeepEqual(round, rl.RoundRecord{}) || !reflect.DeepEqual(before, getDraft(t, draft)) {
		t.Fatal("overflow sealed or mutated draft")
	}
	assertFinalizeFenceStorageEmpty(t, store, db, request.OperationID)
	if preparation.calls.Load() != 1 || entropy.calls.Load() != 0 || effects.ids.Load() != 0 || effects.notices.Load() != 0 || draftNotices.Load() != 0 {
		t.Fatal("overflow performed durable or execution effects")
	}
	draft.mu.Lock()
	draft.draft.Summary.Revision = contracts.Revision(contracts.MaxSafeInteger - 1)
	draft.mu.Unlock()
	retry := finalizeRequest(draft, string(request.OperationID))
	completed, err := draft.CreateCollection(context.Background(), retry, lifecycle)
	if err != nil || completed.State != contracts.Completed || completed.Outcome == nil || len(completed.Outcome.Winners) != 1 {
		t.Fatalf("maximum-minus-one result state=%s err=%v", completed.State, err)
	}
	sealed := getDraft(t, draft)
	if sealed.Summary.State != contracts.DraftFinalized || uint64(sealed.Summary.Revision) != contracts.MaxSafeInteger || sealed.Summary.CollectionID == nil || *sealed.Summary.CollectionID != completed.CollectionID {
		t.Fatal("valid maximum revision not sealed exactly once")
	}
	persisted, err := store.ReadRound(context.Background(), completed.ID)
	if err != nil || !reflect.DeepEqual(persisted, completed) {
		t.Fatal("public result differs from committed SQLite result", err)
	}
	operation, err := store.ReadOperation(context.Background(), request.OperationID)
	if err != nil || operation.Status != contracts.OperationSucceeded {
		t.Fatal("same operation ID could not succeed after corrected boundary", err)
	}
	if preparation.calls.Load() != 2 || entropy.calls.Load() != 1 || effects.ids.Load() != 2 || effects.notices.Load() != 2 || draftNotices.Load() != 1 {
		t.Fatal("successful boundary produced duplicate or missing effects")
	}
	if db.Stats().InUse != 0 {
		t.Fatal("completed observer leaked a connection")
	}
}
