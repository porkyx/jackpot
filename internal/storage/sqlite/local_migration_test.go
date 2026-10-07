package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

// This is a test-only v2 database created with the exact retained v1/v2 DDL.
// Its obsolete verifier is copied from the old fixture format; new product
// admission never creates or supplies a verifier.
func localLegacyV2(t *testing.T) (*Store, rl.CreateCollectionRequest, rl.RoundRecord) {
	t.Helper()
	current := migratedTestStore(t)
	request := productRequest(storageNow, rl.ImmediateMode)
	service := productService(t, current, new(productEntropy), func() time.Time { return storageNow }, "session-one", nil)
	completed, err := service.CreateCollection(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	legacy := newTestStore(t)
	for _, step := range append(append([]string{}, migrationOne...), migrationTwo...) {
		execSQL(t, legacy.db, step)
	}
	execSQL(t, legacy.db, "ATTACH DATABASE ? AS prior_source", current.path)
	tx, err := legacy.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	key := sha256.Sum256([]byte("legacy-test-fixture"))
	obsolete := struct {
		Algorithm        string
		Version, N, R, P uint32
		Salt             [16]byte
		Key              [32]byte
	}{"scrypt", 1, 131072, 8, 1, [16]byte{}, key}
	if _, err = tx.Exec("INSERT INTO collections(id,source_draft_id,revision,frozen_json,verifier_json,created_at) SELECT id,source_draft_id,revision,frozen_json,?,created_at FROM prior_source.collections", jsonValue(t, obsolete)); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"participants", "operations", "rounds", "round_candidates", "round_prizes", "round_attempts", "results", "winners"} {
		if _, err = tx.Exec("INSERT INTO " + table + " SELECT * FROM prior_source." + table); err != nil {
			t.Fatal(table, err)
		}
	}
	if _, err = tx.Exec("PRAGMA user_version=2"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	execSQL(t, legacy.db, "DETACH DATABASE prior_source")
	assertRoundSQLIntegrity(t, legacy)
	assertNoHandles(t, legacy)
	return legacy, request, completed
}

func localSchemaVersion(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != want {
		t.Fatal("schema version", version, want, err)
	}
}
func localVerifierColumns(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM pragma_table_info('collections') WHERE name='verifier_json'").Scan(&count); err != nil || count != want {
		t.Fatal("verifier column", count, want, err)
	}
}
func localLegacyPublicBytes(t *testing.T, legacy *Store) string {
	t.Helper()
	var rows [][][]any
	if err := json.Unmarshal([]byte(corruptionDurableSnapshot(t, legacy)), &rows); err != nil {
		t.Fatal(err)
	}
	for i, row := range rows[0] {
		if len(row) != 6 {
			t.Fatal("expected exact v2 collections column count", len(row))
		}
		rows[0][i] = append(row[:4:4], row[5:]...)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func localOpenBackup(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", fileURI(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	localSchemaVersion(t, db, 2)
	localVerifierColumns(t, db, 1)
	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil || result != "ok" {
		t.Fatal(result, err)
	}
	return db
}

func TestLocalMigrationV2PreservesPublicBytesResultsReplayAndRestartCommands(t *testing.T) {
	legacy, request, completed := localLegacyV2(t)
	before := localLegacyPublicBytes(t, legacy)
	backup, err := legacy.Migrate(context.Background())
	if err != nil || backup == "" {
		t.Fatal(backup, err)
	}
	localSchemaVersion(t, legacy.db, 3)
	localVerifierColumns(t, legacy.db, 0)
	if corruptionDurableSnapshot(t, legacy) != before {
		t.Fatal("migration changed durable public rows")
	}
	saved := localOpenBackup(t, backup)
	if err := saved.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, backup+".verified"); err != nil {
		t.Fatal("backup handle leaked", err)
	}
	assertRoundSQLIntegrity(t, legacy)
	assertNoHandles(t, legacy)
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), legacy.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	againBackup, err := reopened.Migrate(context.Background())
	if err != nil || againBackup != "" {
		t.Fatal("idempotent migration", againBackup, err)
	}
	localVerifierColumns(t, reopened.db, 0)
	now := storageNow
	entropy := new(productEntropy)
	restarted := productService(t, reopened, entropy, func() time.Time { return now }, "session-two", nil)
	request.Context.BackendSessionID = "session-two"
	replay, err := restarted.CreateCollection(context.Background(), request)
	if err != nil || !reflect.DeepEqual(replay, completed) || entropy.calls.Load() != 0 || corruptionDurableSnapshot(t, reopened) != before {
		t.Fatal("legacy operation replay changed result/state", err)
	}
	next, err := restarted.Rerun(context.Background(), rl.RerunRequest{OperationID: "local-rerun", Context: contracts.CollectionContext{BackendSessionID: "session-two", CollectionID: completed.CollectionID, Revision: completed.Revision}, Prizes: []rl.Prize{{ID: "local-gift", Count: 1}}, Mode: rl.ImmediateMode})
	if err != nil || next.State != contracts.Completed || len(next.Input.CandidateIDs) != 2 || entropy.calls.Load() != 1 || next.Outcome.Winners[0].ParticipantID != "p2" {
		t.Fatal(next, err)
	}
	reservation := productRequest(now, rl.ReservationMode)
	reservation.OperationID = "local-reservation"
	reservation.Context.BackendSessionID = "session-two"
	reservation.Context.DraftID = "new-local-draft"
	reservation.Collection.SourceDraftID = reservation.Context.DraftID
	pending, err := restarted.CreateCollection(context.Background(), reservation)
	if err != nil || pending.State != contracts.PendingSchedule || entropy.calls.Load() != 1 {
		t.Fatal(pending, err)
	}
	quick := uint32(30)
	scheduled, err := restarted.SetSchedule(context.Background(), rl.SetScheduleRequest{OperationID: "local-schedule", Context: productRoundContext(pending, "session-two"), QuickDelaySeconds: &quick, Timezone: "Asia/Seoul"})
	if err != nil || scheduled.State != contracts.Scheduled {
		t.Fatal(scheduled, err)
	}
	if err := restarted.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	fresh := productService(t, reopened, entropy, func() time.Time { return now }, "session-three", nil)
	early, err := fresh.Recover(context.Background(), rl.RecoverRequest{})
	if err != nil || len(early.Completed) != 0 || entropy.calls.Load() != 1 {
		t.Fatal(early, err)
	}
	now = now.Add(30 * time.Second)
	due, err := fresh.Recover(context.Background(), rl.RecoverRequest{})
	if err != nil || len(due.Completed) != 1 || due.Completed[0] != scheduled.ID || entropy.calls.Load() != 2 {
		t.Fatal(due, err)
	}
	current, err := reopened.ReadRound(context.Background(), scheduled.ID)
	if err != nil || current.State != contracts.Completed || current.Outcome == nil || current.Outcome.Winners[0].ParticipantID != "p1" {
		t.Fatal(current, err)
	}
	prior, err := reopened.ReadRound(context.Background(), completed.ID)
	if err != nil || !reflect.DeepEqual(prior.Outcome, completed.Outcome) || prior.State != contracts.Completed {
		t.Fatal("prior immutable result changed", err)
	}
	_, err = fresh.Rerun(context.Background(), rl.RerunRequest{OperationID: "stale-local", Context: contracts.CollectionContext{BackendSessionID: "session-three", CollectionID: current.CollectionID, Revision: current.Revision - 1}, Prizes: []rl.Prize{{ID: "p", Count: 1}}, Mode: rl.ImmediateMode})
	requireFault(t, err, contracts.StaleRevision)
	localVerifierColumns(t, reopened.db, 0)
	assertRoundSQLIntegrity(t, reopened)
	assertNoHandles(t, reopened)
}

