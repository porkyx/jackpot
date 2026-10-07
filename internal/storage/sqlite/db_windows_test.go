//go:build windows

package sqlite

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestOpenOSWriteRefusalPreservesDatabaseAndReleasesFailedConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "한글 읽기 전용", "data.sqlite3")
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	execSQL(t, store.db, "CREATE TABLE preserved(value TEXT NOT NULL); INSERT INTO preserved VALUES ('original')")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	// The OS permits the read-only version probe but denies every writable open.
	// This is a real sharing refusal, independent of administrator ACL privileges.
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	owned := true
	t.Cleanup(func() {
		if owned {
			if err := windows.CloseHandle(handle); err != nil {
				t.Error(err)
			}
		}
	})
	for attempt := 0; attempt < 3; attempt++ {
		refused, err := Open(context.Background(), path)
		if refused != nil || err == nil {
			if refused != nil {
				if closeErr := refused.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			}
			t.Fatalf("attempt %d: refused open returned store=%v err=%v", attempt, refused, err)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("attempt %d changed database: %v", attempt, err)
		}
	}
	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	owned = false
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("write refusal was not retryable: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	var value string
	if err := reopened.db.QueryRow("SELECT value FROM preserved").Scan(&value); err != nil || value != "original" {
		t.Fatalf("preserved=%q err=%v", value, err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".released"); err != nil {
		t.Fatalf("failed writable opens retained a file handle: %v", err)
	}
}
