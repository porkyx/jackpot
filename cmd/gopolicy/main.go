package main

import (
	"errors"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	root := flag.String("root", ".", "project root")
	flag.Parse()
	if err := checkProject(*root, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func forbiddenImport(file, path string) bool {
	file = filepath.ToSlash(file)
	sqlImport := path == "database/sql" || path == "modernc.org/sqlite" || strings.HasPrefix(path, "github.com/porkyx/jackpot/internal/storage/")
	if strings.HasPrefix(file, "internal/desktop/") || strings.HasPrefix(file, "internal/scheduler/") {
		return sqlImport || path == "github.com/porkyx/jackpot/internal/draw"
	}
	if strings.HasPrefix(file, "internal/roundlifecycle/") {
		return sqlImport || strings.HasPrefix(path, "github.com/wailsapp/")
	}
	return false
}
func checkProject(root string, output io.Writer) error {
	if root == "" || output == nil {
		return errors.New("invalid policy arguments")
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return err
	}
	violations := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || entry.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		tree, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, declaration := range tree.Imports {
			imported, err := strconv.Unquote(declaration.Path.Value)
			if err != nil {
				return err
			}
			if forbiddenImport(relative, imported) {
				violations++
				if _, err := fmt.Fprintf(output, "%s: forbidden dependency %s\n", filepath.ToSlash(relative), imported); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if violations != 0 {
		return fmt.Errorf("%d architectural dependency violation(s)", violations)
	}
	return nil
}
