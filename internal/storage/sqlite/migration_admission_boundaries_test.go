package sqlite

import (
	"context"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	driverSQLite "modernc.org/sqlite"
)

// Registered before test execution, the hook is inert outside one exact
// owned file or its owned backup prefix. It runs real SQLite commands;
// connection errors below come from the pinned driver's connection boundary.
type admissionConnectionProbe struct {
	path       string
	backup     bool
	at         int32
	continuous bool
	calls      atomic.Int32
	action     func(driverSQLite.ExecQuerierContext) error
}

var activeAdmissionConnectionProbe atomic.Pointer[admissionConnectionProbe]

func init() {
	driverSQLite.RegisterConnectionHook(func(c driverSQLite.ExecQuerierContext, dsn string) error {
		probe := activeAdmissionConnectionProbe.Load()
		if probe == nil {
			return nil
		}
		target := strings.HasPrefix(dsn, fileURI(probe.path)+"?") && strings.Contains(dsn, "_txlock=immediate")
		if probe.backup {
			target = strings.HasPrefix(dsn, fileURI(filepath.Dir(probe.path))+"/jackpot-backup-") && strings.HasSuffix(dsn, "?mode=ro")
		}
		if !target {
			return nil
		}
		call := probe.calls.Add(1)
		if call == probe.at || probe.continuous && call >= probe.at {
			return probe.action(c)
		}
		return nil
	})
}

func armAdmissionConnectionProbe(t *testing.T, probe *admissionConnectionProbe) {
	t.Helper()
	if !activeAdmissionConnectionProbe.CompareAndSwap(nil, probe) {
		t.Fatal("connection probe already owned")
	}
	t.Cleanup(func() { activeAdmissionConnectionProbe.CompareAndSwap(probe, nil) })
}

func backupPaths(t *testing.T, store *Store) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(filepath.Dir(store.path), "jackpot-backup-*.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestBackupVersionMismatchRemovesUnverifiedCopyAndPreservesSource(t *testing.T) {
	store := newTestStore(t)
	seedLegacy(t, store)
	for attempt := 0; attempt < 3; attempt++ {
		path, err := store.backup(context.Background(), 1)
		if path != "" || err == nil || err.Error() != "backup schema version mismatch" || len(backupPaths(t, store)) != 0 {
			t.Fatal("unverified schema backup escaped", path, err, backupPaths(t, store))
		}
		verifyLegacy(t, store.db, 0)
		assertNoHandles(t, store)
	}
	verified, err := store.backup(context.Background(), 0)
	if err != nil || verified == "" || len(backupPaths(t, store)) != 1 {
		t.Fatal("healthy retry failed", verified, err)
	}
	verifyBackup(t, verified)
	if err := os.Rename(verified, verified+".exclusive"); err != nil {
		t.Fatal("backup verifier retained its file", err)
	}
}

func TestBackupVerificationConnectionFailuresFirstNthContinuousNeverReturnUnverifiedPath(t *testing.T) {
	for _, mode := range []struct {
		name       string
		at         int32
		continuous bool
	}{
		{name: "first", at: 1},
		{name: "nth", at: 2},
		{name: "continuous", at: 1, continuous: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			store := newTestStore(t)
			seedLegacy(t, store)
			sentinel := errors.New("owned backup verification admission failure")
			probe := &admissionConnectionProbe{path: store.path, backup: true, at: mode.at, continuous: mode.continuous, action: func(driverSQLite.ExecQuerierContext) error { return sentinel }}
			armAdmissionConnectionProbe(t, probe)
			verifiedCount := 0
			for attempt := int32(1); attempt <= 3; attempt++ {
				path, err := store.backup(context.Background(), 0)
				failed := attempt == mode.at || mode.continuous && attempt >= mode.at
				if failed {
					if path != "" || !errors.Is(err, sentinel) {
						t.Fatal("verification failure returned a backup or lost its cause", attempt, path, err)
					}
				} else {
					if err != nil || path == "" {
						t.Fatal("non-failing invocation changed", attempt, path, err)
					}
					verifiedCount++
				}
				if probe.calls.Load() != attempt || len(backupPaths(t, store)) != verifiedCount {
					t.Fatal("unverified file or implicit retry leaked", probe.calls.Load(), backupPaths(t, store))
				}
				verifyLegacy(t, store.db, 0)
				assertNoHandles(t, store)
			}
			activeAdmissionConnectionProbe.CompareAndSwap(probe, nil)
			path, err := store.backup(context.Background(), 0)
			if err != nil || path == "" {
				t.Fatal("corrected verification could not retry", path, err)
			}
			for _, file := range backupPaths(t, store) {
				verifyBackup(t, file)
				if err := os.Rename(file, file+".exclusive"); err != nil {
					t.Fatal("backup connection remained open", err)
				}
			}
		})
	}
}

