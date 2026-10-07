//go:build windows

package singleinstance

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func mustAcquire(t *testing.T, path string) *Lease {
	t.Helper()
	lease, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
	})
	return lease
}

func TestRealOSLockRejectsSecondOwnerUntilFirstCloses(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "new", "한국어", "data.sqlite3")
	first := mustAcquire(t, path)
	for name, duplicatePath := range map[string]string{
		"same path": path, "case alias": strings.ToUpper(path),
		"clean alias":                         filepath.Dir(path) + "\\unused\\..\\data.sqlite3",
		"same data folder different filename": filepath.Join(filepath.Dir(path), "other.sqlite3"),
	} {
		t.Run(name, func(t *testing.T) {
			second, err := Acquire(context.Background(), duplicatePath)
			if second != nil || !errors.Is(err, ErrAlreadyRunning) {
				if second != nil {
					_ = second.Close()
				}
				t.Fatalf("second=%v err=%v", second, err)
			}
		})
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := mustAcquire(t, path)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := Acquire(context.Background(), path)
	if third != nil || !errors.Is(err, ErrAlreadyRunning) {
		if third != nil {
			_ = third.Close()
		}
		t.Fatalf("old owner repeated Close surrendered new owner: third=%v err=%v", third, err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ".jackpot.instance.lock" {
		t.Fatalf("unexpected filesystem side effects: %v", entries)
	}
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(path), ".jackpot.instance.lock"))
	if err != nil || len(contents) != 0 {
		t.Fatalf("lock contents=%q err=%v", contents, err)
	}
	moved := filepath.Join(root, "moved")
	if err := os.Rename(filepath.Join(root, "new"), moved); err != nil {
		t.Fatalf("closed lock leaked handle: %v", err)
	}
}

