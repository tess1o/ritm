package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"ritm/internal/store"
)

func TestBackupCopiesEveryDatabase(t *testing.T) {
	ctx := context.Background()
	data, out := t.TempDir(), filepath.Join(t.TempDir(), "backup")
	t.Setenv("RITM_DATA_DIR", data)
	t.Setenv("RITM_INIT_USER", "")
	t.Setenv("RITM_INIT_PASSWORD", "")
	t.Setenv("RITM_LOG_LEVEL", "error")

	a, err := newApp(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, u := range []string{"alice", "bob"} {
		if _, err := a.auth.EnsureUser(ctx, u, "password1"); err != nil {
			t.Fatal(err)
		}
		info, err := a.auth.UserByName(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		names[u] = info.DBName
		db, err := store.OpenUser(ctx, data, info.DBName)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetSetting(ctx, db.W, "owner", u); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
	a.db.Close()

	if err := backup([]string{out}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(out, store.AppFileName)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("app.db copy: %v %v", fi, err)
	}
	for u, name := range names {
		db, err := store.OpenPath(ctx, store.UserPath(out, name), os.DirFS(t.TempDir()))
		if err != nil {
			t.Fatal(err)
		}
		var owner string
		if _, err := store.GetSetting(ctx, db.RO, "owner", &owner); err != nil || owner != u {
			t.Fatalf("%s's copy: owner %q, %v", u, owner, err)
		}
		db.Close()
	}
	if err := backup([]string{out}); err == nil {
		t.Fatal("backup into a non-empty directory must fail")
	}
}
