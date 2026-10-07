package sqlite

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

// These fixtures contain two collections or rounds, never an aggregate overflow
// workload. A read-only view changes only one driver value after a healthy row.
func remainingReadScanError(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "Scan error on column index") {
		t.Fatalf("missing exact database/sql Scan failure: %v", err)
	}
}

func remainingReadCloseAndRename(t *testing.T, store *Store) {
	t.Helper()
	assertNoHandles(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(store.path, store.path+".released"); err != nil {
		t.Fatal("failed read retained the owned database file", err)
	}
}

func TestHistoryReadRevisionScanAfterHealthyRowNeverPublishesPartialPage(t *testing.T) {
	for _, method := range []string{"ListCollections", "ListCollectionPage"} {
		t.Run(method, func(t *testing.T) {
			store := migratedTestStore(t)
			entropy := new(productEntropy)
			service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
			for index := 0; index < 2; index++ {
				request := productRequest(storageNow, rl.ReservationMode)
				request.OperationID = contracts.OperationID(fmt.Sprintf("read-scan-create-%d", index))
				request.Context.DraftID = contracts.DraftID(fmt.Sprintf("read-scan-draft-%d", index))
				request.Collection.SourceDraftID = request.Context.DraftID
				if _, err := service.CreateCollection(context.Background(), request); err != nil {
					t.Fatal(err)
				}
			}
			query := func() ([]rl.CollectionRecord, uint32, error) {
				if method == "ListCollections" {
					records, err := store.ListCollections(context.Background(), 0, 100)
					return records, 0, err
				}
				return store.ListCollectionPage(context.Background(), 0, 100, "", "all")
			}
			healthy, total, err := query()
			if err != nil || len(healthy) != 2 || (method == "ListCollectionPage" && total != 2) {
				t.Fatal("healthy history fixture", healthy, total, err)
			}
			// Descending created_at/id order returns the healthy row before this one.
			execSQL(t, store.db, fmt.Sprintf(
				"ALTER TABLE collections RENAME TO remaining_history_rows; CREATE VIEW collections AS SELECT id,source_draft_id,CASE WHEN id='%s' THEN -1 ELSE revision END AS revision,frozen_json,created_at FROM remaining_history_rows",
				healthy[1].ID))
			before := corruptionDurableSnapshot(t, store)
			for repeat := 0; repeat < 3; repeat++ {
				partial, partialTotal, err := query()
				remainingReadScanError(t, err)
				if partial != nil || partialTotal != 0 || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 0 {
					t.Fatal("history Scan error published partial values or wrote state", partial, partialTotal)
				}
				assertNoHandles(t, store)
			}
			execSQL(t, store.db, "DROP VIEW collections; ALTER TABLE remaining_history_rows RENAME TO collections")
			recovered, recoveredTotal, err := query()
			if err != nil || recoveredTotal != total || !reflect.DeepEqual(recovered, healthy) {
				t.Fatal("restored exact history could not be retried", recovered, recoveredTotal, err)
			}
			remainingReadCloseAndRename(t, store)
		})
	}
}

func TestRoundWinnerSlotScanAfterHealthyWinnerReturnsNoRoundAndPreservesResult(t *testing.T) {
	store := migratedTestStore(t)
	entropy := new(productEntropy)
	service := productService(t, store, entropy, func() time.Time { return storageNow }, "session-one", nil)
	request := productRequest(storageNow, rl.ImmediateMode)
	request.Input.Prizes[0].Count = 2
	expected, err := service.CreateCollection(context.Background(), request)
	if err != nil || expected.Outcome == nil || len(expected.Outcome.Winners) != 2 || entropy.calls.Load() != 2 {
		t.Fatal("two-winner fixture", expected, err)
	}
	execSQL(t, store.db, "ALTER TABLE winners RENAME TO remaining_winner_rows; CREATE VIEW winners AS SELECT collection_id,round_id,participant_id,prize_id,CASE WHEN slot=2 THEN 'not-a-slot' ELSE slot END AS slot FROM remaining_winner_rows")
	// INTEGER slot 1 sorts before the TEXT fault, so the first winner was healthy.
	var first int
	if err := store.db.QueryRow("SELECT slot FROM winners ORDER BY slot LIMIT 1").Scan(&first); err != nil || first != 1 {
		t.Fatal("fault must follow the healthy winner", first, err)
	}
	before := corruptionDurableSnapshot(t, store)
	for repeat := 0; repeat < 3; repeat++ {
		partial, err := store.ReadRound(context.Background(), expected.ID)
		remainingReadScanError(t, err)
		if !reflect.DeepEqual(partial, rl.RoundRecord{}) || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 2 {
			t.Fatal("winner Scan error published a partial outcome or changed rows/RNG", partial)
		}
		assertNoHandles(t, store)
	}
	execSQL(t, store.db, "DROP VIEW winners; ALTER TABLE remaining_winner_rows RENAME TO winners")
	recovered, err := store.ReadRound(context.Background(), expected.ID)
	if err != nil || !reflect.DeepEqual(recovered, expected) {
		t.Fatal("original durable result not recoverable", recovered, err)
	}
	remainingReadCloseAndRename(t, store)
}

func TestRoundIDNullScanAfterHealthyIDReturnsNoListAndPreservesHistory(t *testing.T) {
	store, _, latest, entropy := collectionPageCancelledFixture(t, 2)
	expected, err := store.ListRounds(context.Background(), latest.CollectionID)
	if err != nil || len(expected) != 2 {
		t.Fatal("two-round fixture", expected, err)
	}
	execSQL(t, store.db, "ALTER TABLE rounds RENAME TO remaining_round_rows; CREATE VIEW rounds AS SELECT collection_id,CASE WHEN number=2 THEN NULL ELSE id END AS id,number,state,version,attempt,input_json,active_operation_id,scheduled_at,timezone,claim_json,failure_code FROM remaining_round_rows")
	var first string
	if err := store.db.QueryRow("SELECT id FROM rounds ORDER BY number LIMIT 1").Scan(&first); err != nil || first != string(expected[0].ID) {
		t.Fatal("fault must follow healthy ID", first, err)
	}
	before := corruptionDurableSnapshot(t, store)
	for repeat := 0; repeat < 3; repeat++ {
		partial, err := store.ListRounds(context.Background(), latest.CollectionID)
		remainingReadScanError(t, err)
		if partial != nil || before != corruptionDurableSnapshot(t, store) || entropy.calls.Load() != 0 {
			t.Fatal("ID Scan error published a partial list or changed history", partial)
		}
		assertNoHandles(t, store)
	}
	execSQL(t, store.db, "DROP VIEW rounds; ALTER TABLE remaining_round_rows RENAME TO rounds")
	recovered, err := store.ListRounds(context.Background(), latest.CollectionID)
	if err != nil || !reflect.DeepEqual(recovered, expected) {
		t.Fatal("original rounds not recoverable", recovered, err)
	}
	remainingReadCloseAndRename(t, store)
}
