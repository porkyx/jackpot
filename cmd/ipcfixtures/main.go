// ipcfixtures emits deterministic JSON through the actual SQLite read service.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/desktop"
	"github.com/porkyx/jackpot/internal/storage/sqlite"
)

func main() {
	out := flag.String("out", "testdata/ipc", "fixture directory")
	flag.Parse()
	if err := generate(*out, os.TempDir()); err != nil {
		log.Fatal(err)
	}
}

func generate(out, temporaryParent string) (err error) {
	if err := os.MkdirAll(out, 0700); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(temporaryParent, "jackpot-ipc-fixtures-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(temporary)) }()
	path := filepath.Join(temporary, "fixture.sqlite3")
	store, err := sqlite.Open(context.Background(), path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	if _, err := store.Migrate(context.Background()); err != nil {
		return err
	}
	now := time.Date(2026, 10, 6, 5, 0, 0, 0, time.UTC)
	notice := contracts.StateNotice{BackendSessionID: "session-fixture", EntityKind: contracts.DraftEntity, EntityID: "draft-fixture", Revision: contracts.Revision(contracts.MaxSafeInteger)}
	if err := notice.Validate(); err != nil {
		return err
	}
	if err := writeFixture(out, "state-notice.json", notice); err != nil {
		return err
	}
	service, err := desktop.NewService("session-fixture", func() time.Time { return now }, store)
	if err != nil {
		return err
	}
	recovery, err := desktop.NewRecoveryService("session-fixture", func() time.Time { return now }, store)
	if err != nil {
		return err
	}
	unknown, err := recovery.GetOperation(context.Background(), contracts.OperationLookupRequest{OperationID: "missing-operation"})
	if err != nil {
		return err
	}
	if err := writeFixture(out, "operation-unknown.json", unknown); err != nil {
		return err
	}
	bootstrap, err := service.Bootstrap(context.Background())
	if err != nil {
		return err
	}
	if err := writeFixture(out, "bootstrap-empty.json", bootstrap); err != nil {
		return err
	}
	limit := uint32(64)
	page, err := service.ListPendingOperations(context.Background(), contracts.PendingOperationsRequest{Limit: &limit})
	if err != nil {
		return err
	}
	if err := writeFixture(out, "pending-empty.json", page); err != nil {
		return err
	}

	// Only this offline fixture tool inserts synthetic records directly. Product
	// mutations use the atomic RoundLifecycle seam in their owning tickets.
	if err := seedBoundaryOperations(path); err != nil {
		return err
	}
	observation, err := recovery.GetOperation(context.Background(), contracts.OperationLookupRequest{OperationID: "operation-001"})
	if err != nil {
		return err
	}
	if err := writeFixture(out, "operation-pending.json", observation); err != nil {
		return err
	}
	bootstrap, err = service.Bootstrap(context.Background())
	if err != nil {
		return err
	}
	data := *bootstrap.Data
	data.ActiveDraft = &contracts.DraftSummary{
		DraftContext: contracts.DraftContext{BackendSessionID: "session-fixture", DraftID: "draft-fixture", Revision: contracts.Revision(contracts.MaxSafeInteger), ArticleGeneration: 2},
		State:        contracts.DraftReady, Snapshot: &contracts.SnapshotSummary{SnapshotID: "snapshot-fixture", CollectedAt: now, Complete: true, Pages: 20, AcceptedComments: 100000},
	}
	for i := 1; i <= 16; i++ {
		data.RecentResults = append(data.RecentResults, contracts.ResultReference{CollectionID: contracts.CollectionID(fmt.Sprintf("done-%03d", i)), RoundID: contracts.RoundID(fmt.Sprintf("done-round-%03d", i)), Revision: 1})
	}
	bootstrap.Data = &data
	if err := writeFixture(out, "bootstrap-boundaries.json", bootstrap); err != nil {
		return err
	}
	page, err = service.ListPendingOperations(context.Background(), contracts.PendingOperationsRequest{Limit: &limit})
	if err != nil {
		return err
	}
	if err := writeFixture(out, "pending-boundaries.json", page); err != nil {
		return err
	}
	if err := writeFixture(out, "bootstrap-error.json", contracts.Envelope[contracts.BootstrapData]{ResponseHeader: bootstrap.ResponseHeader, OK: false, Code: contracts.InvalidState, MessageKey: "InvalidState"}); err != nil {
		return err
	}
	if err := store.Close(); err != nil {
		return err
	}
	page, err = service.ListPendingOperations(context.Background(), contracts.PendingOperationsRequest{Limit: &limit})
	if err != nil {
		return err
	}
	return writeFixture(out, "pending-error.json", page)
}

func seedBoundaryOperations(path string) (err error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i := 1; i <= 65; i++ {
		c, r, op := fmt.Sprintf("collection-%03d", i), fmt.Sprintf("round-%03d", i), fmt.Sprintf("operation-%03d", i)
		if _, err := tx.Exec("INSERT INTO collections(id,source_draft_id,revision,frozen_json,created_at) VALUES (?,?,1,'{}','2026-10-06T05:00:00Z')", c, "draft-"+c); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO rounds(collection_id,id,number,state,version,attempt,input_json,active_operation_id) VALUES (?,?,1,'executing',1,1,'{}',?)", c, r, op); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO operations(operation_id,kind,collection_id,round_id,status,revision,public_fingerprint) VALUES (?,'CreateCollection',?,?,'pending',1,zeroblob(32))", fmt.Sprintf("operation-%03d", i), fmt.Sprintf("collection-%03d", i), fmt.Sprintf("round-%03d", i)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func writeFixture(out, name string, data any) (err error) {
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(out, ".fixture-*")
	if err != nil {
		return err
	}
	path := temporary.Name()
	defer os.Remove(path)
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(path, filepath.Join(out, name)); err != nil {
		return err
	}
	return nil
}
