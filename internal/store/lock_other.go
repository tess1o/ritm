//go:build !unix

package store

import "errors"

// ErrLocked is returned by LockFile when another holder has the lock and wait is false.
var ErrLocked = errors.New("locked by another process")

// LockFile is a no-op where flock is unavailable; in-process locks still serialise the server.
func LockFile(string, bool) (func(), error) { return func() {}, nil }
