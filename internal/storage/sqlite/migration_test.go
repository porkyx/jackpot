package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "data.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}
func seedLegacy(t *testing.T, store *Store) {
	t.Helper()
	if _, err := store.db.Exec("CREATE TABLE legacy(value TEXT NOT NULL); INSERT INTO legacy VALUES ('preserved')"); err != nil {
		t.Fatal(err)
	}
}
func verifyLegacy(t *testing.T, db *sql.DB, wantVersion int) {
	t.Helper()
	var version int
	var value string
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT value FROM legacy").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if version != wantVersion || value != "preserved" {
		t.Fatalf("data/version changed: %q/%d", value, version)
	}
}
func verifyBackup(t *testing.T, path string) {
	t.Helper()
	backup, err := sql.Open("sqlite", fileURI(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	verifyLegacy(t, backup, 0)
	var integrity string
	if err := backup.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("backup failed independent integrity check: %q/%v", integrity, err)
	}
	var count int
	if err := backup.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='operations'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("backup contains migration changes")
	}
}

func TestMigrationVerifiedBackupIdempotencyReopenAndRestore(t *testing.T) {
	store := newTestStore(t)
	seedLegacy(t, store)
	backup, err := store.Migrate(context.Background())
	if err != nil || backup == "" {
		t.Fatalf("%q/%v", backup, err)
	}
	verifyBackup(t, backup)
	verifyLegacy(t, store.db, SchemaVersion)
	again, err := store.Migrate(context.Background())
	if err != nil || again != "" {
		t.Fatalf("repeated migration: %q/%v", again, err)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(store.path), "jackpot-backup-*.sqlite3"))
	if err != nil || len(files) != 1 {
		t.Fatalf("idempotency: %v/%v", files, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path, data, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(context.Background(), store.path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	verifyLegacy(t, restored.db, 0)
	newBackup, err := restored.Migrate(context.Background())
	if err != nil || newBackup == "" {
		t.Fatalf("restored retry: %q/%v", newBackup, err)
	}
	verifyLegacy(t, restored.db, SchemaVersion)
}

func TestMigrationFirstNthAndContinuousStepFailuresRollbackAllDDL(t *testing.T) {
	allSteps := append(slices.Clone(migrationOne), migrationTwo...)
	for index := range allSteps {
		t.Run(fmt.Sprintf("step-%d", index+1), func(t *testing.T) {
			store := newTestStore(t)
			seedLegacy(t, store)
			steps := slices.Clone(allSteps)
			steps[index] = "INVALID MIGRATION SQL"
			for attempt := 0; attempt < 3; attempt++ {
				backup, err := store.migrate(context.Background(), steps)
				if err == nil || backup == "" {
					t.Fatalf("%q/%v", backup, err)
				}
				verifyBackup(t, backup)
				verifyLegacy(t, store.db, 0)
				var count int
				if err := store.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name NOT IN ('legacy','sqlite_sequence')").Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("partial migration survived: %d", count)
				}
				if err := store.db.QueryRow("SELECT count(*) FROM sqlite_temp_master").Scan(&count); err != nil || count != 0 {
					t.Fatalf("temporary migration state survived rollback: %d/%v", count, err)
				}
				if store.db.Stats().InUse != 0 {
					t.Fatal("transaction retained connection")
				}
			}
			if _, err := store.Migrate(context.Background()); err != nil {
				t.Fatalf("safe retry failed: %v", err)
			}
			verifyLegacy(t, store.db, SchemaVersion)
		})
	}
}

func TestMigrationNilCancelledDeadlineAndClosedDBCreateNoBackup(t *testing.T) {
	for _, mode := range []string{"nil", "cancelled", "deadline", "closed"} {
		t.Run(mode, func(t *testing.T) {
			store := newTestStore(t)
			seedLegacy(t, store)
			ctx := context.Background()
			var want error
			switch mode {
			case "nil":
				ctx = nil
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
			case "closed":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 3; i++ {
				backup, err := store.Migrate(ctx)
				if backup != "" || err == nil || want != nil && !errors.Is(err, want) {
					t.Fatalf("%q/%v", backup, err)
				}
			}
			files, err := filepath.Glob(filepath.Join(filepath.Dir(store.path), "jackpot-backup-*"))
			if err != nil || len(files) != 0 {
				t.Fatalf("orphaned backup: %v/%v", files, err)
			}
		})
	}
}

func TestNewerSchemaRejectedBeforeWALOrFilesystemMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.sqlite3")
	db, err := sql.Open("sqlite", fileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE preserved(value TEXT); INSERT INTO preserved VALUES ('future'); PRAGMA user_version=999"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		store, err := Open(context.Background(), path)
		if store != nil || !errors.Is(err, ErrNewerSchema) {
			t.Fatalf("%v/%v", store, err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("newer DB changed: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("sidecar mutation: %v/%v", entries, err)
	}
}

func TestMigrationNewerVersionInExistingConnectionPreservesData(t *testing.T) {
	store := newTestStore(t)
	seedLegacy(t, store)
	if _, err := store.db.Exec("PRAGMA user_version=999"); err != nil {
		t.Fatal(err)
	}
	backup, err := store.Migrate(context.Background())
	if backup != "" || !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("%q/%v", backup, err)
	}
	verifyLegacy(t, store.db, 999)
}

func TestMigrationCommitFailureRollsBackVersionAndEveryDDLChange(t *testing.T) {
	store := newTestStore(t)
	seedLegacy(t, store)
	steps := []string{
		"CREATE TABLE migration_parent(id INTEGER PRIMARY KEY)",
		"CREATE TABLE migration_child(parent INTEGER REFERENCES migration_parent(id) DEFERRABLE INITIALLY DEFERRED)",
		"INSERT INTO migration_child VALUES (99)",
	}
	backup, err := store.migrate(context.Background(), steps)
	if err == nil || backup == "" {
		t.Fatalf("commit failure hidden: %q/%v", backup, err)
	}
	verifyBackup(t, backup)
	verifyLegacy(t, store.db, 0)
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name LIKE 'migration_%'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial commit: %d/%v", count, err)
	}
	if store.db.Stats().InUse != 0 {
		t.Fatal("commit failure retained connection")
	}
	if _, err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("driver rollback did not allow retry: %v", err)
	}
}

func TestMigrationBackupFailureRemovesUnverifiedTemporaryFileAndAllowsRetry(t *testing.T) {
	store := newTestStore(t)
	seedLegacy(t, store)
	if _, err := store.db.Exec("PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	backup, err := store.Migrate(context.Background())
	if err == nil || backup != "" {
		t.Fatalf("unverified backup returned: %q/%v", backup, err)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(store.path), "jackpot-backup-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("orphaned backup: %v/%v", files, err)
	}
	verifyLegacy(t, store.db, 0)
	if _, err := store.db.Exec("PRAGMA query_only=OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationBackupDirectoryFailurePreservesOriginalDatabase(t *testing.T) {
	store := newTestStore(t)
	seedLegacy(t, store)
	original := store.path
	parent := filepath.Join(filepath.Dir(original), "file-parent")
	if err := os.WriteFile(parent, []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(parent, "data.sqlite3")
	backup, err := store.Migrate(context.Background())
	if err == nil || backup != "" {
		t.Fatalf("invalid backup directory accepted: %q/%v", backup, err)
	}
	verifyLegacy(t, store.db, 0)
	store.path = original
	if _, err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestV1UpgradePreservesDeletedOperationSequenceHighWater(t *testing.T) {
	store := newTestStore(t)
	for _, step := range migrationOne {
		execSQL(t, store.db, step)
	}
	execSQL(t, store.db, `INSERT INTO operations(operation_id,kind,collection_id,round_id,status,revision,public_fingerprint) VALUES ('old','CreateCollection','old','old','pending',0,zeroblob(32))`)
	execSQL(t, store.db, `DELETE FROM operations; PRAGMA user_version=1`)
	backup, err := store.Migrate(context.Background())
	if err != nil || backup == "" {
		t.Fatalf("upgrade: %q/%v", backup, err)
	}
	seedOperation(t, store, 1, "pending")
	var key int
	if err := store.db.QueryRow("SELECT creation_key FROM operations").Scan(&key); err != nil || key != 2 {
		t.Fatalf("generation reused: %d/%v", key, err)
	}
	again, err := store.Migrate(context.Background())
	if err != nil || again != "" {
		t.Fatal("upgrade not idempotent")
	}
	assertNoHandles(t, store)
}

func TestV1OrphanUpgradeFailsWithoutDiscardingDataAndRetainsVerifiedBackup(t *testing.T) {
	store := newTestStore(t)
	for _, step := range migrationOne {
		execSQL(t, store.db, step)
	}
	execSQL(t, store.db, `INSERT INTO operations(operation_id,kind,collection_id,round_id,status,revision,public_fingerprint) VALUES ('old','CreateCollection','old','old','pending',0,zeroblob(32)); PRAGMA user_version=1`)
	for attempt := 0; attempt < 3; attempt++ {
		backup, err := store.Migrate(context.Background())
		if err == nil || backup == "" {
			t.Fatalf("orphan upgrade silently discarded data: %q/%v", backup, err)
		}
		var version, count int
		if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 1 {
			t.Fatalf("version changed: %d/%v", version, err)
		}
		if err := store.db.QueryRow("SELECT count(*) FROM operations WHERE operation_id='old'").Scan(&count); err != nil || count != 1 {
			t.Fatalf("record changed: %d/%v", count, err)
		}
		if err := store.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='collections'").Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial schema: %d/%v", count, err)
		}
		db, err := sql.Open("sqlite", fileURI(backup)+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT count(*) FROM operations WHERE operation_id='old'").Scan(&count); err != nil || count != 1 {
			t.Fatalf("backup lost original: %d/%v", count, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		assertNoHandles(t, store)
	}
}

func seedEmptyVersionOne(t *testing.T, store *Store) {
	t.Helper()
	seedLegacy(t, store)
	for _, step := range migrationOne {
		execSQL(t, store.db, step)
	}
	execSQL(t, store.db, `INSERT INTO operations(creation_key,operation_id,kind,collection_id,round_id,status,revision,public_fingerprint) VALUES (7,'old','CreateCollection','old','old','pending',0,zeroblob(32))`)
	execSQL(t, store.db, `DELETE FROM operations; PRAGMA user_version=1`)
}

func verifyEmptyVersionOne(t *testing.T, db *sql.DB) {
	t.Helper()
	verifyLegacy(t, db, 1)
	var count, sequence int
	if err := db.QueryRow("SELECT count(*) FROM operations").Scan(&count); err != nil || count != 0 {
		t.Fatalf("v1 operation rows changed: %d/%v", count, err)
	}
	if err := db.QueryRow("SELECT seq FROM sqlite_sequence WHERE name='operations'").Scan(&sequence); err != nil || sequence != 7 {
		t.Fatalf("v1 creation sequence changed: %d/%v", sequence, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('collections','operations_next')").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial v2 schema survived: %d/%v", count, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM sqlite_temp_master").Scan(&count); err != nil || count != 0 {
		t.Fatalf("temporary v2 state survived: %d/%v", count, err)
	}
}

func TestVersionOneEveryMigrationStepFailurePreservesSequenceBackupAndReopen(t *testing.T) {
	for index := range migrationTwo {
		t.Run(fmt.Sprintf("step-%d", index+1), func(t *testing.T) {
			store := newTestStore(t)
			seedEmptyVersionOne(t, store)
			steps := slices.Clone(migrationTwo)
			steps[index] = "INVALID VERSION TWO MIGRATION SQL"
			backup, err := store.migrate(context.Background(), steps)
			if err == nil || backup == "" {
				t.Fatalf("failure hidden: %q/%v", backup, err)
			}
			verifyEmptyVersionOne(t, store.db)
			assertNoHandles(t, store)
			copyDB, err := sql.Open("sqlite", fileURI(backup)+"?mode=ro")
			if err != nil {
				t.Fatal(err)
			}
			verifyEmptyVersionOne(t, copyDB)
			if err := copyDB.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(backup, backup+".verified"); err != nil {
				t.Fatalf("verified backup retained handle: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if stats := store.db.Stats(); stats.OpenConnections != 0 || stats.InUse != 0 {
				t.Fatalf("migration failure/retry retained connections: %+v", stats)
			}
			reopened, err := Open(context.Background(), store.path)
			if err != nil {
				t.Fatalf("failed upgrade prevented reopen: %v", err)
			}
			defer reopened.Close()
			verifyEmptyVersionOne(t, reopened.db)
			if _, err := reopened.Migrate(context.Background()); err != nil {
				t.Fatalf("failed upgrade prevented safe retry: %v", err)
			}
			verifyLegacy(t, reopened.db, SchemaVersion)
			seedOperation(t, reopened, 1, "pending")
			var key int
			if err := reopened.db.QueryRow("SELECT creation_key FROM operations").Scan(&key); err != nil || key != 8 {
				t.Fatalf("retry reused prior high-water: %d/%v", key, err)
			}
			assertNoHandles(t, reopened)
		})
	}
}

func TestMigrationBackupIsStandaloneWithUncheckpointedSourceWAL(t *testing.T) {
	store := newTestStore(t)
	execSQL(t, store.db, "PRAGMA wal_autocheckpoint=0")
	seedLegacy(t, store)
	wal, err := os.Stat(store.path + "-wal")
	if err != nil || wal.Size() == 0 {
		t.Fatalf("harness has no uncheckpointed WAL: %v/%v", wal, err)
	}
	backup, err := store.Migrate(context.Background())
	if err != nil || backup == "" {
		t.Fatalf("backup: %q/%v", backup, err)
	}
	raw, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	restoredPath := filepath.Join(t.TempDir(), "standalone.sqlite3")
	if err := os.WriteFile(restoredPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	verifyBackup(t, restoredPath)
	entries, err := os.ReadDir(filepath.Dir(restoredPath))
	if err != nil || len(entries) != 1 {
		t.Fatalf("backup requires sidecar: %v/%v", entries, err)
	}
	if err := os.Rename(restoredPath, restoredPath+".verified"); err != nil {
		t.Fatalf("backup verifier retained handle: %v", err)
	}
}

func TestNewerSchemaInUncheckpointedWALIsRejectedWithoutChangingDatabaseOrJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future-wal.sqlite3")
	db, err := sql.Open("sqlite", fileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	execSQL(t, db, fmt.Sprintf("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE preserved(value TEXT); INSERT INTO preserved VALUES ('future'); PRAGMA user_version=%d", SchemaVersion+1))
	before := make(map[string][]byte)
	for _, filename := range []string{path, path + "-wal"} {
		raw, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		before[filename] = raw
	}
	shmBefore, err := os.ReadFile(path + "-shm")
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		store, err := Open(context.Background(), path)
		if store != nil || !errors.Is(err, ErrNewerSchema) {
			t.Fatalf("attempt %d failed to read WAL schema: %v/%v", attempt, store, err)
		}
		for filename, raw := range before {
			after, err := os.ReadFile(filename)
			if err != nil || !bytes.Equal(raw, after) {
				t.Fatalf("attempt %d modified newer schema file %s: %v", attempt, filename, err)
			}
		}
		shmAfter, err := os.ReadFile(path + "-shm")
		if err != nil || len(shmBefore) != len(shmAfter) {
			t.Fatalf("newer WAL probe changed shared-memory extent: %v", err)
		}
		// WAL readers update only the transient read marks in this controlled,
		// already initialized WAL index. They never alter committed WAL frames.
		// SQLite's documented WAL-index header puts these marks at 100..119.
		for offset, value := range shmBefore {
			if shmAfter[offset] != value && (offset < 100 || offset > 119) {
				t.Fatalf("newer WAL probe changed non-read-mark SHM offset %d", offset)
			}
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 3 {
		t.Fatalf("newer WAL probe created sidecars: %v/%v", entries, err)
	}
}
