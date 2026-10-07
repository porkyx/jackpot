package sqlite

// Direct storage boundary regressions preserve state, ownership and retry semantics.
// These cover the storage seam independently of upstream service validation.
import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func TestStorageSetScheduleEachEntryGuardPreservesDurableStateAndOperationID(t *testing.T) {
	cases := []struct {
		name      string
		context   func() context.Context
		mutate    func(*rl.ScheduleRequest)
		cancelled bool
	}{
		{name: "nil context", context: func() context.Context { return nil }},
		{name: "cancelled context", context: func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}, cancelled: true},
		{name: "cancelled context wins over invalid request", context: func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}, mutate: func(r *rl.ScheduleRequest) { r.Operation.Kind = "CancelSchedule" }, cancelled: true},
		{name: "empty operation", mutate: func(r *rl.ScheduleRequest) { r.Operation.ID = "" }},
		{name: "wrong kind", mutate: func(r *rl.ScheduleRequest) { r.Operation.Kind = "CancelSchedule" }},
		{name: "empty collection", mutate: func(r *rl.ScheduleRequest) { r.CollectionID = "" }},
		{name: "empty round", mutate: func(r *rl.ScheduleRequest) { r.RoundID = "" }},
		{name: "wrong timezone", mutate: func(r *rl.ScheduleRequest) { r.Timezone = "UTC" }},
		{name: "zero acceptance", mutate: func(r *rl.ScheduleRequest) { r.AcceptedAt = time.Time{} }},
		{name: "zero schedule", mutate: func(r *rl.ScheduleRequest) { r.ScheduledAt = time.Time{} }},
		// Direct storage accepts a typed nonzero year-zero AcceptedAt. Keep
		// Before(AcceptedAt+10s) false so only ScheduledAt.IsZero rejects.
		{name: "zero schedule with independent minimum operand", mutate: func(r *rl.ScheduleRequest) {
			r.AcceptedAt = time.Date(0, 12, 31, 23, 59, 40, 0, time.UTC)
			r.ScheduledAt = time.Time{}
		}},
		{name: "one nanosecond before minimum", mutate: func(r *rl.ScheduleRequest) { r.ScheduledAt = r.AcceptedAt.Add(10*time.Second - time.Nanosecond) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			pending, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ReservationMode))
			if err != nil || pending.State != contracts.PendingSchedule {
				t.Fatal("fixture must be a pending reservation", pending, err)
			}
			healthy := rl.ScheduleRequest{
				Operation:    rl.OperationIdentity{ID: "same-storage-schedule", Kind: "SetSchedule", PublicFingerprint: [32]byte{1}},
				CollectionID: pending.CollectionID, RoundID: pending.ID, ExpectedRevision: pending.Revision, ExpectedVersion: pending.Version,
				AcceptedAt: storageNow, ScheduledAt: storageNow.Add(10 * time.Second), Timezone: "Asia/Seoul",
			}
			bad := healthy
			if tc.mutate != nil {
				tc.mutate(&bad)
			}
			ctx := context.Background()
			if tc.context != nil {
				ctx = tc.context()
			}
			before := corruptionDurableSnapshot(t, store)
			for repeat := 0; repeat < 2; repeat++ {
				got, failure := store.SetSchedule(ctx, bad)
				if tc.cancelled {
					if !errors.Is(failure, context.Canceled) {
						t.Fatal("cancelled context must preserve its error", failure)
					}
				} else {
					requireFault(t, failure, contracts.InvalidInput)
				}
				if !reflect.DeepEqual(got, rl.RoundRecord{}) {
					t.Fatal("failed entry returned a public round", got)
				}
				assertNoHandles(t, store) // Check before opening the snapshot transaction.
				if before != corruptionDurableSnapshot(t, store) {
					t.Fatal("failed entry changed durable rows or operation sequence")
				}
				op, failure := store.ReadOperation(context.Background(), healthy.Operation.ID)
				if failure != nil || op.Status != contracts.OperationUnknown {
					t.Fatal("failed entry consumed the reusable operation", op, failure)
				}
				if store.db.Stats().InUse != 0 || entropy.calls.Load() != 0 {
					t.Fatal("failed entry retained a DB connection or reached RNG")
				}
			}
			scheduled, err := store.SetSchedule(context.Background(), healthy)
			if err != nil || scheduled.State != contracts.Scheduled || scheduled.Revision != pending.Revision+1 || scheduled.Version != pending.Version+1 ||
				scheduled.ScheduledAt == nil || !scheduled.ScheduledAt.Equal(storageNow.Add(10*time.Second)) {
				t.Fatal("corrected same operation must accept the exact minimum once", scheduled, err)
			}
			assertNoHandles(t, store)
			after := corruptionDurableSnapshot(t, store)
			replay, err := store.SetSchedule(context.Background(), healthy)
			assertNoHandles(t, store)
			if err != nil || !reflect.DeepEqual(replay, scheduled) || after != corruptionDurableSnapshot(t, store) {
				t.Fatal("successful exact replay changed state or returned a different round", replay, err)
			}
			if store.db.Stats().InUse != 0 || entropy.calls.Load() != 0 {
				t.Fatal("successful entry/replay retained a connection or reached RNG")
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}

