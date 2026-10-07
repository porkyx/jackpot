package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

type atomicTempFile interface {
	Name() string
	Write([]byte) (int, error)
	Sync() error
	Close() error
}
type atomicFileOps struct {
	create  func(string, string) (atomicTempFile, error)
	remove  func(string) error
	replace func(string, string) error
}

// One publication at a time prevents competing Windows replacement calls.
// Staging remains concurrent, and a queued publication can be cancelled.
var atomicPublication = make(chan struct{}, 1)

// WriteFileAtomic stages the complete file beside the destination before replace.
// Failure before replace preserves the existing destination and removes the stage.
func WriteFileAtomic(ctx context.Context, path string, data []byte) error {
	return writeFileAtomic(ctx, path, data, atomicFileOps{
		create:  func(dir, pattern string) (atomicTempFile, error) { return os.CreateTemp(dir, pattern) },
		remove:  os.Remove,
		replace: replaceFile,
	})
}
func writeFileAtomic(ctx context.Context, path string, data []byte, ops atomicFileOps) (err error) {
	if ctx == nil || !filepath.IsAbs(path) || len(data) == 0 {
		return ErrInvalidSaveOutcome
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	file, err := ops.create(filepath.Dir(path), ".jackpot-export-*")
	if err != nil {
		return err
	}
	stage := file.Name()
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, file.Close())
		}
		if removeErr := ops.remove(stage); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	n, err := file.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return errors.New("incomplete export write")
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err = ctx.Err(); err != nil {
		return err
	}
	select {
	case atomicPublication <- struct{}{}:
		defer func() { <-atomicPublication }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return ops.replace(stage, path)
}
