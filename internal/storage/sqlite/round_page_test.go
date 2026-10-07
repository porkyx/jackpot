package sqlite

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func appendCancelledPageRound(t *testing.T, service *rl.Service, previous rl.RoundRecord, number int) rl.RoundRecord {
	t.Helper()
	round := previous
	var err error
	if number > 1 {
		round, err = service.Rerun(context.Background(), rl.RerunRequest{OperationID: contracts.OperationID(fmt.Sprintf("page-rerun-%d", number)), Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: previous.CollectionID, Revision: previous.Revision}, Prizes: []rl.Prize{{ID: "page-prize", Count: 1}}, Mode: rl.ReservationMode})
		if err != nil {
			t.Fatal(err)
		}
	}
	round, err = service.CancelSchedule(context.Background(), rl.CancelRequest{OperationID: contracts.OperationID(fmt.Sprintf("page-cancel-%d", number)), Context: productRoundContext(round, "session-one")})
	if err != nil || round.Number != uint32(number) || round.State != contracts.Cancelled {
		t.Fatal("valid reservation/cancel page fixture", err)
	}
	return round
}
func collectionPageCancelledFixture(t *testing.T, count int) (*Store, *rl.Service, rl.RoundRecord, *productEntropy) {
	t.Helper()
	if count < 1 || count > 101 {
		t.Fatal("bounded page fixture required")
	}
	store := migratedTestStore(t)
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
	round, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ReservationMode))
	if err != nil {
		t.Fatal(err)
	}
	for number := 1; number <= count; number++ {
		round = appendCancelledPageRound(t, service, round, number)
	}
	return store, service, round, entropy
}

