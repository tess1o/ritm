package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"ritm/internal/store"
)

// backup writes consistent copies of app.db and every user's database into dir, mirroring the
// data-directory layout (NFR-8). VACUUM INTO reads a snapshot, so it is safe while the server
// runs. Restore by stopping Ritm and copying the files back into RITM_DATA_DIR.
func backup(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: ritm backup DIR")
	}
	dir := args[0]
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s is not empty", dir)
	}
	if err := os.MkdirAll(filepath.Join(dir, store.UsersDir), 0o750); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx)
	if err != nil {
		return err
	}
	defer a.db.Close()

	start := time.Now()
	if err := a.db.Backup(ctx, filepath.Join(dir, store.AppFileName)); err != nil {
		return fmt.Errorf("back up %s: %w", store.AppFileName, err)
	}
	users, err := a.auth.ListUsers(ctx)
	if err != nil {
		return err
	}
	for _, u := range users {
		if err := backupUser(ctx, a.cfg.DataDir, dir, u.DBName); err != nil {
			return fmt.Errorf("back up data of %s: %w", u.Username, err)
		}
	}
	fmt.Printf("backed up %s and %d user database(s) to %s in %.1fs\n", store.AppFileName, len(users), dir, time.Since(start).Seconds())
	return nil
}

func backupUser(ctx context.Context, dataDir, dir, dbName string) error {
	// Never create a database just to back it up: a user added with the CLI has none until the
	// server first opens it.
	if _, err := os.Stat(store.UserPath(dataDir, dbName)); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	db, err := store.OpenUser(ctx, dataDir, dbName)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Backup(ctx, store.UserPath(dir, dbName))
}
