package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/porkyx/jackpot/internal/contracts"
)

func TestGeneratedFixtureBytesAreDeterministicAndTemporaryResourcesRemoved(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "fixtures")
	temporary := filepath.Join(root, "temporary")
	if err := os.Mkdir(temporary, 0700); err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for run := 0; run < 2; run++ {
		if err := generate(out, temporary); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"bootstrap-empty.json", "bootstrap-boundaries.json", "bootstrap-error.json", "pending-empty.json", "pending-boundaries.json", "pending-error.json", "state-notice.json", "operation-unknown.json", "operation-pending.json"} {
			data, err := os.ReadFile(filepath.Join(out, name))
			if err != nil {
				t.Fatal(err)
			}
			if run == 0 {
				before[name] = data
			} else if !bytes.Equal(before[name], data) {
				t.Fatalf("non-deterministic fixture %s", name)
			}
		}
		entries, err := os.ReadDir(temporary)
		if err != nil || len(entries) != 0 {
			t.Fatalf("temp resources leaked: %v/%v", entries, err)
		}
	}
	var page contracts.PendingOperationsResponse
	if err := json.Unmarshal(before["pending-boundaries.json"], &page); err != nil {
		t.Fatal(err)
	}
	if !page.OK || page.Data == nil || len(page.Data.Operations) != 64 || page.Data.Cursor == nil {
		t.Fatalf("actual DB page missing: %+v", page)
	}
	if err := page.Validate(); err != nil {
		t.Fatal(err)
	}
	var failed contracts.PendingOperationsResponse
	if err := json.Unmarshal(before["pending-error.json"], &failed); err != nil {
		t.Fatal(err)
	}
	if failed.OK || failed.Data != nil || failed.Code != contracts.StorageUnavailable {
		t.Fatal("DB failure disguised as empty success")
	}
}

func TestFixtureFailureCleansDatabaseAndPartiallyWrittenTemporaryFile(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "out")
	temporary := filepath.Join(root, "temporary")
	if err := os.MkdirAll(filepath.Join(out, "pending-boundaries.json"), 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(out, "pending-boundaries.json", "preserved")
	if err := os.WriteFile(sentinel, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(temporary, 0700); err != nil {
		t.Fatal(err)
	}
	if err := generate(out, temporary); err == nil {
		t.Fatal("fixture target directory accepted")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "original" {
		t.Fatal("existing target damaged")
	}
	entries, err := os.ReadDir(temporary)
	if err != nil || len(entries) != 0 {
		t.Fatalf("DB cleanup failed: %v/%v", entries, err)
	}
	files, err := filepath.Glob(filepath.Join(out, ".fixture-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("partial file cleanup failed: %v/%v", files, err)
	}
}

func TestFixtureDirectoryAndSerializationFailuresHaveNoTemporaryLeak(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	if err := os.WriteFile(parent, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := generate(filepath.Join(parent, "out"), root); err == nil {
		t.Fatal("file parent accepted")
	}
	out := filepath.Join(root, "out")
	if err := os.Mkdir(out, 0700); err != nil {
		t.Fatal(err)
	}
	if err := generate(out, parent); err == nil {
		t.Fatal("invalid temporary parent accepted")
	}
	if err := writeFixture(out, "unsupported.json", make(chan int)); err == nil {
		t.Fatal("unsupported JSON accepted")
	}
	if err := writeFixture(parent, "test.json", struct{}{}); err == nil {
		t.Fatal("non-directory output accepted")
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 0 {
		t.Fatalf("orphaned file: %v/%v", entries, err)
	}
}
