// Package sqlite owns connections and SQL. Desktop and scheduler use domain ports.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

var ErrInvalidPath = errors.New("invalid app data path")

// UserDataPath resolves a per-user application directory, never the executable directory.
func UserDataPath(resolve func() (string, error)) (string, error) {
	if resolve == nil {
		return "", ErrInvalidPath
	}
	base, err := resolve()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(base) {
		return "", ErrInvalidPath
	}
	return filepath.Join(base, "Jackpot", "jackpot.sqlite3"), nil
}

type Store struct {
	db   *sql.DB
	path string
}

func fileURI(path string) string {
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	return (&url.URL{Scheme: "file", Path: uriPath}).String()
}

func checkExistingVersion(ctx context.Context, path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", fileURI(path)+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > SchemaVersion {
		return ErrNewerSchema
	}
	return nil
}

func Open(ctx context.Context, path string) (*Store, error) {
	if ctx == nil {
		return nil, errors.New("nil database context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(path) {
		return nil, ErrInvalidPath
	}
	if err := checkExistingVersion(ctx, path); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	query := url.Values{}
	for _, pragma := range []string{"foreign_keys(1)", "journal_mode(WAL)", "synchronous(FULL)", "busy_timeout(1000)"} {
		query.Add("_pragma", pragma)
	}
	query.Set("_txlock", "immediate")
	db, err := sql.Open("sqlite", fileURI(path)+"?"+query.Encode())
	if err != nil {
		return nil, err
	}
	// One connection serializes this app's short transactions. Every replacement
	// connection receives the same DSN pragmas; expensive work stays outside SQL.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	failed := true
	defer func() {
		if failed {
			_ = db.Close()
		}
	}()
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	var foreignKeys, synchronous, busyTimeout int
	var journal string
	for _, check := range []struct {
		query  string
		target any
	}{
		{"PRAGMA foreign_keys", &foreignKeys}, {"PRAGMA synchronous", &synchronous},
		{"PRAGMA busy_timeout", &busyTimeout}, {"PRAGMA journal_mode", &journal},
	} {
		if err := db.QueryRowContext(ctx, check.query).Scan(check.target); err != nil {
			return nil, err
		}
	}
	if foreignKeys != 1 || synchronous != 2 || busyTimeout != 1000 || journal != "wal" {
		return nil, errors.New("unsafe SQLite durability configuration")
	}
	// SQLite can fall back to a read-only file while all the read pragmas still
	// pass. BEGIN IMMEDIATE alone also succeeds in that mode. Force one header
	// write inside a transaction that is always rolled back before returning.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version=0"); err != nil {
		return nil, errors.Join(err, tx.Rollback())
	}
	if err := tx.Rollback(); err != nil {
		return nil, err
	}
	failed = false
	return &Store{db: db, path: path}, nil
}

func (store *Store) Close() error { return store.db.Close() }
