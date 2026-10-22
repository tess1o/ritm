package tenant

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ritm/internal/auth"
	"ritm/internal/clock"
	"ritm/internal/config"
	"ritm/internal/ingest"
	"ritm/internal/store"
	"ritm/internal/synth"
)

func setup(t *testing.T, maxImports int) (*Registry, *auth.Service, *config.Config) {
	t.Helper()
	ctx := context.Background()
	cfg := &config.Config{DataDir: t.TempDir(), TZ: time.UTC, MaxConcurrentImports: maxImports, DeltaWindow: 7 * 24 * time.Hour}
	appDB, err := store.OpenApp(ctx, cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	a := auth.NewService(appDB, clk, time.Hour)
	reg := NewRegistry(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), clk, a)
	t.Cleanup(func() {
		reg.Shutdown(context.Background())
		appDB.Close()
	})
	return reg, a, cfg
}

func user(t *testing.T, a *auth.Service, name string) auth.User {
	t.Helper()
	u, err := a.CreateUser(context.Background(), name, "password1")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestResetDataSwapsToAnEmptyDatabase(t *testing.T) {
	ctx := context.Background()
	reg, a, cfg := setup(t, 1)
	u := user(t, a, "alice")
	tn, err := reg.Get(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(ctx, tn.DB.W, "marker", 1); err != nil {
		t.Fatal(err)
	}
	next, err := reg.ResetData(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if next.DBName == u.DBName {
		t.Fatal("reset must assign a new database name")
	}
	if exists(store.UserPath(cfg.DataDir, u.DBName)) {
		t.Fatal("old database file must be removed")
	}
	info, _ := a.UserByName(ctx, "alice")
	if info.DBName != next.DBName {
		t.Fatalf("users.db_name = %q, want %q", info.DBName, next.DBName)
	}
	tn, err = reg.Get(ctx, info.User)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	if ok, _ := store.GetSetting(ctx, tn.DB.RO, "marker", &v); ok {
		t.Fatal("data survived the reset")
	}
}

func TestDeleteUserRemovesAccountAndFile(t *testing.T) {
	ctx := context.Background()
	reg, a, cfg := setup(t, 1)
	u := user(t, a, "alice")
	if _, err := reg.Get(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := reg.DeleteUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if exists(store.UserPath(cfg.DataDir, u.DBName)) {
		t.Fatal("database file must be removed")
	}
	if _, err := a.UserByName(ctx, "alice"); !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("user still exists: %v", err)
	}
}

func TestServerWideImportCap(t *testing.T) {
	ctx := context.Background()
	reg, a, _ := setup(t, 1)
	ta, err := reg.Get(ctx, user(t, a, "alice"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ta.Imports.Reserve(); err != nil {
		t.Fatal(err)
	}
	if err := ta.Imports.Reserve(); !errors.Is(err, ingest.ErrBusy) {
		t.Fatalf("second import of the same user: %v", err)
	}
	ta.Imports.Release()

	// Another user's import holds the only server-wide slot: an upload is accepted and queued
	// (not rejected), and runs once the slot is free.
	reg.slots <- struct{}{}
	if err := ta.Imports.Reserve(); err != nil {
		t.Fatalf("reserve while another user imports: %v", err)
	}
	tmp := filepath.Join(ta.Imports.TmpDir(), "upload")
	opts := synth.Options{Start: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Days: 3, Loc: time.UTC, Seed: 1, HRIntervalSec: 1800}
	if err := synth.WriteZip(tmp, opts); err != nil {
		t.Fatal(err)
	}
	if err := ta.Imports.Start("imp1", tmp, "export.zip", 1, false); err != nil {
		t.Fatal(err)
	}
	status := func() string {
		row, err := ingest.GetImport(ctx, ta.DB.RO, "imp1")
		if err != nil {
			t.Fatal(err)
		}
		return row.Status
	}
	time.Sleep(100 * time.Millisecond)
	if s := status(); s != ingest.StatusQueued {
		t.Fatalf("status while the slot is taken: %s, want queued", s)
	}
	<-reg.slots
	deadline := time.Now().Add(20 * time.Second)
	for status() != ingest.StatusSucceeded {
		if time.Now().After(deadline) {
			t.Fatalf("import did not finish, status %s", status())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
