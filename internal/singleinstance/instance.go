// Package singleinstance prevents two app processes from owning one data folder.
package singleinstance

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

var (
	ErrAlreadyRunning      = errors.New("Jackpot이 이미 실행 중입니다")
	ErrInvalidContext      = errors.New("single instance context is required")
	ErrInvalidPath         = errors.New("single instance requires an absolute database file path")
	ErrUnsupportedPlatform = errors.New("single instance locking is supported on Windows only")
)

// Lease owns the OS lock until Close. Close is safe concurrently and idempotent.
// A zero or nil Lease owns no resource and can also be closed.
type Lease struct {
	once    sync.Once
	release func() error
	err     error
}

func (l *Lease) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if l.release != nil {
			l.err = l.release()
		}
	})
	return l.err
}

// Acquire must run before opening or migrating the database. The lock belongs
// to its directory, so the stable lock file is never deleted or replaced while
// another process may have it open. A process exit releases its OS lock.
// ctx controls acquisition only; subsequent cancellation does not surrender
// ownership while the database is still open.
func Acquire(ctx context.Context, databasePath string) (*Lease, error) {
	return acquire(ctx, databasePath, acquirePlatformLock)
}

func acquire(ctx context.Context, databasePath string, lock func(string) (func() error, error)) (*Lease, error) {
	if ctx == nil {
		return nil, ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(databasePath) > 32767 || !utf8.ValidString(databasePath) || strings.IndexByte(databasePath, 0) >= 0 || !filepath.IsAbs(databasePath) {
		return nil, ErrInvalidPath
	}
	cleaned := filepath.Clean(databasePath)
	if filepath.Dir(cleaned) == cleaned {
		return nil, ErrInvalidPath
	}
	release, err := lock(filepath.Join(filepath.Dir(cleaned), ".jackpot.instance.lock"))
	if err != nil {
		return nil, err
	}
	if release == nil {
		panic("single instance lock succeeded without an owner")
	}
	lease := &Lease{release: release}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, lease.Close())
	}
	return lease, nil
}
