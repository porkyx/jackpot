package singleinstance

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcquireRejectsInvalidContextBeforeLock(t *testing.T) {
	called := false
	lease, err := acquire(nil, filepath.Join(t.TempDir(), "data.sqlite3"), func(string) (func() error, error) {
		called = true
		return func() error { return nil }, nil
	})
	if lease != nil || !errors.Is(err, ErrInvalidContext) || called {
		t.Fatalf("lease=%v err=%v lock called=%v", lease, err, called)
	}
}

func TestAcquireRejectsCancellationBeforeLock(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancelled", true: "expired deadline"}[deadline], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			want := context.Canceled
			if deadline {
				ctx, cancel = context.WithDeadline(context.Background(), time.Unix(0, 0))
				want = context.DeadlineExceeded
			}
			cancel()
			called := false
			lease, err := acquire(ctx, filepath.Join(t.TempDir(), "data.sqlite3"), func(string) (func() error, error) {
				called = true
				return func() error { return nil }, nil
			})
			if lease != nil || !errors.Is(err, want) || called {
				t.Fatalf("lease=%v err=%v lock called=%v", lease, err, called)
			}
		})
	}
}

func TestAcquireRejectsMalformedPathBeforeLock(t *testing.T) {
	absolute := filepath.Join(t.TempDir(), "data.sqlite3")
	volumeRoot := filepath.VolumeName(absolute) + string(filepath.Separator)
	for name, path := range map[string]string{
		"empty": "", "relative": "data.sqlite3", "dot": ".", "root": volumeRoot,
		"NUL": absolute + "\x00", "invalid UTF8": absolute + "\xff", "overlong": absolute + strings.Repeat("x", 32768),
	} {
		t.Run(name, func(t *testing.T) {
			called := false
			lease, err := acquire(context.Background(), path, func(string) (func() error, error) {
				called = true
				return func() error { return nil }, nil
			})
			if lease != nil || !errors.Is(err, ErrInvalidPath) || called {
				t.Fatalf("lease=%v err=%v lock called=%v", lease, err, called)
			}
		})
	}
}

func TestAcquireCanonicalizesDirectoryAndAcceptsMaximumPathSize(t *testing.T) {
	root := t.TempDir()
	for name, path := range map[string]string{
		"Korean":         filepath.Join(root, "한글", "data.sqlite3"),
		"clean parent":   filepath.Join(root, "unused") + string(filepath.Separator) + ".." + string(filepath.Separator) + "data.sqlite3",
		"maximum length": filepath.Join(root, strings.Repeat("x", 32767-len(root)-1)),
	} {
		t.Run(name, func(t *testing.T) {
			calls, releases := 0, 0
			lease, err := acquire(context.Background(), path, func(lockPath string) (func() error, error) {
				calls++
				want := filepath.Join(filepath.Dir(filepath.Clean(path)), ".jackpot.instance.lock")
				if lockPath != want {
					t.Fatalf("path=%q want=%q", lockPath, want)
				}
				return func() error { releases++; return nil }, nil
			})
			if err != nil || lease == nil || calls != 1 || releases != 0 {
				t.Fatalf("lease=%v err=%v calls=%d releases=%d", lease, err, calls, releases)
			}
			if err := lease.Close(); err != nil || releases != 1 {
				t.Fatalf("close=%v releases=%d", err, releases)
			}
		})
	}
}

func TestAcquirePropagatesFirstNthAndContinuousLockFailure(t *testing.T) {
	for name, fails := range map[string]func(int) bool{
		"first":      func(n int) bool { return n == 1 },
		"Nth":        func(n int) bool { return n == 3 },
		"continuous": func(int) bool { return true },
	} {
		t.Run(name, func(t *testing.T) {
			failure := errors.New("controlled lock failure")
			calls, releases, successes := 0, 0, 0
			lock := func(string) (func() error, error) {
				calls++
				if fails(calls) {
					return nil, failure
				}
				return func() error { releases++; return nil }, nil
			}
			path := filepath.Join(t.TempDir(), "data.sqlite3")
			for n := 1; n <= 4; n++ {
				lease, err := acquire(context.Background(), path, lock)
				if fails(n) {
					if lease != nil || !errors.Is(err, failure) {
						t.Fatalf("call=%d lease=%v err=%v", n, lease, err)
					}
				} else {
					successes++
					if lease == nil || err != nil {
						t.Fatalf("call=%d lease=%v err=%v", n, lease, err)
					}
					if err := lease.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			if calls != 4 || releases != successes {
				t.Fatalf("calls=%d releases=%d successes=%d", calls, releases, successes)
			}
		})
	}
}

func TestAcquireCancellationAfterLockReleasesOwner(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleanup succeeds", true: "cleanup error preserved"}[cleanupFails], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("controlled close failure")
			releases := 0
			lease, err := acquire(ctx, filepath.Join(t.TempDir(), "data.sqlite3"), func(string) (func() error, error) {
				cancel()
				return func() error {
					releases++
					if cleanupFails {
						return failure
					}
					return nil
				}, nil
			})
			if lease != nil || !errors.Is(err, context.Canceled) || errors.Is(err, failure) != cleanupFails || releases != 1 {
				t.Fatalf("lease=%v err=%v releases=%d", lease, err, releases)
			}
		})
	}
}

func TestSuccessfulLockWithoutOwnerIsLoudInvariantViolation(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("missing owner did not panic")
		}
	}()
	_, _ = acquire(context.Background(), filepath.Join(t.TempDir(), "data.sqlite3"), func(string) (func() error, error) { return nil, nil })
}

func TestCloseNilAndZeroLeaseOwnNoResource(t *testing.T) {
	for _, lease := range []*Lease{nil, {}} {
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentCloseRunsReleaseOnceAndPreservesFailure(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fails], func(t *testing.T) {
			failure := errors.New("release failure")
			var calls atomic.Int32
			lease := &Lease{release: func() error {
				calls.Add(1)
				if fails {
					return failure
				}
				return nil
			}}
			start := make(chan struct{})
			var group sync.WaitGroup
			for range 64 {
				group.Go(func() {
					<-start
					if err := lease.Close(); errors.Is(err, failure) != fails {
						t.Errorf("close error=%v", err)
					}
				})
			}
			close(start)
			group.Wait()
			if calls.Load() != 1 {
				t.Fatalf("release calls=%d", calls.Load())
			}
		})
	}
}

func FuzzAcquirePath(f *testing.F) {
	for _, path := range []string{"", ".", "relative.sqlite", "C:\\data\\db.sqlite3", "/data/db.sqlite3", "\x00", "\xff"} {
		f.Add(path)
	}
	f.Fuzz(func(t *testing.T, path string) {
		calls, releases := 0, 0
		lease, err := acquire(context.Background(), path, func(string) (func() error, error) {
			calls++
			return func() error { releases++; return nil }, nil
		})
		if err != nil {
			if lease != nil || !errors.Is(err, ErrInvalidPath) || calls != 0 || releases != 0 {
				t.Fatalf("invalid result lease=%v err=%v calls=%d releases=%d", lease, err, calls, releases)
			}
			return
		}
		if lease == nil || calls != 1 {
			t.Fatalf("valid result lease=%v calls=%d", lease, calls)
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
		if releases != 1 {
			t.Fatalf("release count=%d", releases)
		}
	})
}