func TestLocalMigrationEveryDDLFailureRollsBackVerifierAndImmutableTriggerThenRetries(t *testing.T) {
	for index := range migrationThree {
		t.Run(fmt.Sprintf("step-%d", index+1), func(t *testing.T) {
			legacy, _, _ := localLegacyV2(t)
			before := corruptionDurableSnapshot(t, legacy)
			steps := append([]string{}, migrationThree...)
			steps[index] = "INVALID LOCAL MIGRATION SQL"
			for attempt := 0; attempt < 2; attempt++ {
				backup, err := legacy.migrate(context.Background(), steps)
				if err == nil || backup == "" {
					t.Fatal("failure hidden", backup, err)
				}
				if corruptionDurableSnapshot(t, legacy) != before {
					t.Fatal("partial migration changed durable rows")
				}
				localSchemaVersion(t, legacy.db, 2)
				localVerifierColumns(t, legacy.db, 1)
				var triggers int
				if err := legacy.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name='immutable_collection' AND instr(sql,'verifier_json')>0").Scan(&triggers); err != nil || triggers != 1 {
					t.Fatal("old immutability lost", triggers, err)
				}
				saved := localOpenBackup(t, backup)
				if err := saved.Close(); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(backup, backup+".verified"); err != nil {
					t.Fatal("backup handle leaked", err)
				}
				assertRoundSQLIntegrity(t, legacy)
				assertNoHandles(t, legacy)
			}
			if _, err := legacy.Migrate(context.Background()); err != nil {
				t.Fatal("safe migration retry", err)
			}
			localSchemaVersion(t, legacy.db, 3)
			localVerifierColumns(t, legacy.db, 0)
			rejectedSQL(t, legacy, "UPDATE collections SET frozen_json='{}'")
			assertRoundSQLIntegrity(t, legacy)
			assertNoHandles(t, legacy)
		})
	}
}

func TestLocalMigrationDiscardsObsoleteVerifierFormatWithoutTouchingResults(t *testing.T) {
	for _, obsolete := range []string{"{}", "null", "[]", `{"Algorithm":"unsupported","N":1}`} {
		t.Run(obsolete, func(t *testing.T) {
			legacy, _, completed := localLegacyV2(t)
			execSQL(t, legacy.db, "DROP TRIGGER immutable_collection")
			execSQL(t, legacy.db, "UPDATE collections SET verifier_json=?", obsolete)
			execSQL(t, legacy.db, migrationTwo[2])
			before := localLegacyPublicBytes(t, legacy)
			if _, err := legacy.Migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			if corruptionDurableSnapshot(t, legacy) != before {
				t.Fatal("obsolete credential format changed public state")
			}
			read, err := legacy.ReadRound(context.Background(), completed.ID)
			if err != nil || !reflect.DeepEqual(read.Outcome, completed.Outcome) {
				t.Fatal("old result lost", err)
			}
			localVerifierColumns(t, legacy.db, 0)
			assertRoundSQLIntegrity(t, legacy)
			assertNoHandles(t, legacy)
		})
	}
}
