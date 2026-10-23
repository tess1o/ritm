//go:build unix

package main

import "syscall"

// restrictFileModes makes every file Ritm creates (databases, WAL, uploads, backups) private to
// its user: app.db holds password hashes and each user's database their health data.
func restrictFileModes() { syscall.Umask(0o077) }
