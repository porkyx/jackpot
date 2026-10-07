package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const SchemaVersion = 3

var ErrNewerSchema = errors.New("database schema is newer than this application")

var migrationOne = []string{
	`CREATE TABLE operations (
 creation_key INTEGER PRIMARY KEY AUTOINCREMENT,
 operation_id TEXT NOT NULL UNIQUE CHECK(length(operation_id)>0),
 kind TEXT NOT NULL CHECK(kind IN ('CreateCollection','Rerun','SetSchedule','CancelSchedule','RetryRound','ExecuteDue')),
 collection_id TEXT NOT NULL CHECK(length(collection_id)>0),
 round_id TEXT NOT NULL CHECK(length(round_id)>0),
 status TEXT NOT NULL CHECK(status IN ('pending','succeeded','failed')),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 0 AND 9007199254740991),
 public_fingerprint BLOB NOT NULL CHECK(length(public_fingerprint)=32)
 ) STRICT`,
	`CREATE INDEX pending_operations_key ON operations(creation_key) WHERE status='pending'`,
	`CREATE TRIGGER immutable_operation_key BEFORE UPDATE OF creation_key,operation_id,kind,collection_id,round_id,public_fingerprint ON operations
 BEGIN SELECT RAISE(ABORT,'immutable operation identity'); END`,
}

// Migrate keeps a verified, standalone backup before modifying an older DB.
// Product writers are added by the owning domain tickets. The foundation schema
// already enforces durable identities, immutable snapshots and composite keys.
func (store *Store) Migrate(ctx context.Context) (string, error) {
	return store.migrate(ctx, nil)
}

func (store *Store) migrate(ctx context.Context, steps []string) (string, error) {
	if ctx == nil {
		return "", errors.New("nil migration context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var version int
	if err := store.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return "", err
	}
	if version > SchemaVersion {
		return "", ErrNewerSchema
	}
	if version == SchemaVersion {
		return "", nil
	}
	if steps == nil {
		if version == 0 {
			steps = append(append(append([]string{}, migrationOne...), migrationTwo...), migrationThree...)
		} else if version == 1 {
			steps = append(append([]string{}, migrationTwo...), migrationThree...)
		} else {
			steps = migrationThree
		}
	}
	backup, err := store.backup(ctx, version)
	if err != nil {
		return "", err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return backup, err
	}
	defer tx.Rollback()
	for _, step := range steps {
		if _, err := tx.ExecContext(ctx, step); err != nil {
			return backup, err
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", SchemaVersion)); err != nil {
		return backup, err
	}
	if err := tx.Commit(); err != nil {
		return backup, err
	}
	return backup, nil
}

func (store *Store) backup(ctx context.Context, version int) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(store.path), "jackpot-backup-*.sqlite3")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	verified := false
	defer func() {
		if !verified {
			_ = os.Remove(path)
		}
	}()
	if _, err := store.db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return "", err
	}
	db, err := sql.Open("sqlite", fileURI(path)+"?mode=ro")
	if err != nil {
		return "", err
	}
	defer db.Close()
	var integrity string
	var actualVersion int
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return "", err
	}
	if integrity != "ok" {
		return "", errors.New("backup integrity check failed")
	}
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&actualVersion); err != nil {
		return "", err
	}
	if actualVersion != version {
		return "", errors.New("backup schema version mismatch")
	}
	verified = true
	return path, nil
}
