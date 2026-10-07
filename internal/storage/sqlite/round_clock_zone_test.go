package sqlite

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func TestRoundClockLocationChangesNeverRewriteAbsoluteScheduleOrRepeatCompletedExecution(t *testing.T) {
	store := migratedTestStore(t)
	now := storageNow
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return now }, "session-one", nil)
	ctx := context.Background()
	pending, err := service.CreateCollection(ctx, productRequest(now, rl.ReservationMode))
	if err != nil {
		t.Fatal(err)
	}
	seconds := uint32(30)
	scheduled, err := service.SetSchedule(ctx, rl.SetScheduleRequest{OperationID: "zone-schedule", Context: productRoundContext(pending, "session-one"), QuickDelaySeconds: &seconds, Timezone: "Asia/Seoul"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := *scheduled.ScheduledAt
	original := corruptionDurableSnapshot(t, store)
	for _, entry := range []struct {
		instant time.Time
		zone    int
	}{{now, -5 * 3600}, {deadline.Add(-time.Nanosecond), 13 * 3600}} {
		now = entry.instant.In(time.FixedZone("changed-user-clock-location", entry.zone))
		early, err := service.ExecuteDue(ctx, rl.ExecuteDueRequest{RoundID: scheduled.ID})
		if err != nil || !reflect.DeepEqual(early, scheduled) || entropy.calls.Load() != 0 || corruptionDurableSnapshot(t, store) != original {
			t.Fatal("timezone changed absolute deadline or state", early, err)
		}
	}
	now = deadline.In(time.FixedZone("another-user-clock-location", -4*3600))
	completed, err := service.ExecuteDue(ctx, rl.ExecuteDueRequest{RoundID: scheduled.ID})
	if err != nil || completed.State != contracts.Completed || completed.Timezone != "Asia/Seoul" || !completed.ScheduledAt.Equal(deadline) || completed.ScheduledAt.Location() != time.UTC || completed.Outcome == nil || !completed.Outcome.ExecutedAt.Equal(deadline) || completed.Outcome.ExecutedAt.Location() != time.UTC || entropy.calls.Load() != 1 {
		t.Fatal("UTC execution changed frozen timezone/deadline", completed, err)
	}
	final := corruptionDurableSnapshot(t, store)
	now = deadline.Add(-time.Hour).In(time.FixedZone("backward-and-zone-changed", 9*3600))
	replay, err := service.ExecuteDue(ctx, rl.ExecuteDueRequest{RoundID: scheduled.ID})
	if err != nil || !reflect.DeepEqual(replay, completed) || entropy.calls.Load() != 1 || corruptionDurableSnapshot(t, store) != final {
		t.Fatal("backward clock reran committed round", replay, err)
	}
	assertRoundSQLIntegrity(t, store)
	assertNoHandles(t, store)
}
