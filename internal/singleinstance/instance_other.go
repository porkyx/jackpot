//go:build !windows

package singleinstance

func acquirePlatformLock(string) (func() error, error) {
	return nil, ErrUnsupportedPlatform
}
