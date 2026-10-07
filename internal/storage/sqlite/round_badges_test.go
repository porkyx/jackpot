package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/draw"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func badgeRequest(mode string, enabled bool) rl.CreateCollectionRequest {
	request := productRequest(storageNow, mode)
	rules := contracts.DefaultBadgeRules()
	rules.WeightingEnabled = enabled
	if enabled {
		rules.Fixed.Weight = 0
		rules.SemiFixed.Weight = 0
		rules.SubManager.Weight = 0
		rules.NewAccount.Weight = 0
		rules.Anonymous.Weight = 0
	}
	request.Collection.Filters.BadgeRules = &rules
	request.Input.BadgeRules = contracts.CloneBadgeRules(&rules)
	request.Collection.Participants = nil
	request.Input.CandidateIDs = nil
	request.Input.CandidateCategories = nil
	order := contracts.BadgeCategories()
	categories := append([]contracts.BadgeCategory{}, order[:]...)
	categories = append(categories, contracts.BadgeMainManager)
	for index, category := range categories {
		kind := "fixed"
		if category == contracts.BadgeSemiFixed {
			kind = "semi_fixed"
		}
		if category == contracts.BadgeAnonymous {
			kind = "anonymous"
		}
		participant := rl.ParticipantSnapshot{ID: contracts.ParticipantID(fmt.Sprintf("p%d", index+1)), Nickname: fmt.Sprintf("nickname%d", index+1), PublicIdentifier: fmt.Sprintf("public%d", index+1), Kind: kind, BadgeCategory: category, Included: true, Manual: &rl.ManualStateSnapshot{ManualIncluded: true}, Comments: []rl.CommentSnapshot{{ID: fmt.Sprintf("comment%d", index+1), Kind: "text", Text: "댓글"}}}
		request.Collection.Participants = append(request.Collection.Participants, participant)
		drawable, err := rl.ParticipantDrawable(participant, &rules)
		if err != nil {
			panic(err)
		}
		if drawable {
			request.Input.CandidateIDs = append(request.Input.CandidateIDs, participant.ID)
			request.Input.CandidateCategories = append(request.Input.CandidateCategories, category)
		}
	}
	return request
}

func TestBadgeWeightedSQLiteRestartRerunExhaustionReplayAndSnapshotOwnership(t *testing.T) {
	ctx := context.Background()
	store := migratedTestStore(t)
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
	request := badgeRequest(rl.ImmediateMode, true)
	first, err := service.CreateCollection(ctx, request)
	if err != nil || first.State != contracts.Completed || first.Outcome.AlgorithmVersion != draw.CategoryAlgorithmVersion || first.Outcome.Winners[0].ParticipantID != "p3" || entropy.calls.Load() != 2 {
		t.Fatal(first, err)
	}
	before := corruptionDurableSnapshot(t, store)
	replay, err := service.CreateCollection(ctx, request)
	if err != nil || !reflect.DeepEqual(first, replay) || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 2 {
		t.Fatal("replay changed outcome", replay, err)
	}
	frozen, rev, err := store.ReadCollectionSnapshot(ctx, first.CollectionID)
	if err != nil || rev != first.Revision || !reflect.DeepEqual(frozen.Filters.BadgeRules, request.Collection.Filters.BadgeRules) || !reflect.DeepEqual(frozen.Participants, request.Collection.Participants) {
		t.Fatal(frozen, rev, err)
	}
	frozen.Filters.BadgeRules.MainManager.Weight = 0
	frozen.Participants[2].BadgeCategory = contracts.BadgeFixed
	if err = service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	path := store.path
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	freshEntropy := new(productEntropy)
	fresh := productService(t, reopened, freshEntropy, func() time.Time { return storageNow }, "session-two", nil)
	if report, err := fresh.Recover(ctx, rl.RecoverRequest{}); err != nil || freshEntropy.calls.Load() != 0 || len(report.Completed) != 0 {
		t.Fatal(report, err)
	}
	second, err := fresh.Rerun(ctx, rl.RerunRequest{OperationID: "badge-rerun", Context: contracts.CollectionContext{BackendSessionID: "session-two", CollectionID: first.CollectionID, Revision: first.Revision}, Prizes: request.Input.Prizes, Mode: rl.ImmediateMode})
	if err != nil || second.Outcome.Winners[0].ParticipantID != "p7" || len(second.Input.CandidateIDs) != 1 || !reflect.DeepEqual(second.Input.BadgeRules, request.Input.BadgeRules) || freshEntropy.calls.Load() != 2 {
		t.Fatal(second, err)
	}
	before = corruptionDurableSnapshot(t, reopened)
	_, err = fresh.Rerun(ctx, rl.RerunRequest{OperationID: "badge-exhausted", Context: contracts.CollectionContext{BackendSessionID: "session-two", CollectionID: second.CollectionID, Revision: second.Revision}, Prizes: request.Input.Prizes, Mode: rl.ImmediateMode})
	requireFault(t, err, contracts.InvalidInput)
	if before != corruptionDurableSnapshot(t, reopened) || freshEntropy.calls.Load() != 2 {
		t.Fatal("exhaustion wrote or drew")
	}
	page, err := reopened.ReadCollectionPage(ctx, first.CollectionID, rl.CollectionPageQuery{Limit: 50})
	if err != nil || page.ConsumedCount != 2 || len(page.Rounds) != 2 || len(page.Frozen.Participants) != 7 {
		t.Fatal(page, err)
	}
	assertRoundSQLIntegrity(t, reopened)
	assertNoHandles(t, reopened)
}

