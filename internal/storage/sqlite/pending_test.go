package sqlite

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/porkyx/jackpot/internal/contracts"
)

func migratedTestStore(t *testing.T) *Store {
	t.Helper()
	store := newTestStore(t)
	if _, err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}
func seedOperation(t *testing.T, store *Store, index int, status string) {
	t.Helper()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	c, r, op := fmt.Sprintf("c-%03d", index), fmt.Sprintf("r-%03d", index), fmt.Sprintf("op-%03d", index)
	if _, err := tx.Exec(`INSERT INTO collections(id,source_draft_id,revision,frozen_json,created_at) VALUES (?,?,1,'{}','2026-10-06T00:00:00Z')`, c, "draft-"+c); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO rounds(collection_id,id,number,state,version,attempt,input_json,active_operation_id) VALUES (?,?,1,'executing',1,1,'{}',?)`, c, r, op); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(`INSERT INTO operations(operation_id,kind,collection_id,round_id,status,revision,public_fingerprint)
 VALUES (?,'CreateCollection',?,?,?,0,zeroblob(32))`, fmt.Sprintf("op-%03d", index), fmt.Sprintf("c-%03d", index), fmt.Sprintf("r-%03d", index), status)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func pendingRequest(cursor *string, limit uint32) contracts.PendingOperationsRequest {
	return contracts.PendingOperationsRequest{Cursor: cursor, Limit: &limit}
}

func TestPendingPagesZeroOne63_64_65And128(t *testing.T) {
	for _, count := range []int{0, 1, 63, 64, 65, 128} {
		t.Run(fmt.Sprintf("count-%d", count), func(t *testing.T) {
			store := migratedTestStore(t)
			for i := 1; i <= count; i++ {
				seedOperation(t, store, i, "pending")
			}
			var cursor *string
			seen := map[contracts.OperationID]bool{}
			pages := 0
			for {
				page, err := store.ListPendingOperations(context.Background(), pendingRequest(cursor, 64))
				if err != nil {
					t.Fatal(err)
				}
				pages++
				if page.Operations == nil || len(page.Operations) > 64 {
					t.Fatal("unbounded or null page")
				}
				for _, descriptor := range page.Operations {
					if seen[descriptor.OperationID] {
						t.Fatal("duplicate operation")
					}
					seen[descriptor.OperationID] = true
				}
				cursor = page.Cursor
				if cursor == nil {
					break
				}
				if pages > 3 {
					t.Fatal("unbounded pagination")
				}
			}
			if len(seen) != count {
				t.Fatalf("missing rows: %d/%d", len(seen), count)
			}
			if store.db.Stats().InUse != 0 {
				t.Fatal("read transaction leaked")
			}
		})
	}
}

func TestPendingKeysetSkipsCompletedEarlierPageWithoutSkippingLaterRows(t *testing.T) {
	store := migratedTestStore(t)
	for i := 1; i <= 130; i++ {
		seedOperation(t, store, i, "pending")
	}
	first, err := store.ListPendingOperations(context.Background(), pendingRequest(nil, 64))
	if err != nil || first.Cursor == nil {
		t.Fatalf("%v/%v", first, err)
	}
	if _, err := store.db.Exec("UPDATE operations SET status='succeeded' WHERE creation_key<=64"); err != nil {
		t.Fatal(err)
	}
	// A row ahead of the cursor also completes. It should disappear, not shift an offset.
	if _, err := store.db.Exec("UPDATE operations SET status='failed' WHERE creation_key=65"); err != nil {
		t.Fatal(err)
	}
	seedOperation(t, store, 131, "pending")
	second, err := store.ListPendingOperations(context.Background(), pendingRequest(first.Cursor, 64))
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Operations) != 64 || second.Operations[0].OperationID != "op-066" || second.Operations[63].OperationID != "op-129" || second.Cursor == nil {
		t.Fatalf("keyset skipped rows: %+v", second)
	}
	last, err := store.ListPendingOperations(context.Background(), pendingRequest(second.Cursor, 64))
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Operations) != 1 || last.Operations[0].OperationID != "op-130" || last.Cursor != nil {
		t.Fatalf("high-water changed: %+v", last)
	}
	resync, err := store.ListPendingOperations(context.Background(), pendingRequest(nil, 64))
	if err != nil || resync.Cursor == nil {
		t.Fatalf("%+v/%v", resync, err)
	}
	rest, err := store.ListPendingOperations(context.Background(), pendingRequest(resync.Cursor, 64))
	if err != nil {
		t.Fatal(err)
	}
	if len(rest.Operations) != 2 || rest.Operations[1].OperationID != "op-131" {
		t.Fatal("next resync lost new operation")
	}
}

func TestPendingQueryIsStableReadOnlyAndContainsOnlyDescriptorFields(t *testing.T) {
	store := migratedTestStore(t)
	seedOperation(t, store, 1, "pending")
	seedOperation(t, store, 2, "succeeded")
	seedOperation(t, store, 3, "failed")
	for i := 0; i < 2; i++ {
		page, err := store.ListPendingOperations(context.Background(), pendingRequest(nil, 1))
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Operations) != 1 || page.Cursor != nil || page.Operations[0].Revision != 0 {
			t.Fatal("read mutated descriptor")
		}
		wire, err := json.Marshal(page.Operations[0])
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(wire, &fields); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"operationId", "kind", "collectionId", "roundId", "status", "revision"} {
			if fields[key] == nil {
				t.Fatalf("missing %s", key)
			}
		}
		if len(fields) != 6 {
			t.Fatalf("secret/internal fields leaked: %s", wire)
		}
	}
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM operations").Scan(&count); err != nil || count != 3 {
		t.Fatalf("records deleted: %d/%v", count, err)
	}
}

func TestPendingReadDuringUncommittedWriterSeesCommittedSnapshot(t *testing.T) {
	store := migratedTestStore(t)
	seedOperation(t, store, 1, "pending")
	writer, err := Open(context.Background(), store.path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	tx, err := writer.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE operations SET status='succeeded',revision=1"); err != nil {
		t.Fatal(err)
	}
	before, err := store.ListPendingOperations(context.Background(), pendingRequest(nil, 64))
	if err != nil || len(before.Operations) != 1 || before.Operations[0].Revision != 0 {
		t.Fatalf("uncommitted state leaked: %+v/%v", before, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	after, err := store.ListPendingOperations(context.Background(), pendingRequest(nil, 64))
	if err != nil || len(after.Operations) != 0 {
		t.Fatalf("committed state lost: %+v/%v", after, err)
	}
}

func TestPendingInvalidInputsAndStorageFailuresReturnNoPartialPage(t *testing.T) {
	for _, mode := range []string{"nil context", "nil limit", "zero limit", "limit 65", "malformed cursor", "cancelled", "closed", "missing table", "missing table next page", "bad descriptor", "bad scan after good row"} {
		t.Run(mode, func(t *testing.T) {
			store := migratedTestStore(t)
			seedOperation(t, store, 1, "pending")
			ctx := context.Background()
			request := pendingRequest(nil, 64)
			switch mode {
			case "nil context":
				ctx = nil
			case "nil limit":
				request.Limit = nil
			case "zero limit":
				request = pendingRequest(nil, 0)
			case "limit 65":
				request = pendingRequest(nil, 65)
			case "malformed cursor":
				bad := "bad"
				request.Cursor = &bad
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "closed":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			case "missing table", "missing table next page":
				// Deliberate corruption injection; production connections keep FK on.
				if _, err := store.db.Exec("PRAGMA foreign_keys=OFF; DROP TABLE operations; PRAGMA foreign_keys=ON"); err != nil {
					t.Fatal(err)
				}
				if mode == "missing table next page" {
					cursor := encodeCursor(pendingCursor{after: 1, through: 2})
					request.Cursor = &cursor
				}
			case "bad descriptor":
				if _, err := store.db.Exec("PRAGMA ignore_check_constraints=ON; UPDATE operations SET revision=9007199254740992"); err != nil {
					t.Fatal(err)
				}
			case "bad scan after good row":
				seedOperation(t, store, 2, "pending")
				if _, err := store.db.Exec("PRAGMA ignore_check_constraints=ON; UPDATE operations SET revision=-1 WHERE creation_key=2"); err != nil {
					t.Fatal(err)
				}
			}
			page, err := store.ListPendingOperations(ctx, request)
			if err == nil || page.Operations != nil || page.Cursor != nil {
				t.Fatalf("partial success: %+v/%v", page, err)
			}
			if store.db.Stats().InUse != 0 {
				t.Fatal("failed read retained connection")
			}
		})
	}
}

func TestCursorBoundariesAndMalformedInput(t *testing.T) {
	good := pendingCursor{after: 1, through: math.MaxInt64}
	wire := encodeCursor(good)
	parsed, err := decodeCursor(wire)
	if err != nil || parsed != good {
		t.Fatalf("%+v/%v", parsed, err)
	}
	for _, raw := range []string{"", strings.Repeat("x", 22), strings.Repeat("x", 24), strings.Repeat("!", 23), wire + "=", encodeCursor(pendingCursor{after: 0, through: 1}), encodeCursor(pendingCursor{after: 2, through: 1})} {
		if _, err := decodeCursor(raw); err == nil {
			t.Fatalf("invalid cursor accepted: %q", raw)
		}
	}
	for _, mode := range []string{"version", "overflow", "noncanonical"} {
		bytes, _ := base64.RawURLEncoding.DecodeString(wire)
		if mode == "version" {
			bytes[0] = 2
		} else if mode == "overflow" {
			binary.BigEndian.PutUint64(bytes[9:17], math.MaxUint64)
		}
		raw := base64.RawURLEncoding.EncodeToString(bytes)
		if mode == "noncanonical" {
			raw = raw[:22] + "9"
		}
		if _, err := decodeCursor(raw); err == nil {
			t.Fatalf("%s accepted", mode)
		}
	}
}

func FuzzPendingCursor(f *testing.F) {
	for _, seed := range []string{"", encodeCursor(pendingCursor{after: 1, through: 1}), "malformed"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		cursor, err := decodeCursor(raw)
		if err == nil {
			if cursor.after < 1 || cursor.after > cursor.through || encodeCursor(cursor) != raw {
				t.Fatal("cursor invariant")
			}
		}
	})
}
