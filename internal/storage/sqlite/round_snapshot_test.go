package sqlite

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	driverSQLite "modernc.org/sqlite"
)

func TestCollectionSnapshotMatchesFullReadAndReturnsDetachedFrozenData(t *testing.T) {
	store := migratedTestStore(t)
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
	request := productRequest(storageNow, rl.ImmediateMode)
	request.Collection.Participants[0].Comments = []rl.CommentSnapshot{{ID: "comment-one", Kind: "text", Text: "original", MediaURLs: []string{"https://dccon.dcinside.com/a"}}}
	round, err := service.CreateCollection(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		round, err = service.Rerun(context.Background(), rl.RerunRequest{OperationID: contracts.OperationID(fmt.Sprintf("snapshot-rerun-%d", index)), Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: round.CollectionID, Revision: round.Revision}, Prizes: []rl.Prize{{ID: "prize", Count: 1}}, Mode: rl.ImmediateMode})
		if err != nil {
			t.Fatal(err)
		}
	}
	expected, rounds, revision, err := store.ReadCollectionState(context.Background(), round.CollectionID)
	if err != nil || len(rounds) != 3 {
		t.Fatal(rounds, err)
	}
	before := corruptionDurableSnapshot(t, store)
	snapshot, actualRevision, err := store.ReadCollectionSnapshot(context.Background(), round.CollectionID)
	if err != nil || actualRevision != revision || !reflect.DeepEqual(snapshot, expected) {
		t.Fatal(snapshot, actualRevision, err)
	}
	snapshot.Participants[0].Nickname = "changed"
	snapshot.Participants[0].Comments[0].Text = "changed"
	snapshot.Participants[0].Comments[0].MediaURLs[0] = "changed"

	again, actualRevision, err := store.ReadCollectionSnapshot(context.Background(), round.CollectionID)
	if err != nil || actualRevision != revision || !reflect.DeepEqual(again, expected) || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 3 {
		t.Fatal("snapshot read aliased or changed durable state", again, actualRevision, err)
	}
	assertRoundSQLIntegrity(t, store)
	assertNoHandles(t, store)
}

func TestCollectionSnapshotRejectsInvalidContextMissingAndClosedWithoutPartialData(t *testing.T) {
	for _, mode := range []string{"nil", "empty", "cancelled", "deadline", "missing", "closed"} {
		t.Run(mode, func(t *testing.T) {
			store := migratedTestStore(t)
			service := productService(t, store, new(productEntropy), func() time.Time { return storageNow }, "session-one", nil)
			round, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ReservationMode))
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			id := round.CollectionID
			var want error
			switch mode {
			case "nil":
				ctx = nil
			case "empty":
				id = ""
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Time{})
				defer cancel()
				want = context.DeadlineExceeded
			case "missing":
				id = "absent"
				want = rl.ErrNotFound
			case "closed":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, revision, err := store.ReadCollectionSnapshot(ctx, id)
			if err == nil || want != nil && !errors.Is(err, want) || revision != 0 || !reflect.DeepEqual(snapshot, rl.FrozenCollection{}) {
				t.Fatal("partial frozen read", snapshot, revision, err)
			}
			assertNoHandles(t, store)
		})
	}
}

func TestCollectionSnapshotFirstNthContinuousReadFailureAndCancellationReturnConnection(t *testing.T) {
	for _, test := range []struct {
		name, mode string
		at         int32
	}{{"first", "once", 1}, {"Nth after prior valid round", "once", 8}, {"continuous", "continuous", 1}, {"cancel after prior valid round", "cancel", 8}} {
		t.Run(test.name, func(t *testing.T) {
			registerCorruptionReadProbe(t)
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			round, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ImmediateMode))
			if err != nil {
				t.Fatal(err)
			}
			for index := 0; index < 2; index++ {
				round, err = service.Rerun(context.Background(), rl.RerunRequest{OperationID: contracts.OperationID(fmt.Sprintf("fault-rerun-%d", index)), Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: round.CollectionID, Revision: round.Revision}, Prizes: []rl.Prize{{ID: "prize", Count: 1}}, Mode: rl.ImmediateMode})
				if err != nil {
					t.Fatal(err)
				}
			}
			execSQL(t, store.db, `ALTER TABLE rounds RENAME TO snapshot_round_rows;
CREATE VIEW rounds AS SELECT collection_id,jackpot_corruption_read_probe(id) AS id,number,state,version,attempt,input_json,active_operation_id,scheduled_at,timezone,claim_json,failure_code FROM snapshot_round_rows`)
			before := corruptionDurableSnapshot(t, store)
			expected, expectedRevision, err := store.ReadCollectionSnapshot(context.Background(), round.CollectionID)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &corruptionReadProbe{at: test.at, mode: test.mode, cancel: cancel}
			activeCorruptionReadProbe.Store(probe)
			defer activeCorruptionReadProbe.Store(nil)
			snapshot, revision, err := store.ReadCollectionSnapshot(ctx, round.CollectionID)
			activeCorruptionReadProbe.Store(nil)
			if err == nil || probe.calls.Load() < test.at || revision != 0 || !reflect.DeepEqual(snapshot, rl.FrozenCollection{}) {
				t.Fatal("read failure published partial snapshot", snapshot, revision, err, probe.calls.Load())
			}
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 3 {
				t.Fatal("snapshot read fault changed durable state")
			}
			assertNoHandles(t, store)
			snapshot, revision, err = store.ReadCollectionSnapshot(context.Background(), round.CollectionID)
			if err != nil || revision != expectedRevision || !reflect.DeepEqual(snapshot, expected) {
				t.Fatal("connection did not recover", snapshot, revision, err)
			}
			assertNoHandles(t, store)
		})
	}
}