func TestBadgeWeightedSQLiteReservationRecoveryUsesFrozenRatios(t *testing.T) {
	ctx := context.Background()
	store := migratedTestStore(t)
	now := storageNow
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
	request := badgeRequest(rl.ReservationMode, true)
	round, err := service.CreateCollection(ctx, request)
	if err != nil || entropy.calls.Load() != 0 {
		t.Fatal(round, err)
	}
	delay := uint32(30)
	round, err = service.SetSchedule(ctx, rl.SetScheduleRequest{OperationID: "badge-schedule", Context: productRoundContext(round, "session-one"), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
	if err != nil || round.State != contracts.Scheduled {
		t.Fatal(round, err)
	}
	before := corruptionDurableSnapshot(t, store)
	request.Input.BadgeRules.MainManager.Weight = 0
	request.Collection.Participants[2].BadgeCategory = contracts.BadgeFixed
	if _, err = service.Recover(ctx, rl.RecoverRequest{}); err != nil || entropy.calls.Load() != 0 || before != corruptionDurableSnapshot(t, store) {
		t.Fatal("early recovery", err)
	}
	if err = service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	fresh := productService(t, store, entropy, func() time.Time { return now }, "session-two", nil)
	now = now.Add(31 * time.Second)
	report, err := fresh.Recover(ctx, rl.RecoverRequest{})
	if err != nil || len(report.Completed) != 1 || entropy.calls.Load() != 2 {
		t.Fatal(report, err)
	}
	completed, err := store.ReadRound(ctx, round.ID)
	if err != nil || completed.Outcome.Winners[0].ParticipantID != "p3" || completed.Outcome.AlgorithmVersion != draw.CategoryAlgorithmVersion {
		t.Fatal(completed, err)
	}
	before = corruptionDurableSnapshot(t, store)
	if _, err = fresh.Recover(ctx, rl.RecoverRequest{}); err != nil || entropy.calls.Load() != 2 || before != corruptionDurableSnapshot(t, store) {
		t.Fatal("duplicate due execution", err)
	}
	assertNoHandles(t, store)
}

func TestBadgeSQLiteInvalidAdmissionAndPartialWriteFailureLeaveNoStateOrEntropy(t *testing.T) {
	for _, mode := range []string{"zero ratios", "one candidate", "category mismatch", "weight mismatch", "omitted candidate", "participant write failure"} {
		t.Run(mode, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			request := badgeRequest(rl.ImmediateMode, true)
			switch mode {
			case "zero ratios":
				request.Collection.Filters.BadgeRules.MainManager.Weight = 0
				request.Input.BadgeRules.MainManager.Weight = 0
			case "one candidate":
				request.Input.CandidateIDs = request.Input.CandidateIDs[:1]
				request.Input.CandidateCategories = request.Input.CandidateCategories[:1]
			case "category mismatch":
				request.Input.CandidateCategories[0] = contracts.BadgeSubManager
			case "weight mismatch":
				request.Input.BadgeRules.MainManager.Weight = 99
			case "omitted candidate":
				request.Input.CandidateIDs = request.Input.CandidateIDs[1:]
				request.Input.CandidateCategories = request.Input.CandidateCategories[1:]
			case "participant write failure":
				execSQL(t, store.db, `CREATE TRIGGER fail_badge_participant BEFORE INSERT ON participants WHEN NEW.position=1 BEGIN SELECT RAISE(ABORT,'fixture failure'); END`)
			}
			before := corruptionDurableSnapshot(t, store)
			if _, err := service.CreateCollection(context.Background(), request); err == nil {
				t.Fatal("invalid create accepted")
			}
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 0 {
				t.Fatal("invalid create persisted or drew")
			}
			assertNoHandles(t, store)
		})
	}
}

func TestBadgeSQLiteCorruptionRejectsReadAndRecoveryWithoutRepair(t *testing.T) {
	for _, mode := range []string{"input downgrade", "input category", "frozen weight", "participant category"} {
		t.Run(mode, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			request := badgeRequest(rl.ReservationMode, true)
			round, err := service.CreateCollection(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "input downgrade":
				input := round.Input
				input.BadgeRules = nil
				input.CandidateCategories = nil
				execSQL(t, store.db, `DROP TRIGGER immutable_round_input`)
				execSQL(t, store.db, `UPDATE rounds SET input_json=? WHERE id=?`, jsonValue(t, input), round.ID)
			case "input category":
				input := round.Input
				input.CandidateCategories[0] = contracts.BadgeSubManager
				execSQL(t, store.db, `DROP TRIGGER immutable_round_input`)
				execSQL(t, store.db, `UPDATE rounds SET input_json=? WHERE id=?`, jsonValue(t, input), round.ID)
			case "frozen weight":
				var raw string
				if err = store.db.QueryRow(`SELECT frozen_json FROM collections WHERE id=?`, round.CollectionID).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var frozen rl.FrozenCollection
				if err = json.Unmarshal([]byte(raw), &frozen); err != nil {
					t.Fatal(err)
				}
				frozen.Filters.BadgeRules.MainManager.Weight = 99
				execSQL(t, store.db, `DROP TRIGGER immutable_collection`)
				execSQL(t, store.db, `UPDATE collections SET frozen_json=? WHERE id=?`, jsonValue(t, frozen), round.CollectionID)
			case "participant category":
				participant := request.Collection.Participants[2]
				participant.BadgeCategory = contracts.BadgeSubManager
				execSQL(t, store.db, `DROP TRIGGER immutable_participant`)
				execSQL(t, store.db, `UPDATE participants SET body_json=? WHERE collection_id=? AND id=?`, jsonValue(t, participant), round.CollectionID, participant.ID)
			}
			before := corruptionDurableSnapshot(t, store)
			if _, err = store.ReadRound(context.Background(), round.ID); err == nil {
				t.Fatal("corrupt round read")
			}
			if _, err = service.Recover(context.Background(), rl.RecoverRequest{}); err == nil {
				t.Fatal("corrupt recovery accepted")
			}
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 0 {
				t.Fatal("corruption repaired or drew")
			}
			assertNoHandles(t, store)
		})
	}
}

