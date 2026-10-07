package sqlite

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

type preparationReadCounter struct {
	*Store
	operationReads, sourceReads atomic.Int32
}

func (storage *preparationReadCounter) ReadOperation(ctx context.Context, id contracts.OperationID) (rl.OperationRecord, error) {
	storage.operationReads.Add(1)
	return storage.Store.ReadOperation(ctx, id)
}
func (storage *preparationReadCounter) FindCollectionByDraft(ctx context.Context, id contracts.DraftID) (contracts.CollectionID, error) {
	storage.sourceReads.Add(1)
	return storage.Store.FindCollectionByDraft(ctx, id)
}

func TestRoundCreateValidationIndependentMetadataSelectionAndParticipantConditionsBeforeStorageWork(t *testing.T) {
	for _, condition := range []string{"valid", "operation empty", "context revision zero", "context generation zero", "context revision overflow", "context generation overflow", "context draft ID empty", "source draft mismatch", "finalized revision mismatch", "generation mismatch", "incomplete snapshot", "snapshot ID empty", "participant limit at maximum", "participant limit exceeded", "minimum two included candidates", "one included candidate", "participant ID empty", "participant ID duplicate", "included count mismatch", "included order mismatch"} {
		t.Run(condition, func(t *testing.T) {
			store := migratedTestStore(t)
			ports := &preparationReadCounter{Store: store}

			entropy := new(productEntropy)
			service, err := rl.NewService(rl.ServiceOptions{Storage: ports, Entropy: entropy, Session: "session-one", Clock: func() time.Time { return storageNow }})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := service.Close(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			request := productRequest(storageNow, rl.ImmediateMode)
			switch condition {
			case "operation empty":
				request.OperationID = ""
			case "context revision zero":
				request.Context.Revision = 0
				request.Collection.FinalizedDraftRevision = 0
			case "context generation zero":
				request.Context.ArticleGeneration = 0
				request.Collection.ArticleGeneration = 0
			case "context revision overflow":
				request.Context.Revision = contracts.Revision(contracts.MaxSafeInteger + 1)
				request.Collection.FinalizedDraftRevision = request.Context.Revision
			case "context generation overflow":
				request.Context.ArticleGeneration = contracts.ArticleGeneration(contracts.MaxSafeInteger + 1)
				request.Collection.ArticleGeneration = request.Context.ArticleGeneration
			case "context draft ID empty":
				request.Context.DraftID = ""
				request.Collection.SourceDraftID = ""
			case "source draft mismatch":
				request.Collection.SourceDraftID = "different-draft"
			case "finalized revision mismatch":
				request.Collection.FinalizedDraftRevision++
			case "generation mismatch":
				request.Collection.ArticleGeneration++
			case "incomplete snapshot":
				request.Collection.Snapshot.Complete = false
			case "snapshot ID empty":
				request.Collection.Snapshot.SnapshotID = ""
			case "participant limit at maximum", "participant limit exceeded":
				participantCount := 100001
				if condition == "participant limit at maximum" {
					participantCount = 100000
				}
				request.Collection.Participants = append(request.Collection.Participants, make([]rl.ParticipantSnapshot, participantCount-len(request.Collection.Participants))...)
				for index := 3; index < len(request.Collection.Participants); index++ {
					request.Collection.Participants[index].ID = contracts.ParticipantID(fmt.Sprintf("excluded-%d", index))
				}
			case "minimum two included candidates":
				request.Collection.Participants[2].Included = false
				request.Input.CandidateIDs = request.Input.CandidateIDs[:2]
			case "one included candidate":
				request.Collection.Participants[1].Included = false
				request.Collection.Participants[2].Included = false
				request.Input.CandidateIDs = request.Input.CandidateIDs[:1]
			case "participant ID empty":
				request.Collection.Participants[2].Included = false
				request.Collection.Participants[2].ID = ""
				request.Input.CandidateIDs = request.Input.CandidateIDs[:2]
			case "participant ID duplicate":
				request.Collection.Participants[2].Included = false
				request.Collection.Participants[2].ID = "p1"
				request.Input.CandidateIDs = request.Input.CandidateIDs[:2]
			case "included count mismatch":
				request.Input.CandidateIDs = request.Input.CandidateIDs[:2]
			case "included order mismatch":
				request.Input.CandidateIDs[0], request.Input.CandidateIDs[1] = request.Input.CandidateIDs[1], request.Input.CandidateIDs[0]
			}
			before := corruptionDurableSnapshot(t, store)
			prepared, err := service.PrepareCreate(context.Background(), request)
			if prepared != nil {
				prepared.Close()
				prepared.Close()
			}
			if condition == "valid" || condition == "context revision zero" || condition == "context generation zero" || condition == "participant limit at maximum" || condition == "minimum two included candidates" {
				if err != nil || prepared == nil || ports.operationReads.Load() != 1 || ports.sourceReads.Load() != 1 {
					t.Fatal("positive preparation failed", prepared, err)
				}
			} else {
				requireFault(t, err, contracts.InvalidInput)
				if prepared != nil || ports.operationReads.Load() != 0 || ports.sourceReads.Load() != 0 {
					t.Fatal("invalid metadata reached credential or storage ports", prepared, err)
				}
			}
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 0 {
				t.Fatal("preparation persisted rows or consumed entropy")
			}
			assertNoHandles(t, store)
		})
	}
}

func TestRoundRerunNumberAndRetryAttemptMaximumRejectBeforeOverflowOrAdmission(t *testing.T) {
	for _, boundary := range []string{"round number maximum", "attempt maximum"} {
		t.Run(boundary, func(t *testing.T) {
			store := migratedTestStore(t)

			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			request := productRequest(storageNow, rl.ImmediateMode)
			frozen := request.Collection
			frozen.ID = "boundary-collection"

			number, attempt := uint32(1), uint32(1)
			if boundary == "round number maximum" {
				number = math.MaxUint32
			} else {
				attempt = math.MaxUint32
			}
			// Construct representable max counters through real admission/claim/final
			// storage transactions, without bypassing constraints or fabricating DTOs.
			admission, err := store.AdmitRound(context.Background(), rl.AdmitRoundRequest{Operation: rl.OperationIdentity{ID: "boundary-create", Kind: "CreateCollection"}, CollectionID: frozen.ID, ExpectedRevision: request.Context.Revision, NewCollection: &frozen, RoundID: "boundary-round", Number: number, Attempt: attempt, Input: request.Input, InitialState: contracts.Executing, AdmittedAt: storageNow})
			if err != nil {
				t.Fatal(err)
			}
			if boundary == "attempt maximum" {
				entropy.fail.Store(true)
			}
			current, err := service.ExecuteAdmission(context.Background(), admission)
			if boundary == "attempt maximum" {
				requireFault(t, err, contracts.InvalidState)
			} else if err != nil {
				t.Fatal(err)
			}
			entropy.fail.Store(false)

			before := corruptionDurableSnapshot(t, store)
			calls := entropy.calls.Load()
			var result rl.RoundRecord
			if boundary == "round number maximum" {
				result, err = service.Rerun(context.Background(), rl.RerunRequest{OperationID: "overflow-rerun", Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: current.CollectionID, Revision: current.Revision}, Prizes: []rl.Prize{{ID: "next", Count: 1}}, Mode: rl.ImmediateMode})
			} else {
				result, err = service.RetryRound(context.Background(), rl.RetryRequest{OperationID: "overflow-retry", Context: productRoundContext(current, "session-one")})
			}
			requireFault(t, err, contracts.InvalidState)
			if !reflect.DeepEqual(result, rl.RoundRecord{}) || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != calls {
				t.Fatal("max counter admitted or wrapped", result, err)
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}

func TestRoundRerunCandidatePredicateExcludesUnusedUnselectedAndPreviouslyConsumed(t *testing.T) {
	store := migratedTestStore(t)
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
	request := productRequest(storageNow, rl.ImmediateMode)
	request.Collection.Participants = append(request.Collection.Participants, rl.ParticipantSnapshot{ID: "p4-unselected", Included: false})
	first, err := service.CreateCollection(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Rerun(context.Background(), rl.RerunRequest{OperationID: "candidate-predicate", Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: first.CollectionID, Revision: first.Revision}, Prizes: []rl.Prize{{ID: "second", Count: 1}}, Mode: rl.ImmediateMode})
	if err != nil || !reflect.DeepEqual(second.Input.CandidateIDs, []contracts.ParticipantID{"p2", "p3"}) || second.Outcome == nil || second.Outcome.Winners[0].ParticipantID != "p2" || entropy.calls.Load() != 2 {
		t.Fatal("Included/used predicate admitted excluded or consumed participant", second, err)
	}
	assertRoundSQLIntegrity(t, store)
	assertNoHandles(t, store)
}
func TestRoundRerunCompletedRequiresImmediateWhileCancelledAllowsBothModes(t *testing.T) {
	for _, condition := range []string{"completed reservation rejected", "cancelled immediate allowed", "cancelled reservation allowed"} {
		t.Run(condition, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			mode := rl.ReservationMode
			if condition == "completed reservation rejected" {
				mode = rl.ImmediateMode
			}
			current, err := service.CreateCollection(context.Background(), productRequest(storageNow, mode))
			if err != nil {
				t.Fatal(err)
			}
			if mode == rl.ReservationMode {
				current, err = service.CancelSchedule(context.Background(), rl.CancelRequest{OperationID: "cancel-for-mode", Context: productRoundContext(current, "session-one")})
				if err != nil {
					t.Fatal(err)
				}
			}
			requestedMode := rl.ReservationMode
			if condition == "cancelled immediate allowed" {
				requestedMode = rl.ImmediateMode
			}
			before := corruptionDurableSnapshot(t, store)
			calls := entropy.calls.Load()
			result, err := service.Rerun(context.Background(), rl.RerunRequest{OperationID: "mode-rerun", Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: current.CollectionID, Revision: current.Revision}, Prizes: []rl.Prize{{ID: "next", Count: 1}}, Mode: requestedMode})
			if condition == "completed reservation rejected" {
				requireFault(t, err, contracts.InvalidInput)
				if !reflect.DeepEqual(result, rl.RoundRecord{}) || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != calls {
					t.Fatal("forbidden rerun mode mutated state")
				}
			} else {
				expected := contracts.PendingSchedule
				delta := int32(0)
				if requestedMode == rl.ImmediateMode {
					expected = contracts.Completed
					delta = 1
				}
				if err != nil || result.Number != 2 || result.State != expected || entropy.calls.Load() != calls+delta {
					t.Fatal("cancelled state lost allowed mode", result, err)
				}
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}
func TestRoundRetryRejectsEachHealthyNonFailedStateWithoutAdmission(t *testing.T) {
	for _, state := range []contracts.RoundState{contracts.PendingSchedule, contracts.Scheduled, contracts.Cancelled, contracts.Completed, contracts.Executing} {
		t.Run(string(state), func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			mode := rl.ReservationMode
			if state == contracts.Completed || state == contracts.Executing {
				mode = rl.ImmediateMode
			}
			request := productRequest(storageNow, mode)
			var current rl.RoundRecord
			var err error
			if state == contracts.Executing {
				prepared, prepareErr := service.PrepareCreate(context.Background(), request)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				defer prepared.Close()
				admission, admitErr := service.AdmitCreate(context.Background(), prepared, request.Collection)
				if admitErr != nil {
					t.Fatal(admitErr)
				}
				current = admission.Round
			} else {
				current, err = service.CreateCollection(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
			}
			if state == contracts.Scheduled {
				delay := uint32(30)
				current, err = service.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: "schedule-no-retry", Context: productRoundContext(current, "session-one"), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
				if err != nil {
					t.Fatal(err)
				}
			}
			if state == contracts.Cancelled {
				current, err = service.CancelSchedule(context.Background(), rl.CancelRequest{OperationID: "cancel-no-retry", Context: productRoundContext(current, "session-one")})
				if err != nil {
					t.Fatal(err)
				}
			}
			before := corruptionDurableSnapshot(t, store)
			calls := entropy.calls.Load()
			result, err := service.RetryRound(context.Background(), rl.RetryRequest{OperationID: "wrong-state-retry", Context: productRoundContext(current, "session-one")})
			requireFault(t, err, contracts.InvalidState)
			if !reflect.DeepEqual(result, rl.RoundRecord{}) || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != calls {
				t.Fatal("retry admitted healthy nonfailed state", result, err)
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}
