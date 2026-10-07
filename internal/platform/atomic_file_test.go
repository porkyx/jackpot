package platform_test

import (
	"context"
	"github.com/porkyx/jackpot/internal/platform"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicPNGWriteReplacesOnlyAfterCompleteStageAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.png")
	for _, data := range []string{"first complete image", "second complete image"} {
		if e := platform.WriteFileAtomic(context.Background(), path, []byte(data)); e != nil {
			t.Fatal(e)
		}
		actual, e := os.ReadFile(path)
		if e != nil || string(actual) != data {
			t.Fatalf("%q/%v", actual, e)
		}
		entries, e := os.ReadDir(dir)
		if e != nil || len(entries) != 1 {
			t.Fatal("stage leaked")
		}
	}
}
func TestAtomicPNGCancelledOrInvalidWritePreservesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.png")
	if e := os.WriteFile(path, []byte("existing"), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		ctx  context.Context
		path string
		data []byte
	}{{nil, path, []byte("new")}, {ctx, path, []byte("new")}, {context.Background(), "relative", []byte("new")}, {context.Background(), path, nil}, {context.Background(), filepath.Join(dir, "missing", "result.png"), []byte("new")}} {
		if e := platform.WriteFileAtomic(c.ctx, c.path, c.data); e == nil {
			t.Fatal("invalid write accepted")
		}
	}
	actual, e := os.ReadFile(path)
	if e != nil || string(actual) != "existing" {
		t.Fatal("existing changed")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("stage leaked")
	}
}
func TestAtomicPNGReplaceFailureRemovesStageAndPreservesDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "result.png")
	if e := os.Mkdir(target, 0700); e != nil {
		t.Fatal(e)
	}
	if e := platform.WriteFileAtomic(context.Background(), target, []byte("image")); e == nil {
		t.Fatal("directory replaced")
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 1 || !entries[0].IsDir() {
		t.Fatal("stage leaked or directory changed")
	}
}
