//go:build windows

package singleinstance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func acquirePlatformLock(path string) (func() error, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create instance lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open instance lock: %w", err)
	}
	return lockFile(file)
}

func lockFile(file *os.File) (func() error, error) {
	handle := windows.Handle(file.Fd())
	err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if err != nil {
		closeErr := file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errors.Join(ErrAlreadyRunning, closeErr)
		}
		return nil, errors.Join(fmt.Errorf("acquire instance lock: %w", err), closeErr)
	}
	return func() error {
		// Explicit unlock gives normal shutdown deterministic handoff. Close is
		// still attempted if unlock fails; it also releases the kernel resource.
		unlockErr := windows.UnlockFileEx(handle, 0, 1, 0, &windows.Overlapped{})
		return errors.Join(unlockErr, file.Close())
	}, nil
}
