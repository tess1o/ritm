package store

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

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

// Keys created with the old "mcp" scope become "read" keys.
func TestReadScopeMigration(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	old := fstest.MapFS{}
	for _, name := range []string{"0001_init.sql", "0002_ingest_scope.sql"} {
		b, err := fs.ReadFile(migrations.App, name)
		if err != nil {
			t.Fatal(err)
		}
		old[name] = &fstest.MapFile{Data: b}
	}
	db, err := OpenPath(ctx, filepath.Join(dir, "app.db"), old)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.W.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, db_name, created_at, updated_at) VALUES (1, 'a', 'x', 'u1', 0, 0);
		INSERT INTO api_keys (user_id, name, prefix, scope, key_hash, created_at) VALUES
		  (1, 'Claude', 'p1', 'mcp', x'01', 0), (1, 'Kuma', 'p2', 'monitor', x'02', 0), (1, 'Shortcut', 'p3', 'ingest', x'03', 0)`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if db, err = OpenApp(ctx, dir); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.RO.QueryContext(ctx, "SELECT name, scope FROM api_keys ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name, scope string
		rows.Scan(&name, &scope)
		got = append(got, name+"="+scope)
	}
	if want := "Claude=read Kuma=monitor Shortcut=ingest"; strings.Join(got, " ") != want {
		t.Fatalf("keys after migration: %v, want %s", got, want)
	}
}