func TestSeparateDataFoldersHaveIndependentOSLocks(t *testing.T) {
	root := t.TempDir()
	first := mustAcquire(t, filepath.Join(root, "first", "data.sqlite3"))
	second := mustAcquire(t, filepath.Join(root, "second", "data.sqlite3"))
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCancellationAfterAcquireKeepsOwnershipUntilClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sqlite3")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lease, err := Acquire(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	cancel()
	second, err := Acquire(context.Background(), path)
	if second != nil || !errors.Is(err, ErrAlreadyRunning) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("cancellation surrendered live DB ownership: second=%v err=%v", second, err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	mustAcquire(t, path)
}

func TestRealOSExistingLockContentIsPreserved(t *testing.T) {
	root := t.TempDir()
	lockPath := filepath.Join(root, ".jackpot.instance.lock")
	if err := os.WriteFile(lockPath, []byte("keep existing bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	lease := mustAcquire(t, filepath.Join(root, "data.sqlite3"))
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(lockPath)
	if err != nil || string(contents) != "keep existing bytes" {
		t.Fatalf("existing lock file changed: %q err=%v", contents, err)
	}
}

func TestRealOSAcquireContentionAllowsExactlyOneOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sqlite3")
	start := make(chan struct{})
	results := make(chan *Lease, 32)
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() {
			<-start
			lease, err := Acquire(context.Background(), path)
			if err != nil && !errors.Is(err, ErrAlreadyRunning) {
				t.Errorf("acquire: %v", err)
			}
			results <- lease
		})
	}
	close(start)
	group.Wait()
	close(results)
	owners := 0
	for lease := range results {
		if lease != nil {
			owners++
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if owners != 1 {
		t.Fatalf("owners=%d want=1", owners)
	}
	lease := mustAcquire(t, path)
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRealOSDirectoryCreationFailureCreatesNoLock(t *testing.T) {
	root := t.TempDir()
	obstruction := filepath.Join(root, "file")
	if err := os.WriteFile(obstruction, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	lease, err := Acquire(context.Background(), filepath.Join(obstruction, "data.sqlite3"))
	if lease != nil || err == nil || errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("lease=%v err=%v", lease, err)
	}
	contents, err := os.ReadFile(obstruction)
	if err != nil || string(contents) != "preserve" {
		t.Fatalf("contents=%q err=%v", contents, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
}

func TestRealOSOpenFailurePreservesExistingLockDirectory(t *testing.T) {
	root := t.TempDir()
	lockPath := filepath.Join(root, ".jackpot.instance.lock")
	if err := os.Mkdir(lockPath, 0700); err != nil {
		t.Fatal(err)
	}
	lease, err := Acquire(context.Background(), filepath.Join(root, "data.sqlite3"))
	if lease != nil || err == nil || errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("lease=%v err=%v", lease, err)
	}
	info, err := os.Stat(lockPath)
	if err != nil || !info.IsDir() {
		t.Fatalf("stat=%v err=%v", info, err)
	}
	if err := os.Rename(root, root+"-released"); err != nil {
		t.Fatalf("open failure leaked handle: %v", err)
	}
	if err := os.Rename(root+"-released", root); err != nil {
		t.Fatal(err)
	}
}

func TestRealOSClosedHandleFailureIsNotMisreportedAsDuplicate(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	release, err := lockFile(file)
	if release != nil || !errors.Is(err, windows.ERROR_INVALID_HANDLE) || !errors.Is(err, os.ErrClosed) || errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("release nil=%v err=%v", release == nil, err)
	}
}

func TestRealOSUnlockFailureStillClosesAndReportsBothErrors(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "closed-after-lock")
	if err != nil {
		t.Fatal(err)
	}
	release, err := lockFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	lease := &Lease{release: release}
	err = lease.Close()
	if !errors.Is(err, windows.ERROR_INVALID_HANDLE) || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("close error=%v", err)
	}
	if next := lease.Close(); next != err {
		t.Fatalf("idempotent close changed error: %v vs %v", next, err)
	}
	renamed := file.Name() + "-released"
	if err := os.Rename(file.Name(), renamed); err != nil {
		t.Fatalf("close failure leaked handle: %v", err)
	}
}

func TestRealOSRepeatedAcquisitionAndDuplicateRejectionLeakNoHandles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "data.sqlite3")
	warm := mustAcquire(t, path)
	if err := warm.Close(); err != nil {
		t.Fatal(err)
	}
	for n := range 100 {
		lease, err := Acquire(context.Background(), path)
		if err != nil {
			t.Fatalf("cycle=%d acquire=%v", n, err)
		}
		second, err := Acquire(context.Background(), path)
		if second != nil || !errors.Is(err, ErrAlreadyRunning) {
			t.Fatalf("cycle=%d second=%v err=%v", n, second, err)
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// Go's runtime can create unrelated kernel event/thread handles. Query the
	// owned file instead: without FILE_SHARE_DELETE, even one leaked file
	// handle makes this deterministic rename fail.
	lockPath := filepath.Join(root, ".jackpot.instance.lock")
	renamed := lockPath + "-closed"
	if err := os.Rename(lockPath, renamed); err != nil {
		t.Fatalf("100 acquire/duplicate/close cycles leaked a file handle: %v", err)
	}
	if err := os.Rename(renamed, lockPath); err != nil {
		t.Fatal(err)
	}
}

func TestRealOSCloseInvalidatesEachOwnedFileHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".jackpot.instance.lock")
	for n := range 100 {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		handle := windows.Handle(file.Fd())
		release, err := lockFile(file)
		if err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if kind, err := windows.GetFileType(handle); err != nil || kind != windows.FILE_TYPE_DISK {
			t.Fatalf("cycle=%d live handle type=%d err=%v", n, kind, err)
		}
		lease := &Lease{release: release}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := windows.GetFileType(handle); !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
			t.Fatalf("cycle=%d closed handle is still valid: %v", n, err)
		}
	}
}

