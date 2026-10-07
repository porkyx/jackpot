package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	driverSQLite "modernc.org/sqlite"
)

// Capture real public values without replacing any SQL driver or read port.
type scalarReadValues struct {
	Count      uint32
	Found      contracts.CollectionID
	Collection rl.FrozenCollection
	Rounds     []rl.RoundRecord
	Revision   contracts.Revision
	Operation  rl.OperationRecord
}

func scalarRead(t *testing.T, store *Store, method string, ctx context.Context, id string) (value scalarReadValues, err error) {
	t.Helper()
	switch method {
	case "CollectionCount":
		value.Count, err = store.CollectionCount(ctx)
	case "FindCollectionByDraft":
		value.Found, err = store.FindCollectionByDraft(ctx, contracts.DraftID(id))
	case "ReadCollectionState":
		value.Collection, value.Rounds, value.Revision, err = store.ReadCollectionState(ctx, contracts.CollectionID(id))
	case "ReadActiveOperation":
		value.Operation, err = store.ReadActiveOperation(ctx, contracts.RoundID(id))
	default:
		t.Fatal("unknown scalar read", method)
	}
	return
}

func scalarReadFixture(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	store := migratedTestStore(t)
	seedRound(t, store, "c1", "r1", "op1", "executing")
	// TEMP fault views belong only to the production connection. This independent
	// actual read-only connection checks every main table, not the fault projection.
	reader, err := sql.Open("sqlite", fileURI(store.path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	reader.SetMaxOpenConns(1)
	reader.SetMaxIdleConns(1)
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := reader.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store, reader
}

func scalarReadSnapshot(t *testing.T, reader *sql.DB) (string, string) {
	t.Helper()
	data := corruptionDurableSnapshot(t, &Store{db: reader})
	var schema string
	err := reader.QueryRow(`SELECT group_concat(type||':'||name||':'||tbl_name||':'||coalesce(sql,''),char(10))
 FROM (SELECT type,name,tbl_name,sql FROM main.sqlite_master ORDER BY type,name)`).Scan(&schema)
	if err != nil {
		t.Fatal(err)
	}
	return data, schema
}

func scalarReadAssertUnchanged(t *testing.T, store *Store, reader *sql.DB, data, schema string) {
	t.Helper()
	after, afterSchema := scalarReadSnapshot(t, reader)
	if after != data || afterSchema != schema {
		t.Fatal("read altered a main table, sequence or schema")
	}
	assertNoHandles(t, store)
	if reader.Stats().InUse != 0 {
		t.Fatal("independent snapshot connection leaked")
	}
}

func scalarReadRelease(t *testing.T, store *Store, reader *sql.DB) {
	t.Helper()
	assertRoundSQLIntegrity(t, store)
	assertNoHandles(t, store)
	if reader.Stats().InUse != 0 {
		t.Fatal("snapshot connection leaked before Close")
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(store.path, store.path+".released"); err != nil {
		t.Fatal("scalar read retained the owned database file", err)
	}
}

func scalarReadHealthy(t *testing.T, store *Store, method, id string) scalarReadValues {
	t.Helper()
	value, err := scalarRead(t, store, method, context.Background(), id)
	if err != nil {
		t.Fatal("healthy scalar fixture failed", method, err)
	}
	switch method {
	case "CollectionCount":
		if value.Count != 1 {
			t.Fatal("wrong healthy collection count", value.Count)
		}
	case "FindCollectionByDraft":
		if value.Found != "c1" {
			t.Fatal("wrong healthy source draft mapping", value.Found)
		}
	case "ReadCollectionState":
		if value.Collection.ID != "c1" || len(value.Collection.Participants) != 3 || len(value.Rounds) != 1 || value.Rounds[0].ID != "r1" || value.Revision != 1 {
			t.Fatal("wrong healthy collection state", value)
		}
	case "ReadActiveOperation":
		if value.Operation.Operation.ID != "op1" || value.Operation.Status != contracts.OperationPending {
			t.Fatal("wrong healthy active operation", value.Operation)
		}
	}
	return value
}

func TestScalarReadCallerNilCancellationAndEmptyIDReturnNoValuesAndCanRequery(t *testing.T) {
	for _, method := range []struct{ name, id string }{{"CollectionCount", ""}, {"FindCollectionByDraft", "draft-c1"}, {"ReadCollectionState", "c1"}, {"ReadActiveOperation", "r1"}} {
		modes := []string{"nil", "cancelled"}
		if method.name != "CollectionCount" {
			modes = append(modes, "empty")
		}
		for _, mode := range modes {
			t.Run(method.name+"/"+mode, func(t *testing.T) {
				store, reader := scalarReadFixture(t)
				expected := scalarReadHealthy(t, store, method.name, method.id)
				data, schema := scalarReadSnapshot(t, reader)
				ctx, id := context.Background(), method.id
				if mode == "nil" {
					ctx = nil
				} else if mode == "cancelled" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				} else {
					id = ""
				}
				value, err := scalarRead(t, store, method.name, ctx, id)
				if mode == "cancelled" {
					if !errors.Is(err, context.Canceled) {
						t.Fatal("caller lost cancellation cause", err)
					}
				} else {
					requireFault(t, err, contracts.InvalidInput)
				}
				if !reflect.DeepEqual(value, scalarReadValues{}) {
					t.Fatal("rejected caller exposed partial values", value)
				}
				scalarReadAssertUnchanged(t, store, reader, data, schema)
				if actual := scalarReadHealthy(t, store, method.name, method.id); !reflect.DeepEqual(actual, expected) {
					t.Fatal("caller correction changed the exact durable projection")
				}
				scalarReadRelease(t, store, reader)
			})
		}
	}
}

func TestScalarReadActualSQLAndScanFailuresPreserveMainAndRecoverOnSameConnection(t *testing.T) {
	cases := []struct{ name, method, id, view, query, failure string }{
		{"count-query", "CollectionCount", "", "collections", `SELECT * FROM main.collections WHERE json_extract('not-json','$') IS NULL`, "SQL"},
		{"draft-query", "FindCollectionByDraft", "draft-c1", "collections", `SELECT * FROM main.collections WHERE json_extract('not-json','$') IS NULL`, "SQL"},
		{"draft-null-scan", "FindCollectionByDraft", "draft-c1", "collections", `SELECT NULL AS id,source_draft_id FROM main.collections`, "Scan"},
		{"state-source-null-scan", "ReadCollectionState", "c1", "collections", `SELECT id,NULL AS source_draft_id,revision,frozen_json,created_at FROM main.collections`, "Scan"},
		{"state-round-query", "ReadCollectionState", "c1", "rounds", `SELECT collection_id,number FROM main.rounds`, "SQL"},
		{"active-query", "ReadActiveOperation", "r1", "rounds", `SELECT id FROM main.rounds`, "SQL"},
		{"active-null-scan", "ReadActiveOperation", "r1", "rounds", `SELECT id,NULL AS active_operation_id FROM main.rounds`, "Scan"},
		{"active-not-found", "ReadActiveOperation", "r1", "", "", "NotFound"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, reader := scalarReadFixture(t)
			expected := scalarReadHealthy(t, store, tc.method, tc.id)
			data, schema := scalarReadSnapshot(t, reader)
			faultInstalled := false
			if tc.view != "" {
				execSQL(t, store.db, "CREATE TEMP VIEW "+tc.view+" AS "+tc.query)
				faultInstalled = true
				t.Cleanup(func() {
					if faultInstalled {
						execSQL(t, store.db, "DROP VIEW IF EXISTS temp."+tc.view)
					}
				})
			}
			id := tc.id
			if tc.failure == "NotFound" {
				id = "absent-round"
			}
			for repeat := 0; repeat < 3; repeat++ {
				value, err := scalarRead(t, store, tc.method, context.Background(), id)
				switch tc.failure {
				case "Scan":
					remainingReadScanError(t, err)
				case "SQL":
					var driverError *driverSQLite.Error
					if !errors.As(err, &driverError) || driverError.Code() != 1 {
						t.Fatal("missing actual SQLite query error", err)
					}
				case "NotFound":
					if !errors.Is(err, rl.ErrNotFound) {
						t.Fatal("missing round became success or unknown", value, err)
					}
				}
				if !reflect.DeepEqual(value, scalarReadValues{}) {
					t.Fatal("real query failure exposed partial values", value)
				}
				scalarReadAssertUnchanged(t, store, reader, data, schema)
			}
			if tc.view != "" {
				execSQL(t, store.db, "DROP VIEW temp."+tc.view)
				faultInstalled = false
			}
			if actual := scalarReadHealthy(t, store, tc.method, tc.id); !reflect.DeepEqual(actual, expected) {
				t.Fatal("removed fault changed the original scalar result")
			}
			scalarReadAssertUnchanged(t, store, reader, data, schema)
			scalarReadRelease(t, store, reader)
		})
	}
}

func TestCollectionStateClosedDatabaseBeginReturnsNoValuesAndReopensSameFile(t *testing.T) {
	store, reader := scalarReadFixture(t)
	expected := scalarReadHealthy(t, store, "ReadCollectionState", "c1")
	data, schema := scalarReadSnapshot(t, reader)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	value, err := scalarRead(t, store, "ReadCollectionState", context.Background(), "c1")
	if err == nil || !strings.Contains(err.Error(), "database is closed") || !reflect.DeepEqual(value, scalarReadValues{}) {
		t.Fatal("closed Begin published a collection", value, err)
	}
	scalarReadAssertUnchanged(t, store, reader, data, schema)
	reopened, err := Open(context.Background(), store.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if actual := scalarReadHealthy(t, reopened, "ReadCollectionState", "c1"); !reflect.DeepEqual(actual, expected) {
		t.Fatal("same-file reopen changed collection state")
	}
	scalarReadAssertUnchanged(t, reopened, reader, data, schema)
	scalarReadRelease(t, reopened, reader)
}