func TestOpenIndependentDurabilityMismatchAndNestedAdmissionReturnNoStoreAndCloseFiles(t *testing.T) {
	cases := []struct {
		name, alter string
		expected    []driver.Value
	}{
		{name: "foreign-keys", alter: "PRAGMA foreign_keys=OFF", expected: []driver.Value{int64(0), int64(2), int64(1000), "wal"}},
		{name: "synchronous", alter: "PRAGMA synchronous=OFF", expected: []driver.Value{int64(1), int64(0), int64(1000), "wal"}},
		{name: "busy-timeout", alter: "PRAGMA busy_timeout=0", expected: []driver.Value{int64(1), int64(2), int64(0), "wal"}},
		{name: "journal-mode", alter: "PRAGMA journal_mode=DELETE", expected: []driver.Value{int64(1), int64(2), int64(1000), "delete"}},
		{name: "begin-admission", alter: "BEGIN IMMEDIATE", expected: []driver.Value{int64(1), int64(2), int64(1000), "wal"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			seedLegacy(t, store)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			var observed []driver.Value
			probe := &admissionConnectionProbe{path: store.path, at: 1, action: func(c driverSQLite.ExecQuerierContext) error {
				if _, err := c.ExecContext(context.Background(), tc.alter, nil); err != nil {
					return err
				}
				for _, q := range []string{"PRAGMA foreign_keys", "PRAGMA synchronous", "PRAGMA busy_timeout", "PRAGMA journal_mode"} {
					rows, err := c.QueryContext(context.Background(), q, nil)
					if err != nil {
						return err
					}
					values := make([]driver.Value, 1)
					nextErr, closeErr := rows.Next(values), rows.Close()
					if nextErr != nil || closeErr != nil {
						return errors.Join(nextErr, closeErr)
					}
					observed = append(observed, values[0])
				}
				return nil
			}}
			armAdmissionConnectionProbe(t, probe)
			actual, err := Open(context.Background(), store.path)
			if actual != nil {
				_ = actual.Close()
				t.Fatal("unsafe connection was published")
			}
			if err == nil || probe.calls.Load() != 1 || !reflect.DeepEqual(observed, tc.expected) {
				t.Fatal("fixture did not isolate the intended durability/admission boundary", observed, err, probe.calls.Load())
			}
			if tc.name == "begin-admission" {
				if !strings.Contains(err.Error(), "within a transaction") {
					t.Fatal("earlier query hid actual BEGIN admission failure", err)
				}
			} else if err.Error() != "unsafe SQLite durability configuration" {
				t.Fatal("mismatch did not reach its independent guard", err)
			}
			activeAdmissionConnectionProbe.CompareAndSwap(probe, nil)
			if err := os.Rename(store.path, store.path+".exclusive"); err != nil {
				t.Fatal("failed Open retained a native handle", err)
			}
			if err := os.Rename(store.path+".exclusive", store.path); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(context.Background(), store.path)
			if err != nil {
				t.Fatal("failed admission prevented corrected Open", err)
			}
			defer reopened.Close()
			verifyLegacy(t, reopened.db, 0)
			assertNoHandles(t, reopened)
		})
	}
}

func TestMigrationVerifiedBackupAdmissionAndVersionWriteFailuresRemainAtomic(t *testing.T) {
	for _, mode := range []string{"closed-after-verification", "version-write"} {
		t.Run(mode, func(t *testing.T) {
			store := newTestStore(t)
			seedLegacy(t, store)
			var backup string
			var err error
			if mode == "closed-after-verification" {
				probe := &admissionConnectionProbe{path: store.path, backup: true, at: 1, action: func(driverSQLite.ExecQuerierContext) error { return store.Close() }}
				armAdmissionConnectionProbe(t, probe)
				backup, err = store.Migrate(context.Background())
				activeAdmissionConnectionProbe.CompareAndSwap(probe, nil)
				if probe.calls.Load() != 1 || err == nil || !strings.Contains(err.Error(), "database is closed") {
					t.Fatal("verified backup did not precede actual BeginTx failure", err, probe.calls.Load())
				}
			} else {
				backup, err = store.migrate(context.Background(), []string{"CREATE TABLE must_rollback(value TEXT)", "PRAGMA query_only=ON"})
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), "readonly") {
					t.Fatal("migration did not reach its final version header write", err)
				}
				if _, clearErr := store.db.Exec("PRAGMA query_only=OFF"); clearErr != nil {
					t.Fatal(clearErr)
				}
				verifyLegacy(t, store.db, 0)
				var count int
				if queryErr := store.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='must_rollback'").Scan(&count); queryErr != nil || count != 0 {
					t.Fatal("failed version update committed preceding DDL", count, queryErr)
				}
			}
			if backup == "" || len(backupPaths(t, store)) != 1 {
				t.Fatal("verified backup was lost after admission/write failure", backup, err)
			}
			verifyBackup(t, backup)
			if err := os.Rename(backup, backup+".exclusive"); err != nil {
				t.Fatal("retained verified backup remained locked", err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(context.Background(), store.path)
			if err != nil {
				t.Fatal("failed migration prevented reopen", err)
			}
			defer reopened.Close()
			verifyLegacy(t, reopened.db, 0)
			if _, err := reopened.Migrate(context.Background()); err != nil {
				t.Fatal("explicit corrected retry failed", err)
			}
			verifyLegacy(t, reopened.db, SchemaVersion)
			assertNoHandles(t, reopened)
		})
	}
}
