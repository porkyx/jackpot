package application

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	"github.com/porkyx/jackpot/internal/selection"
)

func manualSnapshotDraft(t *testing.T, group selection.Group, manual selection.ManualState) *DraftService {
	t.Helper()
	snapshot := snapshotFixture()
	snapshot.Comments = []selection.InputComment{
		{ID: "target-comment", Nickname: "target", Identifier: "target-id", ParticipantKind: selection.Fixed, Kind: selection.Text, Text: "target", PostedAt: &draftNow},
		{ID: "safe-1", Nickname: "safe one", Identifier: "safe-1", ParticipantKind: selection.Fixed, Kind: selection.Text, Text: "target safe", PostedAt: &draftNow},
		{ID: "safe-2", Nickname: "safe two", Identifier: "safe-2", ParticipantKind: selection.Fixed, Kind: selection.Text, Text: "target safe", PostedAt: &draftNow},
	}
	if group == selection.AutoExcluded {
		snapshot.Comments[0].Text = "excluded"
	}
	draft := newDraftHarness(t, testCollector(func(context.Context, string, func(CollectionProgress)) (CollectionSnapshot, error) {
		return snapshot, nil
	}))
	if _, err := draft.Load(context.Background(), contracts.LoadArticleRequest{DraftMutationHeader: draftHeader(draft, "manual-load"), URL: "example"}); err != nil {
		t.Fatal(err)
	}
	draft.workers.Wait()
	draft.mu.Lock()
	draft.participants[0].Manual = manual
	draft.draft.Filters = contracts.FilterConfiguration{IncludeKeywords: []string{}, ExcludeKeywords: []string{}}
	if group == selection.AutoIncluded {
		draft.draft.Filters.IncludeKeywords = []string{"target"}
	}
	if group == selection.AutoExcluded {
		draft.draft.Filters.ExcludeKeywords = []string{"excluded"}
	}
	draft.mu.Unlock()
	return draft
}

func TestFreezePreservesBothManualBitsForEveryGroupWithoutDerivingFromFinalIncluded(t *testing.T) {
	for _, group := range []selection.Group{selection.Unclassified, selection.AutoIncluded, selection.AutoExcluded} {
		for _, included := range []bool{false, true} {
			for _, override := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/include-%t/override-%t", group, included, override), func(t *testing.T) {
					original := selection.ManualState{ManualIncluded: included, OverrideExcluded: override}
					draft := manualSnapshotDraft(t, group, original)
					request := finalizeRequest(draft, "manual-freeze")
					draft.mu.Lock()
					frozen, input, err := draft.freeze(request)
					draft.mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
					expectedIncluded := included
					if group == selection.AutoExcluded {
						expectedIncluded = override
					}
					target := frozen.Participants[0]
					if target.Manual == nil || *target.Manual != (rl.ManualStateSnapshot{ManualIncluded: included, OverrideExcluded: override}) || target.Classification != string(group) || target.Included != expectedIncluded {
						t.Fatalf("manual state was lost or inferred: %+v", target)
					}
					expectedCandidates := []contracts.ParticipantID{}
					for index, row := range frozen.Participants {
						if row.Manual == nil {
							t.Fatalf("new producer omitted manual state at%d", index)
						}
						if row.Included {
							expectedCandidates = append(expectedCandidates, row.ID)
						}
					}
					if !reflect.DeepEqual(input.CandidateIDs, expectedCandidates) {
						t.Fatal("manual metadata changed candidate partition")
					}
					target.Manual.ManualIncluded = !included
					target.Manual.OverrideExcluded = !override
					if frozen.Participants[1].Manual == target.Manual || frozen.Participants[1].Manual.ManualIncluded != true || frozen.Participants[1].Manual.OverrideExcluded != false {
						t.Fatal("manual pointer aliased another participant")
					}
					draft.mu.Lock()
					again, _, err := draft.freeze(request)
					source := draft.participants[0].Manual
					draft.mu.Unlock()
					if err != nil || source != original || *again.Participants[0].Manual != (rl.ManualStateSnapshot{ManualIncluded: included, OverrideExcluded: override}) {
						t.Fatal("frozen projection mutated draft manual state", err)
					}
				})
			}
		}
	}
}