func TestStorageCancelScheduleEachEntryGuardPreservesDurableStateAndOperationID(t *testing.T) {
	cases := []struct {
		name      string
		context   func() context.Context
		mutate    func(*rl.CancelScheduleRequest)
		cancelled bool
	}{
		{name: "nil context", context: func() context.Context { return nil }},
		{name: "cancelled context", context: func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}, cancelled: true},
		{name: "cancelled context wins over invalid request", context: func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}, mutate: func(r *rl.CancelScheduleRequest) { r.Operation.Kind = "SetSchedule" }, cancelled: true},
		{name: "empty operation", mutate: func(r *rl.CancelScheduleRequest) { r.Operation.ID = "" }},
		{name: "wrong kind", mutate: func(r *rl.CancelScheduleRequest) { r.Operation.Kind = "SetSchedule" }},
		{name: "empty collection", mutate: func(r *rl.CancelScheduleRequest) { r.CollectionID = "" }},
		{name: "empty round", mutate: func(r *rl.CancelScheduleRequest) { r.RoundID = "" }},
		{name: "zero acceptance", mutate: func(r *rl.CancelScheduleRequest) { r.AcceptedAt = time.Time{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			pending, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ReservationMode))
			if err != nil || pending.State != contracts.PendingSchedule {
				t.Fatal("fixture must be a pending reservation", pending, err)
			}
			healthy := rl.CancelScheduleRequest{
				Operation:    rl.OperationIdentity{ID: "same-storage-cancel", Kind: "CancelSchedule", PublicFingerprint: [32]byte{2}},
				CollectionID: pending.CollectionID, RoundID: pending.ID, ExpectedRevision: pending.Revision, ExpectedVersion: pending.Version, AcceptedAt: storageNow,
			}
			bad := healthy
			if tc.mutate != nil {
				tc.mutate(&bad)
			}
			ctx := context.Background()
			if tc.context != nil {
				ctx = tc.context()
			}
			before := corruptionDurableSnapshot(t, store)
			for repeat := 0; repeat < 2; repeat++ {
				got, failure := store.CancelSchedule(ctx, bad)
				if tc.cancelled {
					if !errors.Is(failure, context.Canceled) {
						t.Fatal("cancelled context must preserve its error", failure)
					}
				} else {
					requireFault(t, failure, contracts.InvalidInput)
				}
				if !reflect.DeepEqual(got, rl.RoundRecord{}) {
					t.Fatal("failed entry returned a public round", got)
				}
				assertNoHandles(t, store) // Check before opening the snapshot transaction.
				if before != corruptionDurableSnapshot(t, store) {
					t.Fatal("failed entry changed durable rows or operation sequence")
				}
				op, failure := store.ReadOperation(context.Background(), healthy.Operation.ID)
				if failure != nil || op.Status != contracts.OperationUnknown {
					t.Fatal("failed entry consumed the reusable operation", op, failure)
				}
				if store.db.Stats().InUse != 0 || entropy.calls.Load() != 0 {
					t.Fatal("failed entry retained a DB connection or reached RNG")
				}
			}
			cancelled, err := store.CancelSchedule(context.Background(), healthy)
			if err != nil || cancelled.State != contracts.Cancelled || cancelled.Revision != pending.Revision+1 || cancelled.Version != pending.Version+1 || cancelled.Outcome != nil {
				t.Fatal("corrected same operation must cancel exactly once", cancelled, err)
			}
			assertNoHandles(t, store)
			after := corruptionDurableSnapshot(t, store)
			replay, err := store.CancelSchedule(context.Background(), healthy)
			assertNoHandles(t, store)
			if err != nil || !reflect.DeepEqual(replay, cancelled) || after != corruptionDurableSnapshot(t, store) {
				t.Fatal("successful exact replay changed state or returned a different round", replay, err)
			}
			if store.db.Stats().InUse != 0 || entropy.calls.Load() != 0 {
				t.Fatal("successful entry/replay retained a connection or reached RNG")
			}
			assertRoundSQLIntegrity(t, store)
			assertNoHandles(t, store)
		})
	}
}
