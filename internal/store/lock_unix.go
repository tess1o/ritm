//go:build unix

package store

import (
	"errors"
	"os"
	"syscall"
)

// ErrLocked is returned by LockFile when another holder has the lock and wait is false.
var ErrLocked = errors.New("locked by another process")

// LockFile takes an exclusive lock on path, creating the file if needed. With wait it blocks
// until the lock is free; otherwise it fails with ErrLocked. The lock works across processes
// (server and CLI) and the OS releases it if the process dies.
func LockFile(path string, wait bool) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