// A cancelled database/sql transaction can be released by its awaitDone worker.
// Acquiring the sole connection is a logical cleanup barrier, never a timed pass.
func awaitCollectionPageConnection(t *testing.T, store *Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal("page connection did not return", err)
	}
	if err = connection.Close(); err != nil {
		t.Fatal(err)
	}
	assertNoHandles(t, store)
}
func requireEmptyCollectionPage(t *testing.T, page rl.CollectionPage, err error) {
	t.Helper()
	if err == nil || !reflect.DeepEqual(page, rl.CollectionPage{}) {
		t.Fatal("failed page returned partial data", err)
	}
}
func assertPageNumbers(t *testing.T, page rl.CollectionPage, total, offset, first, count uint32, latest rl.RoundRecord) {
	t.Helper()
	if page.Total != total || page.Offset != offset || len(page.Rounds) != int(count) || page.Revision != latest.Revision || page.LatestRound.ID != latest.ID || page.LatestRound.Number != total {
		t.Fatal("page metadata/latest/count mismatch", page.Total, page.Offset, len(page.Rounds))
	}
	for index, round := range page.Rounds {
		if round.Number != first+uint32(index) || round.Revision != page.Revision || round.CollectionID != latest.CollectionID {
			t.Fatal("page order/snapshot mismatch")
		}
	}
}
func TestCollectionPageLiteralSizesAndNewestOffsetsBoundRetainedRounds(t *testing.T) {
	store, service, round, entropy := collectionPageCancelledFixture(t, 1)
	sizes := map[int]uint32{1: 1, 49: 1, 50: 1, 51: 2, 100: 51, 101: 52}
	var ids []contracts.RoundID
	for number := 1; number <= 101; number++ {
		if number > 1 {
			round = appendCancelledPageRound(t, service, round, number)
		}
		ids = append(ids, round.ID)
		if first, check := sizes[number]; check {
			page, err := store.ReadCollectionPage(context.Background(), round.CollectionID, rl.CollectionPageQuery{Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			count := uint32(number)
			if count > 50 {
				count = 50
			}
			assertPageNumbers(t, page, uint32(number), 0, first, count, round)
			if page.ConsumedCount != 0 {
				t.Fatal("cancellation consumed participant")
			}
		}
	}
	before := corruptionDurableSnapshot(t, store)
	for _, test := range []struct {
		name                        string
		limit, offset, first, count uint32
	}{
		{"newest50", 50, 0, 52, 50}, {"offset1", 50, 1, 51, 50}, {"offset49", 50, 49, 3, 50},
		{"older50", 50, 50, 2, 50}, {"older51", 50, 51, 1, 50}, {"last", 50, 100, 1, 1},
		{"beyond101", 50, 101, 0, 0}, {"offset-max", 50, ^uint32(0), 0, 0}, {"newest1", 1, 0, 101, 1}, {"newest49", 49, 0, 53, 49},
	} {
		t.Run(test.name, func(t *testing.T) {
			page, err := store.ReadCollectionPage(context.Background(), round.CollectionID, rl.CollectionPageQuery{Limit: test.limit, Offset: test.offset})
			if err != nil {
				t.Fatal(err)
			}
			assertPageNumbers(t, page, 101, test.offset, test.first, test.count, round)
			if page.Rounds == nil {
				t.Fatal("successful empty page must have [] rows")
			}
			for index, r := range page.Rounds {
				if r.ID != ids[int(test.first)-1+index] {
					t.Fatal("page contains wrong identity")
				}
			}
		})
	}
	if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 0 {
		t.Fatal("page read mutated DB or RNG")
	}
	assertRoundSQLIntegrity(t, store)
	assertNoHandles(t, store)
}
func TestCollectionPageAnchorResolvesOlderPageWithoutLosingLatest(t *testing.T) {
	store, _, latest, _ := collectionPageCancelledFixture(t, 51)
	rounds, err := store.ListRounds(context.Background(), latest.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	before := corruptionDurableSnapshot(t, store)
	for _, test := range []struct {
		name                        string
		index                       int
		limit, offset, first, count uint32
	}{
		{"latest", 50, 50, 0, 2, 50}, {"oldest", 0, 50, 50, 1, 1}, {"last-in-new-page", 1, 50, 0, 2, 50},
		{"single-anchor", 24, 1, 26, 25, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			page, err := store.ReadCollectionPage(context.Background(), latest.CollectionID, rl.CollectionPageQuery{Limit: test.limit, RoundID: rounds[test.index].ID})
			if err != nil {
				t.Fatal(err)
			}
			assertPageNumbers(t, page, 51, test.offset, test.first, test.count, latest)
		})
	}
	if before != corruptionDurableSnapshot(t, store) {
		t.Fatal("anchor read mutated durable state")
	}
	assertNoHandles(t, store)
}
func TestCollectionPageInvalidQueriesMissingAnchorsAndClosedStoreHaveZeroPayload(t *testing.T) {
	store, service, latest, _ := collectionPageCancelledFixture(t, 1)
	other := productRequest(storageNow, rl.ReservationMode)
	other.OperationID = "other-create"
	other.Context.DraftID = "other-draft"
	other.Collection.SourceDraftID = other.Context.DraftID
	foreign, err := service.CreateCollection(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	before := corruptionDurableSnapshot(t, store)
	for _, mode := range []string{"nil", "empty-id", "cancelled", "deadline", "missing-collection", "missing-anchor", "foreign-anchor", "zero-limit", "limit51", "limit-max", "anchor-offset"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			id := latest.CollectionID
			query := rl.CollectionPageQuery{Limit: 50}
			var want error
			switch mode {
			case "nil":
				ctx = nil
			case "empty-id":
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
			case "missing-collection":
				id = "absent"
				want = rl.ErrNotFound
			case "missing-anchor":
				query.RoundID = "absent"
				want = rl.ErrNotFound
			case "foreign-anchor":
				query.RoundID = foreign.ID
				want = rl.ErrNotFound
			case "zero-limit":
				query.Limit = 0
			case "limit51":
				query.Limit = 51
			case "limit-max":
				query.Limit = ^uint32(0)
			case "anchor-offset":
				query.RoundID = latest.ID
				query.Offset = 1
			}
			page, err := store.ReadCollectionPage(ctx, id, query)
			requireEmptyCollectionPage(t, page, err)
			if want != nil && !errors.Is(err, want) {
				t.Fatal("page error contract", err)
			}
			assertNoHandles(t, store)
		})
	}
	if before != corruptionDurableSnapshot(t, store) {
		t.Fatal("rejected query mutated durable state")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	page, err := store.ReadCollectionPage(context.Background(), latest.CollectionID, rl.CollectionPageQuery{Limit: 50})
	requireEmptyCollectionPage(t, page, err)
	assertNoHandles(t, store)
}
func TestCollectionPageEmptyDatabaseAndOrphanedCollectionRejectMissingLatest(t *testing.T) {
	store := migratedTestStore(t)
	page, err := store.ReadCollectionPage(context.Background(), "absent", rl.CollectionPageQuery{Limit: 50})
	requireEmptyCollectionPage(t, page, err)
	if !errors.Is(err, rl.ErrNotFound) {
		t.Fatal(err)
	}
	seedRound(t, store, "orphan", "orphan-round", "orphan-operation", "executing")
	corruptionMode(t, store)
	execSQL(t, store.db, "PRAGMA foreign_keys=OFF; DROP TRIGGER undeletable_round; DELETE FROM rounds WHERE collection_id='orphan'")
	before := corruptionDurableSnapshot(t, store)
	page, err = store.ReadCollectionPage(context.Background(), "orphan", rl.CollectionPageQuery{Limit: 50})
	requireEmptyCollectionPage(t, page, err)
	requireFault(t, err, contracts.InvalidState)
	if before != corruptionDurableSnapshot(t, store) {
		t.Fatal("empty round invariant repaired DB")
	}
	assertNoHandles(t, store)
}
func collectionPageCompletedFixture(t *testing.T) (*Store, *rl.Service, rl.RoundRecord, *productEntropy) {
	t.Helper()
	store := migratedTestStore(t)
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
	request := productRequest(storageNow, rl.ImmediateMode)
	request.Collection.Participants[0].Manual = &rl.ManualStateSnapshot{ManualIncluded: true}
	request.Collection.Participants[1].Manual = &rl.ManualStateSnapshot{ManualIncluded: true, OverrideExcluded: true}
	request.Collection.Participants[0].Comments = []rl.CommentSnapshot{{ID: "page-comment", Kind: "text", Text: "original", MediaURLs: []string{"https://dccon.dcinside.com/a"}}}
	round, err := service.CreateCollection(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for number := 2; number <= 3; number++ {
		round, err = service.Rerun(context.Background(), rl.RerunRequest{OperationID: contracts.OperationID(fmt.Sprintf("completed-page-%d", number)), Context: contracts.CollectionContext{BackendSessionID: "session-one", CollectionID: round.CollectionID, Revision: round.Revision}, Prizes: []rl.Prize{{ID: "next-prize", Count: 1}}, Mode: rl.ImmediateMode})
		if err != nil {
			t.Fatal(err)
		}
	}
	return store, service, round, entropy
}
func TestCollectionPageConsumedCountIncludesUnselectedRoundsAndRestoresManualSnapshotAfterReopen(t *testing.T) {
	store, service, latest, entropy := collectionPageCompletedFixture(t)
	before := corruptionDurableSnapshot(t, store)
	original, err := store.ReadCollectionPage(context.Background(), latest.CollectionID, rl.CollectionPageQuery{Limit: 1, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	assertPageNumbers(t, original, 3, 2, 1, 1, latest)
	if original.ConsumedCount != 3 || original.Frozen.Participants[0].Manual == nil || !original.Frozen.Participants[0].Manual.ManualIncluded || original.Frozen.Participants[1].Manual == nil || !original.Frozen.Participants[1].Manual.OverrideExcluded || original.Frozen.Participants[2].Manual != nil {
		t.Fatal("manual/legacy/consumed snapshot lost")
	}
	changed := original
	changed.Frozen.Participants[0].Manual.ManualIncluded = false
	changed.Frozen.Participants[0].Comments[0].Text = "changed"
	changed.Frozen.Participants[0].Comments[0].MediaURLs[0] = "changed"

	changed.Rounds[0].Input.CandidateIDs[0] = "changed"
	changed.LatestRound.Outcome.Winners[0].ParticipantID = "changed"
	again, err := store.ReadCollectionPage(context.Background(), latest.CollectionID, rl.CollectionPageQuery{Limit: 1, Offset: 2})
	if err != nil || !again.Frozen.Participants[0].Manual.ManualIncluded || again.Frozen.Participants[0].Comments[0].Text != "original" || again.ConsumedCount != 3 || again.LatestRound.Outcome.Winners[0].ParticipantID == "changed" || again.Rounds[0].Input.CandidateIDs[0] == "changed" || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 3 {
		t.Fatal("read graphs aliased or changed durable bytes", err)
	}
	assertNoHandles(t, store)
	if err = service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := store.path
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	restarted, err := reopened.ReadCollectionPage(context.Background(), latest.CollectionID, rl.CollectionPageQuery{Limit: 1, Offset: 2})
	if err != nil || !reflect.DeepEqual(restarted, again) || before != corruptionDurableSnapshot(t, reopened) {
		t.Fatal("reopened page/manual snapshot differs", err)
	}
	assertNoHandles(t, reopened)
}
func TestCollectionPageFirstNthContinuousAndCancelledSQLiteReadReturnNoPartialPage(t *testing.T) {
	for _, test := range []struct {
		name, mode string
		at         int32
	}{{"first", "once", 1}, {"Nth-after-retained-oldest", "once", 3}, {"continuous", "continuous", 1}, {"cancel-after-retained-oldest", "cancel", 3}} {
		t.Run(test.name, func(t *testing.T) {
			registerCorruptionReadProbe(t)
			store, _, latest, entropy := collectionPageCancelledFixture(t, 3)
			execSQL(t, store.db, `ALTER TABLE rounds RENAME TO page_round_rows; CREATE VIEW rounds AS SELECT collection_id,id,number,state,version,attempt,jackpot_corruption_read_probe(input_json) AS input_json,active_operation_id,scheduled_at,timezone,claim_json,failure_code FROM page_round_rows`)
			before := corruptionDurableSnapshot(t, store)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &corruptionReadProbe{at: test.at, mode: test.mode, cancel: cancel}
			activeCorruptionReadProbe.Store(probe)
			defer activeCorruptionReadProbe.Store(nil)
			for repetition := 0; repetition < 3; repetition++ {
				probe.calls.Store(0)
				page, err := store.ReadCollectionPage(ctx, latest.CollectionID, rl.CollectionPageQuery{Limit: 1, Offset: 2})
				requireEmptyCollectionPage(t, page, err)
				if repetition == 0 && probe.calls.Load() < test.at {
					t.Fatal("fault did not reach intended read")
				}
				if test.mode == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("SQLite cancellation error lost", err)
				}
				awaitCollectionPageConnection(t, store)
			}
			activeCorruptionReadProbe.Store(nil)
			if before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 0 {
				t.Fatal("read failure changed durable state or RNG")
			}
			healthy, err := store.ReadCollectionPage(context.Background(), latest.CollectionID, rl.CollectionPageQuery{Limit: 1, Offset: 2})
			if err != nil {
				t.Fatal("healthy retry after read fault", err)
			}
			assertPageNumbers(t, healthy, 3, 2, 1, 1, latest)
			assertNoHandles(t, store)
		})
	}
}
func TestCollectionPageAtomicTotalLatestAndRevisionDuringConcurrentAdmission(t *testing.T) {
	registerSnapshotBarrier(t)
	store, _, latest, _ := collectionPageCancelledFixture(t, 1)
	execSQL(t, store.db, `ALTER TABLE participants RENAME TO page_participant_rows; CREATE VIEW participants AS SELECT collection_id,id,position,included,jackpot_snapshot_barrier(body_json) AS body_json FROM page_participant_rows`)
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
		page rl.CollectionPage
		err  error
	}
	done := make(chan answer, 1)
	returned := false
	defer func() {
		release.Do(func() { close(barrier.release) })
		cancel()
		if !returned {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			select {
			case <-done:
			case <-cleanup.Done():
				t.Error("page worker cleanup exceeded deadline")
			}
		}
	}()
	go func() {
		page, err := store.ReadCollectionPage(ctx, latest.CollectionID, rl.CollectionPageQuery{Limit: 50})
		done <- answer{page, err}
	}()
	select {
	case <-barrier.entered:
	case <-ctx.Done():
		t.Fatal("page did not enter snapshot barrier", ctx.Err())
	}
	admission, err := writer.AdmitRound(ctx, rl.AdmitRoundRequest{Operation: rl.OperationIdentity{ID: "concurrent-page-admission", Kind: "Rerun"}, CollectionID: latest.CollectionID, RoundID: "concurrent-page-round", ExpectedRevision: latest.Revision, Number: 2, Attempt: 1, Input: rl.RoundInput{CandidateIDs: []contracts.ParticipantID{"p1", "p2", "p3"}, Prizes: []rl.Prize{{ID: "concurrent-prize", Count: 1}}, Mode: rl.ReservationMode}, InitialState: contracts.PendingSchedule, AdmittedAt: storageNow})
	if err != nil || admission.Round.Revision != latest.Revision+1 {
		t.Fatal("concurrent admission failed", err)
	}
	release.Do(func() { close(barrier.release) })
	var result answer
	select {
	case result = <-done:
		returned = true
	case <-ctx.Done():
		t.Fatal("page snapshot did not return", ctx.Err())
	}
	activeSnapshotBarrier.Store(nil)
	if result.err != nil {
		t.Fatal(result.err)
	}
	assertPageNumbers(t, result.page, 1, 0, 1, 1, latest)
	fresh, err := store.ReadCollectionPage(context.Background(), latest.CollectionID, rl.CollectionPageQuery{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	assertPageNumbers(t, fresh, 2, 0, 1, 2, admission.Round)
	if !reflect.DeepEqual(fresh.Frozen, result.page.Frozen) || fresh.LatestRound.State != contracts.PendingSchedule {
		t.Fatal("fresh page lost atomic committed snapshot")
	}
	assertNoHandles(t, store)
	assertNoHandles(t, writer)
}