type snapshotBarrier struct {
	entered, release chan struct{}
	once             sync.Once
	ctx              context.Context
}

var snapshotBarrierRegistration sync.Once
var snapshotBarrierRegistrationError error
var activeSnapshotBarrier atomic.Pointer[snapshotBarrier]

func registerSnapshotBarrier(t *testing.T) {
	t.Helper()
	snapshotBarrierRegistration.Do(func() {
		snapshotBarrierRegistrationError = driverSQLite.RegisterScalarFunction("jackpot_snapshot_barrier", 1, func(_ *driverSQLite.FunctionContext, args []driver.Value) (driver.Value, error) {
			barrier := activeSnapshotBarrier.Load()
			if barrier != nil {
				barrier.once.Do(func() { close(barrier.entered) })
				select {
				case <-barrier.release:
				case <-barrier.ctx.Done():
					return nil, barrier.ctx.Err()
				}
			}
			return args[0], nil
		})
	})
	if snapshotBarrierRegistrationError != nil {
		t.Fatal(snapshotBarrierRegistrationError)
	}
}
func TestCollectionSnapshotKeepsFrozenAndRevisionInSameReadTransactionDuringConcurrentCommit(t *testing.T) {
	registerSnapshotBarrier(t)
	store := migratedTestStore(t)
	service := productService(t, store, new(productEntropy), func() time.Time { return storageNow }, "session-one", nil)
	round, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ReservationMode))
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, store.db, `ALTER TABLE participants RENAME TO snapshot_participant_rows;
CREATE VIEW participants AS SELECT collection_id,id,position,included,jackpot_snapshot_barrier(body_json) AS body_json FROM snapshot_participant_rows`)
	writer, err := Open(context.Background(), store.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	barrier := &snapshotBarrier{entered: make(chan struct{}), release: make(chan struct{}), ctx: ctx}
	activeSnapshotBarrier.Store(barrier)
	defer activeSnapshotBarrier.Store(nil)
	var release sync.Once
	defer release.Do(func() { close(barrier.release) })
	type answer struct {
		snapshot rl.FrozenCollection
		revision contracts.Revision
		err      error
	}
	done := make(chan answer, 1)
	returned := false
	defer func() {
		release.Do(func() { close(barrier.release) })
		cancel()
		if !returned {
			cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			select {
			case <-done:
			case <-cleanupContext.Done():
				t.Error("snapshot goroutine cleanup did not finish")
			}
		}
	}()
	go func() {
		snapshot, revision, err := store.ReadCollectionSnapshot(ctx, round.CollectionID)
		done <- answer{snapshot, revision, err}
	}()
	select {
	case <-barrier.entered:
	case <-ctx.Done():
		t.Fatal("snapshot did not reach SQLite barrier", ctx.Err())
	}
	scheduled, err := writer.SetSchedule(ctx, rl.ScheduleRequest{Operation: rl.OperationIdentity{ID: "concurrent-schedule", Kind: "SetSchedule"}, CollectionID: round.CollectionID, RoundID: round.ID, ExpectedRevision: round.Revision, ExpectedVersion: round.Version, AcceptedAt: storageNow, ScheduledAt: storageNow.Add(30 * time.Second), Timezone: "Asia/Seoul"})
	if err != nil || scheduled.Revision != round.Revision+1 {
		t.Fatal("concurrent actual writer failed", scheduled, err)
	}
	release.Do(func() { close(barrier.release) })
	var result answer
	select {
	case result = <-done:
		returned = true
	case <-ctx.Done():
		t.Fatal("snapshot goroutine did not return", ctx.Err())
	}
	activeSnapshotBarrier.Store(nil)
	if result.err != nil || result.revision != round.Revision || result.snapshot.ID != round.CollectionID || len(result.snapshot.Participants) != 3 {
		t.Fatal("read mixed concurrent revision", result)
	}
	fresh, revision, err := store.ReadCollectionSnapshot(ctx, round.CollectionID)
	if err != nil || revision != scheduled.Revision || !reflect.DeepEqual(fresh, result.snapshot) {
		t.Fatal("fresh read lost committed revision", fresh, revision, err)
	}
	assertNoHandles(t, store)
	assertNoHandles(t, writer)
}
