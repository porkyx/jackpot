package application

// Core boundary regressions use real SQLite and controlled dependency barriers.
import (
	"context"
	"encoding/json"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func TestFinalizeReadyDraftModeMismatchRejectsBeforePreparationAndCorrectedSameOperationSucceeds(t *testing.T) {
	for _, configured := range []string{rl.ImmediateMode, rl.ReservationMode} {
		t.Run(configured, func(t *testing.T) {
			draft := completedDraft(t)
			if configured != rl.ImmediateMode {
				prizes := getDraft(t, draft).Prizes
				prizes.DrawMode = configured
				if _, err := draft.Edit(context.Background(), contracts.DraftEditRequest{DraftMutationHeader: draftHeader(draft, "configure-mode"), Kind: "SetPrizes", Prizes: &prizes}); err != nil {
					t.Fatal(err)
				}
			}
			before := getDraft(t, draft)
			store, db := finalizeStore(t)
			preparation, entropy := new(finalizePreparation), new(finalizeEntropy)
			lifecycle, effects := newFinalizeFenceLifecycle(t, store, preparation, entropy)
			var notices atomic.Int32
			draft.options.Publish = func(contracts.StateNotice) error { notices.Add(1); return nil }
			request := finalizeRequest(draft, "mismatched-valid-mode")
			request.Mode = rl.ReservationMode
			if configured == rl.ReservationMode {
				request.Mode = rl.ImmediateMode
			}
			if err := request.Validate(); err != nil {
				t.Fatal("fixture must isolate owner-mode mismatch", err)
			}
			requestRaw, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 3; attempt++ {
				round, err := draft.CreateCollection(context.Background(), request, lifecycle)
				finalizeFault(t, err, contracts.InvalidInput)
				if !reflect.DeepEqual(round, rl.RoundRecord{}) || !reflect.DeepEqual(before, getDraft(t, draft)) {
					t.Fatal("mode mismatch changed draft or exposed a round")
				}
				assertFinalizeFenceStorageEmpty(t, store, db, request.OperationID)
				operation, err := draft.Operation(context.Background(), request.OperationID)
				if err != nil || operation.State != contracts.OperationUnknown {
					t.Fatal("rejected mode consumed ephemeral operation", operation, err)
				}
				afterRaw, err := json.Marshal(request)
				if err != nil || string(requestRaw) != string(afterRaw) {
					t.Fatal("rejection changed caller input", err)
				}
				if preparation.calls.Load() != 0 || entropy.calls.Load() != 0 || effects.ids.Load() != 0 || effects.notices.Load() != 0 || notices.Load() != 0 {
					t.Fatal("mismatch reached preparation, IDs, RNG or publication")
				}
			}
			request.Mode = configured
			result, err := draft.CreateCollection(context.Background(), request, lifecycle)
			if err != nil {
				t.Fatal("corrected same operation was consumed", err)
			}
			expectedState, expectedRNG := contracts.PendingSchedule, int32(0)
			if configured == rl.ImmediateMode {
				expectedState, expectedRNG = contracts.Completed, 1
			}
			if result.State != expectedState || result.Input.Mode != configured || preparation.calls.Load() != 1 || entropy.calls.Load() != expectedRNG {
				t.Fatal("corrected mode did not admit exactly once", result)
			}
			sealed := getDraft(t, draft)
			if sealed.Summary.State != contracts.DraftFinalized || sealed.Summary.Revision != before.Summary.Revision+1 || sealed.Summary.CollectionID == nil || *sealed.Summary.CollectionID != result.CollectionID {
				t.Fatal("successful mode did not seal exactly once")
			}
			persisted, err := store.ReadRound(context.Background(), result.ID)
			if err != nil || !reflect.DeepEqual(persisted, result) {
				t.Fatal("public result differs from durable round", err)
			}
			if db.Stats().InUse != 0 {
				t.Fatal("observer handle retained")
			}
		})
	}
}

func TestLoadTerminalCounterOverflowKeepsPriorConfiguredSnapshotAndReleasesWorkerLease(t *testing.T) {
	for _, boundary := range []string{"revision", "article-generation"} {
		t.Run(boundary, func(t *testing.T) {
			draft, _ := configuredDraftSnapshot(t)
			draft.mu.Lock()
			if boundary == "revision" {
				draft.draft.Summary.Revision = contracts.Revision(contracts.MaxSafeInteger - 1)
			} else {
				draft.draft.Summary.ArticleGeneration = contracts.ArticleGeneration(contracts.MaxSafeInteger)
			}
			ownerBefore, err := json.Marshal(struct {
				Participants any
				Author       any
			}{draft.participants, draft.author})
			draft.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			old := getDraft(t, draft)
			var calls, notices atomic.Int32
			draft.options.Publish = func(contracts.StateNotice) error { notices.Add(1); return nil }
			draft.options.Collector = testCollector(func(ctx context.Context, _ string, progress func(CollectionProgress)) (CollectionSnapshot, error) {
				calls.Add(1)
				progress(CollectionProgress{Pages: 1, Comments: 2})
				next := snapshotFixture()
				next.Article.Title = "replacement must not promote"
				next.Comments[0].Text = "different next snapshot"
				return next, ctx.Err()
			})
			request := contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(draft, "terminal-counter-overflow"), URL: "example"}
			loading, err := draft.Load(context.Background(), request)
			if err != nil || loading.Load.State != "loading" || loading.Summary.Revision != old.Summary.Revision+1 {
				t.Fatal("legal admission boundary rejected", loading, err)
			}
			draft.workers.Wait()
			terminal := getDraft(t, draft)
			if terminal.Load.State != "failed" || terminal.Load.FailureCode == nil || *terminal.Load.FailureCode != contracts.InvalidInput || terminal.Load.Sequence != 2 || terminal.Summary.State != contracts.DraftReady {
				t.Fatal("overflow promoted success or left pending operation", terminal.Load)
			}
			expectedRevision := old.Summary.Revision + 2
			if boundary == "revision" {
				expectedRevision = contracts.Revision(contracts.MaxSafeInteger)
			}
			if terminal.Summary.Revision != expectedRevision || terminal.Summary.ArticleGeneration != old.Summary.ArticleGeneration {
				t.Fatal("terminal counter wrapped or incremented generation")
			}
			assertPreviousCollectionPreserved(t, draft, old)
			draft.mu.Lock()
			ownerAfter, marshalErr := json.Marshal(struct {
				Participants any
				Author       any
			}{draft.participants, draft.author})
			leaseReleased := draft.cancel == nil
			draft.mu.Unlock()
			if marshalErr != nil || string(ownerBefore) != string(ownerAfter) || !leaseReleased {
				t.Fatal("terminal failure replaced participant/author owner or retained cancel lease", marshalErr)
			}
			operation, err := draft.Operation(context.Background(), request.OperationID)
			if err != nil || operation.State != contracts.OperationFailed || operation.FailureCode == nil || *operation.FailureCode != contracts.InvalidInput {
				t.Fatal("terminal operation not failed", operation, err)
			}
			replay, err := draft.Load(context.Background(), request)
			if err != nil || !reflect.DeepEqual(replay, terminal) || calls.Load() != 1 || notices.Load() != 2 {
				t.Fatal("terminal replay restarted work or changed state", err)
			}
			operationAfter, err := draft.Operation(context.Background(), request.OperationID)
			if err != nil || !reflect.DeepEqual(operation, operationAfter) {
				t.Fatal("replay changed terminal receipt", err)
			}
			// Legal test-only counter seeds avoid trillions of edits. Restore the
			// legal predecessor to prove the cancelled/worker lease is reusable.
			draft.mu.Lock()
			draft.draft.Summary.Revision = 20
			draft.draft.Summary.ArticleGeneration = 10
			draft.mu.Unlock()
			recovered, err := draft.Load(context.Background(), contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(draft, "counter-corrected-load"), URL: "example"})
			if err != nil || recovered.Load.State != "loading" {
				t.Fatal("terminal failure retained collection gate", recovered, err)
			}
			draft.workers.Wait()
			complete := getDraft(t, draft)
			if complete.Load.State != "completed" || complete.Summary.Revision != 22 || complete.Summary.ArticleGeneration != 11 || calls.Load() != 2 {
				t.Fatal("corrected load did not complete exactly once", complete)
			}
			if err := draft.Close(); err != nil {
				t.Fatal(err)
			}
			if err := draft.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
