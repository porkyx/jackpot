package platform

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

var errAtomicInjected = errors.New("injected atomic export failure")

type faultAtomicFile struct {
	*os.File
	stage  string
	fault  string
	cancel context.CancelFunc
	closes int
}

func (file *faultAtomicFile) Write(data []byte) (int, error) {
	if file.fault == "write" {
		n, _ := file.File.Write(data[:1])
		return n, errAtomicInjected
	}
	if file.fault == "short" {
		return file.File.Write(data[:len(data)-1])
	}
	return file.File.Write(data)
}
func (file *faultAtomicFile) Sync() error {
	if file.fault == "sync" {
		return errAtomicInjected
	}
	if file.cancel != nil {
		file.cancel()
	}
	return file.File.Sync()
}
func (file *faultAtomicFile) Close() error {
	file.closes++
	err := file.File.Close()
	if file.fault == "close" {
		return errors.Join(err, errAtomicInjected)
	}
	return err
}
func TestAtomicExportInjectedStageFailuresPreserveDestinationAndCleanEveryResource(t *testing.T) {
	for _, fault := range []string{"create", "write", "short", "sync", "close", "replace", "cancel-after-sync"} {
		t.Run(fault, func(t *testing.T) {
			directory := t.TempDir()
			target := filepath.Join(directory, "한국어 result.png")
			original := []byte("original complete result")
			if err := os.WriteFile(target, original, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var file *faultAtomicFile
			replaces, removes := 0, 0
			ops := atomicFileOps{
				create: func(dir, pattern string) (atomicTempFile, error) {
					if fault == "create" {
						return nil, errAtomicInjected
					}
					actual, err := os.CreateTemp(dir, pattern)
					if err != nil {
						return nil, err
					}
					file = &faultAtomicFile{File: actual, fault: fault}
					if fault == "cancel-after-sync" {
						file.cancel = cancel
					}
					return file, nil
				},
				remove: func(path string) error { removes++; return os.Remove(path) },
				replace: func(stage, path string) error {
					replaces++
					if fault == "replace" {
						return errAtomicInjected
					}
					return replaceFile(stage, path)
				},
			}
			err := writeFileAtomic(ctx, target, []byte("replacement complete bytes"), ops)
			if err == nil {
				t.Fatal("fault accepted")
			}
			if fault == "cancel-after-sync" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			actual, readErr := os.ReadFile(target)
			if readErr != nil || !bytes.Equal(actual, original) {
				t.Fatal("original modified", readErr)
			}
			entries, readErr := os.ReadDir(directory)
			if readErr != nil || len(entries) != 1 {
				t.Fatal("stage leaked", entries, readErr)
			}
			if file != nil && (file.closes != 1 || removes != 1) {
				t.Fatal("file cleanup", file.closes, removes)
			}
			expected := 0
			if fault == "replace" {
				expected = 1
			}
			if replaces != expected {
				t.Fatal("replace performed prematurely", replaces)
			}
			if fault == "create" && removes != 0 {
				t.Fatal("removed unowned stage")
			}
		})
	}
}
func TestAtomicExportCleanupFailureIsObservableAndOwnedStageCanBeRecovered(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "result.png")
	var file *faultAtomicFile
	ops := atomicFileOps{create: func(dir, pattern string) (atomicTempFile, error) {
		actual, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		file = &faultAtomicFile{File: actual, fault: "write"}
		return file, nil
	}, remove: func(string) error { return os.ErrPermission }, replace: func(string, string) error { t.Fatal("unexpected replace"); return nil }}
	err := writeFileAtomic(context.Background(), target, []byte("image"), ops)
	if !errors.Is(err, os.ErrPermission) || !errors.Is(err, errAtomicInjected) || file == nil || file.closes != 1 {
		t.Fatal(err)
	}
	if err = os.Remove(file.Name()); err != nil {
		t.Fatal("closed stage cannot be removed", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}
func TestAtomicExportFirstNthAndContinuousFailuresAllowSafeRetry(t *testing.T) {
	for _, mode := range []string{"first", "third", "continuous"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			target := filepath.Join(directory, "result.png")
			expected := []byte("original")
			if err := os.WriteFile(target, expected, 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			ops := atomicFileOps{create: func(dir, pattern string) (atomicTempFile, error) {
				calls++
				file, err := os.CreateTemp(dir, pattern)
				if err != nil {
					return nil, err
				}
				fault := ""
				if mode == "first" && calls == 1 || mode == "third" && calls == 3 || mode == "continuous" {
					fault = "write"
				}
				return &faultAtomicFile{File: file, fault: fault}, nil
			}, remove: os.Remove, replace: replaceFile}
			for attempt := 1; attempt <= 4; attempt++ {
				data := []byte{byte(attempt), 11, 22}
				err := writeFileAtomic(context.Background(), target, data, ops)
				failed := mode == "first" && attempt == 1 || mode == "third" && attempt == 3 || mode == "continuous"
				if failed != (err != nil) {
					t.Fatal("fault schedule", attempt, err)
				}
				if !failed {
					expected = data
				}
				actual, readErr := os.ReadFile(target)
				if readErr != nil || !bytes.Equal(actual, expected) {
					t.Fatal("partial replacement", attempt, readErr)
				}
				entries, readErr := os.ReadDir(directory)
				if readErr != nil || len(entries) != 1 {
					t.Fatal("stage leak", entries, readErr)
				}
			}
		})
	}
}
func TestAtomicExportConcurrentWritersPublishOneCompleteFileAndRemoveEveryStage(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "result.png")
	payloads := [][]byte{bytes.Repeat([]byte{1}, 1024), bytes.Repeat([]byte{2}, 2048)}
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, data := range payloads {
		group.Add(1)
		go func(data []byte) {
			defer group.Done()
			<-start
			results <- WriteFileAtomic(context.Background(), target, data)
		}(data)
	}
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	actual, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(actual, payloads[0]) && !bytes.Equal(actual, payloads[1]) {
		t.Fatal("mixed output", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatal("stage leak", entries, err)
	}
}

type publicationContext struct {
	context.Context
	calls   atomic.Int32
	checked chan struct{}
	release chan struct{}
}

func (ctx *publicationContext) Err() error {
	status := ctx.Context.Err()
	if ctx.calls.Add(1) == 2 {
		close(ctx.checked)
		<-ctx.release
	}
	return status
}
func TestAtomicExportCancelledWhilePublicationIsOccupiedRemovesStageWithoutReplacing(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "result.png")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	firstEntered, releaseFirst := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseFirst) })
	firstDone := make(chan error, 1)
	firstOps := atomicFileOps{create: func(dir, pattern string) (atomicTempFile, error) { return os.CreateTemp(dir, pattern) }, remove: os.Remove, replace: func(from, to string) error { close(firstEntered); <-releaseFirst; return replaceFile(from, to) }}
	go func() { firstDone <- writeFileAtomic(context.Background(), target, []byte("first complete"), firstOps) }()
	<-firstEntered
	inner, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiter := &publicationContext{Context: inner, checked: make(chan struct{}), release: make(chan struct{})}
	secondDone := make(chan error, 1)
	var replaced atomic.Int32
	secondOps := atomicFileOps{create: func(dir, pattern string) (atomicTempFile, error) { return os.CreateTemp(dir, pattern) }, remove: os.Remove, replace: func(from, to string) error { replaced.Add(1); return replaceFile(from, to) }}
	go func() {
		secondDone <- writeFileAtomic(waiter, target, []byte("cancelled must never appear"), secondOps)
	}()
	<-waiter.checked
	cancel()
	close(waiter.release)
	secondErr := <-secondDone
	releaseOnce.Do(func() { close(releaseFirst) })
	firstErr := <-firstDone
	if !errors.Is(secondErr, context.Canceled) || firstErr != nil || replaced.Load() != 0 {
		t.Fatal("cancelled publication waited or replaced", secondErr, firstErr, replaced.Load())
	}
	actual, err := os.ReadFile(target)
	if err != nil || string(actual) != "first complete" {
		t.Fatal("cancelled bytes replaced committed publication", string(actual), err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatal("cancelled waiter leaked stage", entries, err)
	}
	if err := WriteFileAtomic(context.Background(), target, []byte("safe retry")); err != nil {
		t.Fatal("publication permit leaked", err)
	}
}

type cancelOnPublicationCheck struct {
	context.Context
	cancel context.CancelFunc
	calls  atomic.Int32
}

func (ctx *cancelOnPublicationCheck) Err() error {
	if ctx.calls.Add(1) == 3 {
		ctx.cancel()
	}
	return ctx.Context.Err()
}
func TestAtomicExportCancellationAfterPublicationPermitPreservesBytesAndReleasesPermit(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "result.png")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	inner, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelOnPublicationCheck{Context: inner, cancel: cancel}
	if err := WriteFileAtomic(ctx, target, []byte("never publish")); !errors.Is(err, context.Canceled) {
		t.Fatal("publication acquired after cancellation", err)
	}
	actual, err := os.ReadFile(target)
	if err != nil || string(actual) != "original" {
		t.Fatal("cancelled publication changed original", string(actual), err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatal("stage remains", entries, err)
	}
	if err := WriteFileAtomic(context.Background(), target, []byte("safe retry")); err != nil {
		t.Fatal("cancelled publication leaked permit", err)
	}
}
