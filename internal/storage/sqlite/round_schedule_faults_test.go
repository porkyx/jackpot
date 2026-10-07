package sqlite

import (
	"context"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func TestRoundScheduleAndCancelEveryWriteAndDeferredCommitFailureRollsBackAndReplaysAfterRetry(t *testing.T) {
	for _, command := range []string{"set", "cancel-pending", "cancel-scheduled"} {
		for _, stage := range []string{"revision", "operation", "round", "commit"} {
			t.Run(command+"/"+stage, func(t *testing.T) {
				store := migratedTestStore(t)
				now := storageNow
				entropy := new(productEntropy)
				var notices atomic.Int32
				service := productService(t, store, entropy, func() time.Time { return now }, "session-one", func(context.Context, contracts.StateNotice) error { notices.Add(1); return nil })
				pending, err := service.CreateCollection(context.Background(), productRequest(now, rl.ReservationMode))
				if err != nil {
					t.Fatal(err)
				}
				initial := pending
				delay := uint32(60)
				if command == "cancel-scheduled" {
					initial, err = service.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: "baseline-set", Context: productRoundContext(pending, "session-one"), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
					if err != nil {
						t.Fatal(err)
					}
				}
				operationID := contracts.OperationID("fault-transition")
				var trigger string
				switch stage {
				case "revision":
					trigger = "CREATE TRIGGER transition_write_fault BEFORE UPDATE OF revision ON collections BEGIN SELECT RAISE(ABORT,'first transition write'); END"
				case "operation":
					trigger = "CREATE TRIGGER transition_write_fault BEFORE INSERT ON operations WHEN NEW.operation_id='fault-transition' BEGIN SELECT RAISE(ABORT,'second transition write'); END"
				case "round":
					trigger = "CREATE TRIGGER transition_write_fault BEFORE UPDATE ON rounds BEGIN SELECT RAISE(ABORT,'third transition write'); END"
				case "commit":
					trigger = "CREATE TRIGGER transition_write_fault AFTER INSERT ON operations WHEN NEW.operation_id='fault-transition' BEGIN INSERT INTO operations(operation_id,kind,collection_id,round_id,status,revision,public_fingerprint) VALUES ('deferred-orphan','SetSchedule',NEW.collection_id,'missing-round','succeeded',NEW.revision,zeroblob(32)); END"
				default:
					panic("Unknown transition write fault stage")
				}
				execSQL(t, store.db, trigger)
				before := corruptionDurableSnapshot(t, store)
				beforeNotices := notices.Load()
				invoke := func() (rl.RoundRecord, error) {
					if command == "set" {
						return service.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: operationID, Context: productRoundContext(initial, "session-one"), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"})
					}
					return service.CancelSchedule(context.Background(), rl.CancelRequest{OperationID: operationID, Context: productRoundContext(initial, "session-one")})
				}
				for attempt := 1; attempt <= 3; attempt++ {
					result, err := invoke()
					requireFault(t, err, contracts.StorageUnavailable)
					if !reflect.DeepEqual(result, rl.RoundRecord{}) || corruptionDurableSnapshot(t, store) != before || notices.Load() != beforeNotices || entropy.calls.Load() != 0 {
						t.Fatal("transition failure published or committed partial state", attempt, result)
					}
					operation, err := store.ReadOperation(context.Background(), operationID)
					if err != nil || operation.Status != contracts.OperationUnknown {
						t.Fatal(operation, err)
					}
					reopen, err := Open(context.Background(), store.path)
					if err != nil {
						t.Fatal(err)
					}
					func() {
						defer func() {
							if err := reopen.Close(); err != nil {
								t.Error(err)
							}
						}()
						actual, err := reopen.ReadRound(context.Background(), initial.ID)
						if err != nil || !reflect.DeepEqual(actual, initial) || corruptionDurableSnapshot(t, reopen) != before {
							t.Fatal("rollback differs after actual reopen", actual, err)
						}
						assertRoundSQLIntegrity(t, reopen)
						assertNoHandles(t, reopen)
					}()
					assertRoundSQLIntegrity(t, store)
					assertNoHandles(t, store)
				}
				execSQL(t, store.db, "DROP TRIGGER transition_write_fault")
				result, err := invoke()
				expectedState := contracts.Cancelled
				if command == "set" {
					expectedState = contracts.Scheduled
				}
				if err != nil || result.State != expectedState || result.Revision != initial.Revision+1 || result.Version != initial.Version+1 || result.Outcome != nil || notices.Load() != beforeNotices+1 || entropy.calls.Load() != 0 {
					t.Fatal("same operation safe retry after storage failure", result, err)
				}
				if result.State == contracts.Scheduled && (result.ScheduledAt == nil || !result.ScheduledAt.Equal(now.Add(60*time.Second))) {
					t.Fatal("retry stored incorrect deadline", result)
				}
				if command == "cancel-scheduled" && (result.ScheduledAt == nil || !result.ScheduledAt.Equal(*initial.ScheduledAt) || result.Timezone != initial.Timezone) {
					t.Fatal("cancellation replaced historical deadline", result)
				}
				durable := corruptionDurableSnapshot(t, store)
				replay, err := invoke()
				if err != nil || !reflect.DeepEqual(replay, result) || corruptionDurableSnapshot(t, store) != durable || notices.Load() != beforeNotices+1 || entropy.calls.Load() != 0 {
					t.Fatal("transition replay changed committed state", fmt.Sprint(replay), err)
				}
				assertRoundSQLIntegrity(t, store)
				assertNoHandles(t, store)
			})
		}
	}
}
