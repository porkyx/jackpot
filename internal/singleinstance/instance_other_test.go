//go:build !windows

package singleinstance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnsupportedPlatformFailsClosedWithoutFilesystemChanges(t *testing.T) {
	root := t.TempDir()
	lease, err := Acquire(context.Background(), filepath.Join(root, "new", "data.sqlite3"))
	if lease != nil || !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("lease=%v err=%v", lease, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
}