func TestBadgeSQLiteDisabledAndLegacyRulesKeepUniformAlgorithmAndPersistedJSON(t *testing.T) {
	for _, mode := range []string{"legacy", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			request := productRequest(storageNow, rl.ImmediateMode)
			if mode == "disabled" {
				request = badgeRequest(rl.ImmediateMode, false)
				request.Collection.Filters.BadgeRules.MainManager.Weight = 0
				request.Input.BadgeRules.MainManager.Weight = 0
			}
			round, err := service.CreateCollection(context.Background(), request)
			if err != nil || round.Outcome.AlgorithmVersion != draw.AlgorithmVersion || entropy.calls.Load() != 1 {
				t.Fatal(round, err)
			}
			if mode == "legacy" {
				var raw string
				if err = store.db.QueryRow(`SELECT input_json FROM rounds WHERE id=?`, round.ID).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				if raw != jsonValue(t, request.Input) {
					t.Fatal("old input JSON changed", raw)
				}
			}
			if _, err = store.ReadCollectionPage(context.Background(), round.CollectionID, rl.CollectionPageQuery{Limit: 50}); err != nil {
				t.Fatal(err)
			}
			assertNoHandles(t, store)
		})
	}
}

func TestBadgeSQLiteEntropyAndOutcomeInsertFailureRetrySameFrozenInput(t *testing.T) {
	for _, mode := range []string{"entropy continuous", "outcome partial insert"} {
		t.Run(mode, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			request := badgeRequest(rl.ImmediateMode, true)
			request.Input.Prizes[0].Count = 2
			if mode == "entropy continuous" {
				entropy.fail.Store(true)
			} else {
				execSQL(t, store.db, `CREATE TRIGGER fail_badge_winner BEFORE INSERT ON winners WHEN NEW.slot=2 BEGIN SELECT RAISE(ABORT,'late result failure'); END`)
			}
			failed, err := service.CreateCollection(context.Background(), request)
			if err == nil || failed.State != contracts.Failed || failed.Outcome != nil || !reflect.DeepEqual(failed.Input, request.Input) {
				t.Fatal(failed, err)
			}
			var results, winners int
			if err = store.db.QueryRow(`SELECT count(*) FROM results`).Scan(&results); err != nil {
				t.Fatal(err)
			}
			if err = store.db.QueryRow(`SELECT count(*) FROM winners`).Scan(&winners); err != nil {
				t.Fatal(err)
			}
			if results != 0 || winners != 0 {
				t.Fatal("partial outcome persisted", results, winners)
			}
			if mode == "entropy continuous" {
				failed, err = service.RetryRound(context.Background(), rl.RetryRequest{OperationID: "fail-again", Context: productRoundContext(failed, "session-one")})
				if err == nil || failed.State != contracts.Failed || failed.Attempt != 2 || entropy.calls.Load() != 2 {
					t.Fatal(failed, err)
				}
				entropy.fail.Store(false)
			} else {
				execSQL(t, store.db, `DROP TRIGGER fail_badge_winner`)
			}
			completed, err := service.RetryRound(context.Background(), rl.RetryRequest{OperationID: "badge-retry", Context: productRoundContext(failed, "session-one")})
			if err != nil || completed.State != contracts.Completed || completed.Outcome.AlgorithmVersion != draw.CategoryAlgorithmVersion || len(completed.Outcome.Winners) != 2 || completed.Outcome.Winners[0].ParticipantID == completed.Outcome.Winners[1].ParticipantID || !reflect.DeepEqual(completed.Input, request.Input) {
				t.Fatal(completed, err)
			}
			before := corruptionDurableSnapshot(t, store)
			calls := entropy.calls.Load()
			if _, err = service.RetryRound(context.Background(), rl.RetryRequest{OperationID: "badge-retry", Context: productRoundContext(failed, "session-one")}); err != nil || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != calls {
				t.Fatal("retry replay wrote/drew", err)
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}

func TestBadgeRawRerunCannotOmitEligibleCandidatesOrDowngradeRules(t *testing.T) {
	for _, mode := range []string{"weighted subset", "nil downgrade", "legacy nil corrupt snapshot"} {
		t.Run(mode, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			request := badgeRequest(rl.ImmediateMode, false)
			request.Input.BadgeRules.WeightingEnabled = true
			request.Collection.Filters.BadgeRules.WeightingEnabled = true
			if mode == "legacy nil corrupt snapshot" {
				request = productRequest(storageNow, rl.ImmediateMode)
			}
			round, err := service.CreateCollection(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			input := round.Input
			input.CandidateIDs = append([]contracts.ParticipantID{}, request.Input.CandidateIDs[1:]...)
			if input.BadgeRules != nil {
				input.CandidateCategories = append([]contracts.BadgeCategory{}, request.Input.CandidateCategories[1:]...)
			}
			if mode == "weighted subset" {
				input.CandidateIDs = input.CandidateIDs[:1]
				input.CandidateCategories = input.CandidateCategories[:1]
			}
			if mode == "nil downgrade" {
				input.BadgeRules = nil
				input.CandidateCategories = nil
			}
			if mode == "legacy nil corrupt snapshot" {
				execSQL(t, store.db, `DROP TRIGGER immutable_collection`)
				execSQL(t, store.db, `UPDATE collections SET frozen_json='{"Unexpected":true}' WHERE id=?`, round.CollectionID)
			}
			before := corruptionDurableSnapshot(t, store)
			calls := entropy.calls.Load()
			admit := rl.AdmitRoundRequest{Operation: rl.OperationIdentity{ID: "raw-badge-rerun", Kind: "Rerun"}, CollectionID: round.CollectionID, ExpectedRevision: round.Revision, RoundID: "raw-badge-round", Number: 2, Attempt: 1, Input: input, InitialState: contracts.Executing, AdmittedAt: storageNow}
			for repeat := 0; repeat < 2; repeat++ {
				if _, err = store.AdmitRound(context.Background(), admit); err == nil {
					t.Fatal("biased/downgraded/corrupt admission accepted")
				}
				if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != calls {
					t.Fatal("rejection changed state/entropy")
				}
				assertNoHandles(t, store)
			}
			if mode != "legacy nil corrupt snapshot" {
				admit.Input = round.Input
				admit.Input.CandidateIDs = append([]contracts.ParticipantID{}, request.Input.CandidateIDs[1:]...)
				admit.Input.CandidateCategories = append([]contracts.BadgeCategory{}, request.Input.CandidateCategories[1:]...)
				accepted, err := store.AdmitRound(context.Background(), admit)
				if err != nil || accepted.Round.State != contracts.Executing {
					t.Fatal("corrected exact candidate set rejected", accepted, err)
				}
				if _, err = service.ExecuteAdmission(context.Background(), accepted); err != nil {
					t.Fatal(err)
				}
				assertRoundSQLIntegrity(t, store)
				assertNoHandles(t, store)
			}
		})
	}
}
