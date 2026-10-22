package store

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"

	"ritm/migrations"
)

func TestMigrationsApplyOnce(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	open := map[string]func() (*DB, error){
		"app":  func() (*DB, error) { return OpenApp(ctx, dir) },
		"user": func() (*DB, error) { return OpenUser(ctx, dir, "u1") },
	}
	want := map[string]int{}
	for name, schema := range map[string]fs.FS{"app": migrations.App, "user": migrations.User} {
		files, _ := fs.Glob(schema, "*.sql")
		want[name] = len(files)
	}
	for name, fn := range open {
		for i := range 2 {
			db, err := fn()
			if err != nil {
				t.Fatalf("%s open #%d: %v", name, i+1, err)
			}
			var n int
			if err := db.RO.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&n); err != nil {
				t.Fatal(err)
			}
			db.Close()
			if n != want[name] {
				t.Fatalf("%s open #%d: %d migrations recorded, want %d", name, i+1, n, want[name])
			}
		}
	}
}

func TestLockFileExcludesOtherHolders(t *testing.T) {
	p := filepath.Join(t.TempDir(), "u.db"+ImportLockSuffix)
	unlock, err := LockFile(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockFile(p, false); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock: %v, want ErrLocked", err)
	}
	unlock()
	unlock2, err := LockFile(p, true)
	if err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}
	unlock2()
}
