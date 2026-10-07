package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDependencyPolicyChecksEveryOwnerAndIndependentDependency(t *testing.T) {
	for _, file := range []string{"internal/desktop/service.go", "internal/scheduler/worker.go", "internal/roundlifecycle/ports.go"} {
		for _, dependency := range []string{"database/sql", "modernc.org/sqlite", "github.com/porkyx/jackpot/internal/storage/sqlite"} {
			if !forbiddenImport(file, dependency) {
				t.Fatalf("allowed %s -> %s", file, dependency)
			}
		}
		for _, dependency := range []string{"context", "time", "github.com/porkyx/jackpot/internal/contracts"} {
			if forbiddenImport(file, dependency) {
				t.Fatalf("rejected %s -> %s", file, dependency)
			}
		}
	}
	for _, file := range []string{"internal/desktop/service.go", "internal/scheduler/worker.go"} {
		if !forbiddenImport(file, "github.com/porkyx/jackpot/internal/draw") {
			t.Fatal("draw bypass allowed")
		}
		if forbiddenImport(file, "github.com/wailsapp/wails/v3/pkg/application") {
			t.Fatal("owning adapter denied")
		}
	}
	if !forbiddenImport("internal/roundlifecycle/ports.go", "github.com/wailsapp/wails/v3/pkg/application") {
		t.Fatal("domain depends on Wails")
	}
	if forbiddenImport("internal/roundlifecycle/draw.go", "github.com/porkyx/jackpot/internal/draw") {
		t.Fatal("domain cannot call draw")
	}
	for _, file := range []string{"main.go", "internal/storage/sqlite/db.go", "cmd/ipcfixtures/main.go"} {
		if forbiddenImport(file, "database/sql") {
			t.Fatal("SQL owning layer rejected")
		}
	}
	if forbiddenImport("internal/desktopish/example.go", "database/sql") {
		t.Fatal("prefix collision")
	}
}
func writePolicyFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestPolicyParsesAliasesDotAndBlankImportsAndSkipsGeneratedDependencies(t *testing.T) {
	root := t.TempDir()
	writePolicyFile(t, root, "go.mod", "module example\n")
	writePolicyFile(t, root, "internal/desktop/service.go", "package desktop\nimport (db \"database/sql\"; _ \"modernc.org/sqlite\"; . \"github.com/porkyx/jackpot/internal/draw\")\n")
	for _, file := range []string{".task/bad.go", "vendor/bad.go", "frontend/node_modules/bad.go", "internal/desktop/service_test.go"} {
		writePolicyFile(t, root, file, "not go")
	}
	writePolicyFile(t, root, "notes.txt", "not go")
	var output bytes.Buffer
	if err := checkProject(root, &output); err == nil || strings.Count(output.String(), "forbidden dependency") != 3 {
		t.Fatalf("policy skipped alias/blank/dot imports: %v/%q", err, output.String())
	}
	writePolicyFile(t, root, "internal/desktop/service.go", "package desktop\nimport \"context\"\n")
	output.Reset()
	if err := checkProject(root, &output); err != nil || output.Len() != 0 {
		t.Fatalf("valid project failed: %v/%q", err, output.String())
	}
}

type failedWriter struct{ err error }

func (w failedWriter) Write([]byte) (int, error) { return 0, w.err }
func TestPolicyRejectsMissingRootMalformedSourceAndOutputFailure(t *testing.T) {
	if err := checkProject("", new(bytes.Buffer)); err == nil {
		t.Fatal("empty root accepted")
	}
	if err := checkProject(t.TempDir(), nil); err == nil {
		t.Fatal("nil writer accepted")
	}
	root := t.TempDir()
	if err := checkProject(root, new(bytes.Buffer)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	writePolicyFile(t, root, "go.mod", "module example\n")
	writePolicyFile(t, root, "main.go", "bad source")
	if err := checkProject(root, new(bytes.Buffer)); err == nil {
		t.Fatal("malformed source accepted")
	}
	if err := os.Remove(filepath.Join(root, "main.go")); err != nil {
		t.Fatal(err)
	}
	writePolicyFile(t, root, "internal/desktop/service.go", "package desktop\nimport \"database/sql\"\n")
	sentinel := errors.New("output unavailable")
	if err := checkProject(root, failedWriter{sentinel}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
}
