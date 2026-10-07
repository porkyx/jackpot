package sqlite

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func permissionCommand(service *rl.Service, kind string, ctx context.Context, operation contracts.OperationID, c contracts.RoundContext) (rl.RoundRecord, error) {
	switch kind {
	case "Rerun":
		return service.Rerun(ctx, rl.RerunRequest{OperationID: operation, Context: c.CollectionContext, Prizes: []rl.Prize{{ID: "next-prize", Count: 1}}, Mode: rl.ImmediateMode})
	case "SetSchedule":
		delay := uint32(30)
		return service.SetSchedule(ctx, rl.SetScheduleRequest{OperationID: operation, Context: c, QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
	case "CancelSchedule":
		return service.CancelSchedule(ctx, rl.CancelRequest{OperationID: operation, Context: c})
	case "RetryRound":
		return service.RetryRound(ctx, rl.RetryRequest{OperationID: operation, Context: c})
	default:
		panic("unreachable permission action")
	}
}

func TestRoundLocalCommandsValidateSessionContextAndRevisionAfterServiceRestart(t *testing.T) {
	for _, kind := range []string{"Rerun", "SetSchedule", "CancelSchedule", "RetryRound"} {
		for _, condition := range []string{"valid", "nil context", "cancelled context", "empty operation", "empty session", "wrong session", "empty collection", "other collection", "same session service restart", "closed service", "wrong revision", "wrong round version"} {
			if kind == "Rerun" && condition == "wrong round version" {
				continue
			}
			t.Run(kind+"/"+condition, func(t *testing.T) {
				store := migratedTestStore(t)
				entropy := new(productEntropy)
				service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
				mode := rl.ReservationMode
				if kind == "Rerun" || kind == "RetryRound" {
					mode = rl.ImmediateMode
				}
				if kind == "RetryRound" {
					entropy.fail.Store(true)
				}
				current, err := service.CreateCollection(context.Background(), productRequest(storageNow, mode))
				if kind == "RetryRound" {
					requireFault(t, err, contracts.InvalidState)
				} else if err != nil {
					t.Fatal(err)
				}
				entropy.fail.Store(false)
				c := productRoundContext(current, "session-one")
				ctx := context.Background()
				operation := contracts.OperationID("permission-command")
				var wantCode contracts.ErrorCode
				var wantError error
				switch condition {
				case "nil context":
					ctx = nil
					wantCode = contracts.InvalidInput
				case "cancelled context":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
					wantError = context.Canceled
				case "empty operation":
					operation = ""
					wantCode = contracts.InvalidInput
				case "empty session":
					c.BackendSessionID = ""
					wantCode = contracts.InvalidInput
				case "wrong session":
					c.BackendSessionID = "session-other"
					wantCode = contracts.BackendSessionChanged
				case "empty collection":
					c.CollectionID = ""
					wantCode = contracts.InvalidInput
				case "other collection":
					c.CollectionID = "unknown-collection"
					wantCode = contracts.StorageUnavailable
					if kind == "RetryRound" {
						wantCode = contracts.StaleRevision
					}
				case "same session service restart":
					service = productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)

				case "closed service":
					if err := service.Close(context.Background()); err != nil {
						t.Fatal(err)
					}
					wantCode = contracts.InvalidState
				case "wrong revision":
					c.Revision++
					wantCode = contracts.StaleRevision
				case "wrong round version":
					c.Version++
					wantCode = contracts.StaleRevision
				}
				before := corruptionDurableSnapshot(t, store)
				beforeEntropy := entropy.calls.Load()
				result, err := permissionCommand(service, kind, ctx, operation, c)
				if condition == "valid" || condition == "same session service restart" {
					if err != nil {
						t.Fatal(result, err)
					}
					expected := contracts.Completed
					delta := int32(1)
					if kind == "SetSchedule" {
						expected = contracts.Scheduled
						delta = 0
					}
					if kind == "CancelSchedule" {
						expected = contracts.Cancelled
						delta = 0
					}
					if result.State != expected || entropy.calls.Load() != beforeEntropy+delta {
						t.Fatal("positive control failed", result, entropy.calls.Load())
					}
					op, readErr := store.ReadOperation(context.Background(), operation)
					if readErr != nil || op.Status != contracts.OperationSucceeded {
						t.Fatal(op, readErr)
					}
				} else {
					if wantCode != "" {
						requireFault(t, err, wantCode)
					} else if !errors.Is(err, wantError) {
						t.Fatal("local context error changed", err, wantError)
					}
					if !reflect.DeepEqual(result, rl.RoundRecord{}) || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != beforeEntropy {
						t.Fatal("rejected predicate changed SQL, entropy, published a result", result, err)
					}
					op, readErr := store.ReadOperation(context.Background(), operation)
					if operation != "" && (readErr != nil || op.Status != contracts.OperationUnknown) {
						t.Fatal("rejected request persisted operation", op, readErr)
					}
				}
				assertRoundSQLIntegrity(t, store)
				assertNoHandles(t, store)
			})
		}
	}
}

func TestRoundReplayKindFingerprintSessionAndFailureAreIndependentWithoutMutation(t *testing.T) {
	for _, condition := range []string{"same payload", "different kind", "different fingerprint", "new session", "failed replay"} {
		t.Run(condition, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			first, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ImmediateMode))
			if err != nil {
				t.Fatal(err)
			}
			request := rl.RerunRequest{OperationID: "replay-rerun", Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: first.CollectionID, Revision: first.Revision}, Prizes: []rl.Prize{{ID: "second-prize", Count: 1}}, Mode: rl.ImmediateMode}
			if condition == "failed replay" {
				entropy.fail.Store(true)
			}
			saved, err := service.Rerun(context.Background(), request)
			if condition == "failed replay" {
				requireFault(t, err, contracts.InvalidState)
			} else if err != nil {
				t.Fatal(err)
			}
			entropy.fail.Store(false)
			switch condition {
			case "different kind":
				corruptionMode(t, store)
				execSQL(t, store.db, `UPDATE operations SET kind='CreateCollection' WHERE operation_id='replay-rerun'`) // Preserve its fingerprint to isolate the kind condition.
			case "different fingerprint":
				request.Message = "different public payload"
			case "new session":
				service = productService(t, store, entropy, func() time.Time { return storageNow }, "session-two", nil)
				request.Context.BackendSessionID = "session-two"
			}
			before := corruptionDurableSnapshot(t, store)
			beforeEntropy := entropy.calls.Load()
			replay, err := service.Rerun(context.Background(), request)
			switch condition {
			case "different kind", "different fingerprint":
				if !errors.Is(err, rl.ErrOperationConflict) || !reflect.DeepEqual(replay, rl.RoundRecord{}) {
					t.Fatal(replay, err)
				}
			case "failed replay":
				requireFault(t, err, contracts.InvalidState)
				if !reflect.DeepEqual(replay, rl.RoundRecord{}) {
					t.Fatal("failed command publicly returned a round", replay)
				}
			default:
				if err != nil || !reflect.DeepEqual(replay, saved) {
					t.Fatal("replay lost committed result", replay, saved, err)
				}
			}
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != beforeEntropy {
				t.Fatal("replay changed SQL or entropy")
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}

func TestRoundRetryContextCollectionRevisionAndVersionRejectIndependently(t *testing.T) {
	for _, condition := range []string{"collection mismatch", "revision mismatch", "version mismatch"} {
		t.Run(condition, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			entropy.fail.Store(true)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			failed, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ImmediateMode))
			requireFault(t, err, contracts.InvalidState)
			entropy.fail.Store(false)
			c := productRoundContext(failed, "session-one")
			if condition == "collection mismatch" {
				request := productRequest(storageNow, rl.ImmediateMode)
				request.OperationID = "other-create"
				request.Context.DraftID = "other-draft"
				request.Collection.SourceDraftID = "other-draft"
				entropy.fail.Store(true)
				other, err := service.CreateCollection(context.Background(), request)
				requireFault(t, err, contracts.InvalidState)
				entropy.fail.Store(false)
				c.RoundID = other.ID
				c.Revision = other.Revision
				c.Version = other.Version // Both owners are failed; only collection ownership differs.
			}
			if condition == "revision mismatch" {
				c.Revision++
			}
			if condition == "version mismatch" {
				c.Version++
			}
			before := corruptionDurableSnapshot(t, store)
			calls := entropy.calls.Load()
			result, err := service.RetryRound(context.Background(), rl.RetryRequest{OperationID: contracts.OperationID(fmt.Sprintf("denied-%s", condition)), Context: c})
			requireFault(t, err, contracts.StaleRevision)
			if !reflect.DeepEqual(result, rl.RoundRecord{}) || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != calls {
				t.Fatal("mismatched retry changed immutable attempt", result, err)
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}