// TestLockProcessHelper is invoked only by the independent OS process harness.
func TestLockProcessHelper(t *testing.T) {
	mode := os.Getenv("JACKPOT_INSTANCE_HELPER_MODE")
	if mode == "" {
		return
	}
	lease, err := Acquire(context.Background(), os.Getenv("JACKPOT_INSTANCE_HELPER_PATH"))
	if errors.Is(err, ErrAlreadyRunning) {
		if mode != "probe" {
			t.Fatal("holder unexpectedly found an owner")
		}
		fmt.Println("busy")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if mode == "probe" {
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
		fmt.Println("available")
		return
	}
	if mode != "hold" {
		t.Fatalf("unknown helper mode=%q", mode)
	}
	fmt.Println("ready")
	input := bufio.NewScanner(os.Stdin)
	for input.Scan() {
		switch input.Text() {
		case "close":
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
			fmt.Println("released")
		case "exit":
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
			return
		case "crash":
			os.Exit(23)
		default:
			t.Fatalf("unknown helper command=%q", input.Text())
		}
	}
	if err := input.Err(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

type holderProcess struct {
	command *exec.Cmd
	input   io.WriteCloser
	output  *bufio.Reader
	context context.Context
	stderr  bytes.Buffer
	waited  bool
}

func startHolder(t *testing.T, path string) *holderProcess {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	process := &holderProcess{context: ctx}
	process.command = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLockProcessHelper$")
	process.command.Env = append(os.Environ(), "JACKPOT_INSTANCE_HELPER_MODE=hold", "JACKPOT_INSTANCE_HELPER_PATH="+path)
	process.command.Stderr = &process.stderr
	input, err := process.command.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	process.input = input
	output, err := process.command.StdoutPipe()
	if err != nil {
		_ = input.Close()
		cancel()
		t.Fatal(err)
	}
	process.output = bufio.NewReader(output)
	if err := process.command.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = input.Close()
		if !process.waited {
			_ = process.command.Process.Kill()
			_ = process.command.Wait()
		}
		cancel()
	})
	process.line(t, "ready")
	return process
}

func (p *holderProcess) line(t *testing.T, want string) {
	t.Helper()
	// CommandContext kills the helper at the bounded deadline, which closes
	// this pipe. No extra reader goroutine needs joining during cleanup.
	line, err := p.output.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != want {
		t.Fatalf("holder output=%q error=%v context=%v want=%q", line, err, p.context.Err(), want)
	}
}

func (p *holderProcess) send(t *testing.T, command string) {
	t.Helper()
	if _, err := fmt.Fprintln(p.input, command); err != nil {
		t.Fatal(err)
	}
}

func (p *holderProcess) wait() error {
	err := p.command.Wait()
	p.waited = true
	return err
}

func probeProcess(t *testing.T, path, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLockProcessHelper$")
	command.Env = append(os.Environ(), "JACKPOT_INSTANCE_HELPER_MODE=probe", "JACKPOT_INSTANCE_HELPER_PATH="+path)
	output, err := command.CombinedOutput()
	if err != nil || !strings.HasPrefix(string(output), want+"\n") && !strings.HasPrefix(string(output), want+"\r\n") {
		t.Fatalf("probe output=%q err=%v want=%q", output, err, want)
	}
}

func TestRealProcessesRejectDuplicateAndHandoffBeforeOwnerExits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sqlite3")
	owner := startHolder(t, path)
	probeProcess(t, path, "busy")
	owner.send(t, "close")
	owner.line(t, "released")
	probeProcess(t, path, "available")
	owner.send(t, "exit")
	if err := owner.wait(); err != nil {
		t.Fatal(err)
	}
	probeProcess(t, path, "available")
}

func TestRealProcessesRecoverAfterOwnerExitsWithoutCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sqlite3")
	owner := startHolder(t, path)
	probeProcess(t, path, "busy")
	owner.send(t, "crash")
	err := owner.wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("crash exit=%v", err)
	}
	probeProcess(t, path, "available")
}

func TestRealProcessesRecoverAfterSupervisorKillsOwner(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "data.sqlite3")
	owner := startHolder(t, path)
	probeProcess(t, path, "busy")
	if err := owner.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := owner.wait(); err == nil {
		t.Fatal("killed owner exited successfully")
	}
	probeProcess(t, path, "available")
	if err := os.Rename(root, root+"-released"); err != nil {
		t.Fatalf("process cleanup leaked file handle: %v", err)
	}
	if err := os.Rename(root+"-released", root); err != nil {
		t.Fatal(err)
	}
}
