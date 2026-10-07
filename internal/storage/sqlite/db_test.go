package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUserDataPathRejectsInvalidResolverAndPreservesFailure(t *testing.T) {
	sentinel := errors.New("resolver failed")
	for _, tc := range []struct {
		name     string
		resolver func() (string, error)
		want     error
	}{
		{"nil", nil, ErrInvalidPath},
		{"empty", func() (string, error) { return "", nil }, ErrInvalidPath},
		{"relative", func() (string, error) { return "local", nil }, ErrInvalidPath},
		{"failure", func() (string, error) { return "", sentinel }, sentinel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, err := UserDataPath(tc.resolver)
			if path != "" || !errors.Is(err, tc.want) {
				t.Fatalf("path=%q err=%v", path, err)
			}
		})
	}
}

func TestUserDataPathUsesKoreanPerUserDirectory(t *testing.T) {
	base := filepath.Join(t.TempDir(), "한글 사용자")
	path, err := UserDataPath(func() (string, error) { return base, nil })
	if err != nil || path != filepath.Join(base, "Jackpot", "jackpot.sqlite3") {
		t.Fatalf("path=%q err=%v", path, err)
	}
	if _, err := os.Stat(base); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path resolver caused side effect: %v", err)
	}
}

func TestOpenRejectsInvalidPathWithoutCreatingFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, path := range []string{"", "relative.sqlite3", ":memory:"} {
		t.Run(path, func(t *testing.T) {
			store, err := Open(context.Background(), path)
			if store != nil || !errors.Is(err, ErrInvalidPath) {
				t.Fatalf("store=%v err=%v", store, err)
			}
		})
	}
	files, err := os.ReadDir(".")
	if err != nil || len(files) != 0 {
		t.Fatalf("invalid path created executable/cwd database: %v/%v", files, err)
	}
}

func TestOpenCancelledBeforeAdmissionHasNoFilesystemSideEffects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new-directory", "data.sqlite3")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store, err := Open(ctx, path)
	if store != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("store=%v err=%v", store, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancel created directory: %v", err)
	}
}

func TestOpenNilContextReturnsErrorWithoutPanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-created", "data.sqlite3")
	store, err := Open(nil, path)
	if store != nil || err == nil {
		t.Fatalf("store=%v err=%v", store, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nil context created directory: %v", err)
	}
}

func TestOpenExpiredDeadlineHasNoFilesystemSideEffects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-created", "data.sqlite3")
	ctx, cancel := context.WithDeadline(context.Background(), time.Time{})
	defer cancel()
	store, err := Open(ctx, path)
	if store != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("store=%v err=%v", store, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired deadline created directory: %v", err)
	}
}

func TestOpenFileBackedDurabilityAndReconnectPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "한글 데이터 # % &", "data.sqlite3")
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	for i := 0; i < 2; i++ {
		var foreignKeys, synchronous, busyTimeout int
		var journal string
		for _, q := range []struct {
			sql    string
			target any
		}{
			{"PRAGMA foreign_keys", &foreignKeys}, {"PRAGMA synchronous", &synchronous},
			{"PRAGMA busy_timeout", &busyTimeout}, {"PRAGMA journal_mode", &journal},
		} {
			if err := store.db.QueryRow(q.sql).Scan(q.target); err != nil {
				t.Fatal(err)
			}
		}
		if foreignKeys != 1 || synchronous != 2 || busyTimeout != 1000 || journal != "wal" {
			t.Fatalf("unsafe pragmas: %d/%d/%d/%s", foreignKeys, synchronous, busyTimeout, journal)
		}
		store.db.SetMaxIdleConns(0) // Force a fresh driver connection; DSN must reapply invariants.
	}
	if _, err := store.db.Exec("CREATE TABLE preserved(value TEXT NOT NULL); INSERT INTO preserved VALUES ('unchanged')"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if stats := store.db.Stats(); stats.OpenConnections != 0 || stats.InUse != 0 {
		t.Fatalf("connections leaked: %+v", stats)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("repeat close: %v", err)
	}
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err := reopened.db.QueryRow("SELECT value FROM preserved").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "unchanged" {
		t.Fatalf("reopen mutated data: %q", value)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	moved := path + ".closed"
	if err := os.Rename(path, moved); err != nil {
		t.Fatalf("file handle retained: %v", err)
	}
	if err := os.Remove(moved); err != nil {
		t.Fatal(err)
	}
}

func TestOpenDirectoryAndMalformedDatabaseFailWithoutPartialStore(t *testing.T) {
	for _, tc := range []string{"directory", "malformed"} {
		t.Run(tc, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "data.sqlite3")
			if tc == "directory" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(path, []byte("original non-SQLite bytes"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			store, err := Open(context.Background(), path)
			if store != nil || err == nil {
				t.Fatalf("store=%v err=%v", store, err)
			}
			if tc == "malformed" {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "original non-SQLite bytes" {
					t.Fatalf("existing file corrupted: %q/%v", data, err)
				}
			}
			if err := os.Rename(path, path+".released"); err != nil {
				t.Fatalf("open failure retained handle: %v", err)
			}
		})
	}
}

func TestOpenParentFileFailureIsRetryableAndPreservesOriginal(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "parent")
	if err := os.WriteFile(parent, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		store, err := Open(context.Background(), filepath.Join(parent, "data.sqlite3"))
		if store != nil || err == nil {
			t.Fatalf("attempt %d: %v/%v", i, store, err)
		}
	}
	data, err := os.ReadFile(parent)
	if err != nil || string(data) != "original" {
		t.Fatalf("parent changed: %q/%v", data, err)
	}
}
