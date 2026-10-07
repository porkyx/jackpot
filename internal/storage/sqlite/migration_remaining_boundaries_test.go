package sqlite

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	driverSQLite "modernc.org/sqlite"
)

// The existing exact-path hook runs after VACUUM has finished and before the
// readonly verifier's first query. It alters only the new owned backup file.
// Every SQL query and error is produced by the pinned SQLite driver.
func TestBackupActualIntegrityNonOKAndQueryErrorsFirstNthContinuousPreserveSource(t *testing.T) {
	for _, queryError := range []bool{false, true} {
		for _, mode := range []struct {
			name       string
			at         int32
			continuous bool
		}{{"first", 1, false}, {"nth", 2, false}, {"continuous", 1, true}} {
			t.Run(fmt.Sprintf("query-error=%t/%s", queryError, mode.name), func(t *testing.T) {
				store := newTestStore(t)
				seedLegacy(t, store)
				var schemaBefore string
				if err := store.db.QueryRow("SELECT sql FROM sqlite_schema WHERE name='legacy'").Scan(&schemaBefore); err != nil {
					t.Fatal(err)
				}
				var existing map[string]bool
				var corruptedPath string
				var corruptions int
				connection := &admissionConnectionProbe{path: store.path, backup: true, at: 1, continuous: true}
				connection.action = func(driverSQLite.ExecQuerierContext) error {
					call := connection.calls.Load()
					if call != mode.at && !(mode.continuous && call >= mode.at) {
						return nil
					}
					var newPaths []string
					for _, path := range backupPaths(t, store) {
						if !existing[path] {
							newPaths = append(newPaths, path)
						}
					}
					if len(newPaths) != 1 {
						return errors.New("fixture did not isolate the newly created owned backup")
					}
					path := newPaths[0]
					raw, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					if len(raw) < 100 || string(raw[:16]) != "SQLite format 3\x00" {
						return errors.New("fixture backup is not an intact SQLite file before the fault")
					}
					if queryError {
						raw[0] = '!'
					} else {
						// SQLite header bytes 32..39 describe the freelist.
						// VACUUM produces no free pages. A declared count of
						// one with head zero is a real integrity discrepancy.
						if binary.BigEndian.Uint32(raw[32:36]) != 0 || binary.BigEndian.Uint32(raw[36:40]) != 0 {
							return errors.New("fixture VACUUM unexpectedly retained free pages")
						}
						binary.BigEndian.PutUint32(raw[36:40], 1)
					}
					if err := os.WriteFile(path, raw, 0600); err != nil {
						return err
					}
					corruptedPath = path
					corruptions++
					return nil
				}
				armAdmissionConnectionProbe(t, connection)
				verifiedCount := 0
				failures := 0
				for call := int32(1); call <= 3; call++ {
					existing = make(map[string]bool)
					for _, path := range backupPaths(t, store) {
						existing[path] = true
					}
					corruptedPath = ""
					path, err := store.backup(context.Background(), 0)
					failed := call == mode.at || mode.continuous && call >= mode.at
					if connection.calls.Load() != call {
						t.Fatal("backup verifier retried or missed its exact connection", connection.calls.Load(), call)
					}
					if failed {
						failures++
						if path != "" || err == nil || corruptedPath == "" || corruptions != failures {
							t.Fatal("unverified integrity backup escaped or fault was not applied", path, err, corruptions, failures)
						}
						if queryError {
							var sqliteError *driverSQLite.Error
							if !errors.As(err, &sqliteError) || sqliteError.Code() != 26 || !strings.Contains(strings.ToLower(err.Error()), "not a database") {
								t.Fatal("actual query NOTADB cause was replaced by a later integrity-value error", err)
							}
						} else if err.Error() != "backup integrity check failed" {
							t.Fatal("actual non-ok header integrity result did not reach its string guard", err)
						}
						if _, statErr := os.Stat(corruptedPath); !errors.Is(statErr, os.ErrNotExist) {
							t.Fatal("unverified corrupted backup was retained", statErr)
						}
					} else {
						if err != nil || path == "" || corruptedPath != "" {
							t.Fatal("healthy non-failing invocation changed", path, err)
						}
						verifiedCount++
					}
					if len(backupPaths(t, store)) != verifiedCount {
						t.Fatal("unverified file survived or verified prior backup was removed", backupPaths(t, store), verifiedCount)
					}
					verifyLegacy(t, store.db, 0)
					var schemaAfter string
					if err := store.db.QueryRow("SELECT sql FROM sqlite_schema WHERE name='legacy'").Scan(&schemaAfter); err != nil || schemaAfter != schemaBefore {
						t.Fatal("backup verification mutated source schema", schemaAfter, err)
					}
					assertNoHandles(t, store)
				}
				activeAdmissionConnectionProbe.CompareAndSwap(connection, nil)
				backup, err := store.Migrate(context.Background())
				if err != nil || backup == "" || len(backupPaths(t, store)) != verifiedCount+1 {
					t.Fatal("corrected verification prevented migration retry", backup, err)
				}
				verifyLegacy(t, store.db, SchemaVersion)
				for _, path := range backupPaths(t, store) {
					verifyBackup(t, path)
					if err := os.Rename(path, path+".exclusive"); err != nil {
						t.Fatal("backup verification retained native file handle", err)
					}
				}
				assertNoHandles(t, store)
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(store.path, store.path+".exclusive"); err != nil {
					t.Fatal("migration retained source native file handle", err)
				}
			})
		}
	}
}