func TestFinalizePersistsManualBitsAndReplayPreservesOriginalSelection(t *testing.T) {
	for _, included := range []bool{false, true} {
		for _, override := range []bool{false, true} {
			t.Run(fmt.Sprintf("include-%t/override-%t", included, override), func(t *testing.T) {
				draft := manualSnapshotDraft(t, selection.AutoExcluded, selection.ManualState{ManualIncluded: included, OverrideExcluded: override})
				store, db := finalizeStore(t)
				entropy := new(finalizeEntropy)
				lifecycle := finalizeLifecycle(t, store, new(finalizePreparation), entropy)
				request := finalizeRequest(draft, "manual-finalize")
				round, err := draft.CreateCollection(context.Background(), request, lifecycle)
				if err != nil || round.State != contracts.Completed {
					t.Fatal(round, err)
				}
				frozen, err := store.ReadCollection(context.Background(), round.CollectionID)
				if err != nil || len(frozen.Participants) != 3 {
					t.Fatal(frozen, err)
				}
				expected := rl.ManualStateSnapshot{ManualIncluded: included, OverrideExcluded: override}
				if frozen.Participants[0].Manual == nil || *frozen.Participants[0].Manual != expected || frozen.Participants[0].Included != override {
					t.Fatal("persistence derived bits from final selection", frozen.Participants[0])
				}
				var body string
				if err = db.QueryRow("SELECT body_json FROM participants WHERE collection_id=? AND position=0", round.CollectionID).Scan(&body); err != nil {
					t.Fatal(err)
				}
				var raw map[string]json.RawMessage
				if err = json.Unmarshal([]byte(body), &raw); err != nil || len(raw["Manual"]) == 0 {
					t.Fatal("actual participant row omitted Manual", body, err)
				}
				replay, err := draft.CreateCollection(context.Background(), request, lifecycle)
				if err != nil || !reflect.DeepEqual(replay, round) || entropy.calls.Load() != 1 {
					t.Fatal("manual replay executed or changed round", err)
				}
				frozen.Participants[0].Manual.ManualIncluded = !included
				frozen.Participants[0].Manual.OverrideExcluded = !override
				again, err := store.ReadCollection(context.Background(), round.CollectionID)
				if err != nil || *again.Participants[0].Manual != expected {
					t.Fatal("read result aliased durable manual state", err)
				}
			})
		}
	}
}

func TestFinalizeManualAdmissionPartialRollbackAndContinuousFailurePreserveDraftAndSameIDRetry(t *testing.T) {
	original := selection.ManualState{ManualIncluded: false, OverrideExcluded: true}
	draft := manualSnapshotDraft(t, selection.AutoExcluded, original)
	before := getDraft(t, draft)
	store, db := finalizeStore(t)
	entropy := new(finalizeEntropy)
	lifecycle := finalizeLifecycle(t, store, new(finalizePreparation), entropy)
	request := finalizeRequest(draft, "manual-rollback")
	if _, err := db.Exec("CREATE TRIGGER manual_snapshot_fault AFTER INSERT ON participants WHEN NEW.position=1 BEGIN SELECT RAISE(ABORT,'manual snapshot fault'); END"); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		_, err := draft.CreateCollection(context.Background(), request, lifecycle)
		finalizeFault(t, err, contracts.StorageUnavailable)
		assertFinalizeFenceStorageEmpty(t, store, db, request.OperationID)
		draft.mu.Lock()
		actual := draft.participants[0].Manual
		draft.mu.Unlock()
		if actual != original || !reflect.DeepEqual(getDraft(t, draft), before) || entropy.calls.Load() != 0 {
			t.Fatal("failed partial admission changed draft manual, sealing or entropy")
		}
	}
	if _, err := db.Exec("DROP TRIGGER manual_snapshot_fault"); err != nil {
		t.Fatal(err)
	}
	round, err := draft.CreateCollection(context.Background(), request, lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.ReadCollection(context.Background(), round.CollectionID)
	if err != nil || stored.Participants[0].Manual == nil || *stored.Participants[0].Manual != (rl.ManualStateSnapshot{ManualIncluded: false, OverrideExcluded: true}) {
		t.Fatal("same ID retry lost original manual state", err)
	}
}

func TestFinalizeManualCancellationDuringPreparationKeepsBothBitsAndUnconsumedOperation(t *testing.T) {
	original := selection.ManualState{ManualIncluded: false, OverrideExcluded: true}
	draft := manualSnapshotDraft(t, selection.AutoExcluded, original)
	before := getDraft(t, draft)
	store, db := finalizeStore(t)
	preparation := &finalizePreparation{entered: make(chan struct{}), release: make(chan struct{})}
	defer preparation.unblock()
	entropy := new(finalizeEntropy)
	lifecycle := finalizeLifecycle(t, store, preparation, entropy)
	request := finalizeRequest(draft, "manual-cancel-preparation")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := draft.CreateCollection(ctx, request, lifecycle); done <- err }()
	finalizeAwait(t, preparation.entered)
	cancel()
	if err := finalizeAwait(t, done); err != context.Canceled {
		t.Fatal("cancelled preparation was admitted", err)
	}
	assertFinalizeFenceStorageEmpty(t, store, db, request.OperationID)
	draft.mu.Lock()
	actual := draft.participants[0].Manual
	draft.mu.Unlock()
	if actual != original || !reflect.DeepEqual(getDraft(t, draft), before) || entropy.calls.Load() != 0 {
		t.Fatal("cancel changed manual state, sealing or entropy")
	}
	preparation.entered = nil
	preparation.unblock()
	round, err := draft.CreateCollection(context.Background(), request, lifecycle)
	if err != nil || round.State != contracts.Completed || preparation.calls.Load() != 2 || entropy.calls.Load() != 1 {
		t.Fatal("cancelled operation could not retry once", round, err)
	}
	stored, err := store.ReadCollection(context.Background(), round.CollectionID)
	if err != nil || *stored.Participants[0].Manual != (rl.ManualStateSnapshot{ManualIncluded: false, OverrideExcluded: true}) {
		t.Fatal("cancel retry lost manual bits", err)
	}
}
