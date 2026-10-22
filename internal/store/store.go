// Package store owns the SQLite database: connection pools, migrations and small shared helpers.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"ritm/migrations"
)

// DB bundles a read-write pool and a read-only pool (mode=ro, query_only) over the same file.
// All API reads and the MCP SQL tool use RO; writes go through W.
type DB struct {
	W    *sql.DB
	RO   *sql.DB
	Path string
}

const (
	AppFileName = "app.db"
	UsersDir    = "users"
	// ImportLockSuffix names the file next to a user database that serialises imports.
	ImportLockSuffix = ".import-lock"
	// DeriveLockSuffix names the file next to a user database that serialises recomputes.
	DeriveLockSuffix = ".derive-lock"
)

// OpenApp opens (creating dataDir if needed) the shared accounts database.
func OpenApp(ctx context.Context, dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	return OpenPath(ctx, filepath.Join(dataDir, AppFileName), migrations.App)
}

// UserPath is the location of a user's health-data database.
func UserPath(dataDir, dbName string) string {
	return filepath.Join(dataDir, UsersDir, dbName+".db")
}

// OpenUser opens (creating it if needed) the health-data database of the user whose db_name is dbName.
func OpenUser(ctx context.Context, dataDir, dbName string) (*DB, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, UsersDir), 0o750); err != nil {
		return nil, fmt.Errorf("create users dir: %w", err)
	}
	return OpenPath(ctx, UserPath(dataDir, dbName), migrations.User)
}

// RemoveFiles deletes a database file together with its WAL, shared-memory and import-lock files.
func RemoveFiles(path string) error {
	var errs []error
	for _, p := range []string{path, path + "-wal", path + "-shm", path + ImportLockSuffix, path + DeriveLockSuffix} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// OpenPath opens the database at path, applies the migrations in schema and opens the read-only pool.
func OpenPath(ctx context.Context, path string, schema fs.FS) (*DB, error) {
	w, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, err
	}
	// Every connection has its own page cache (see dsn), so few are kept idle and idle readers
	// are closed after a while: memory stays within NFR-1/NFR-4 with many users.
	w.SetMaxOpenConns(4)
	w.SetMaxIdleConns(2)
	w.SetConnMaxIdleTime(2 * time.Minute)
	if err := w.PingContext(ctx); err != nil {
		w.Close()
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := migrate(ctx, w, schema); err != nil {
		w.Close()
		return nil, err
	}
	ro, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		w.Close()
		return nil, err
	}
	ro.SetMaxOpenConns(8)
	ro.SetMaxIdleConns(2)
	ro.SetConnMaxIdleTime(2 * time.Minute)
	if err := ro.PingContext(ctx); err != nil {
		w.Close()
		ro.Close()
		return nil, fmt.Errorf("open read-only db: %w", err)
	}
	return &DB{W: w, RO: ro, Path: path}, nil
}

func dsn(path string, readOnly bool) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(15000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "temp_store(MEMORY)")
	if readOnly {
		q.Add("_pragma", "cache_size(-8000)") // 8 MB per reader
		q.Set("mode", "ro")
		q.Add("_pragma", "query_only(1)")
	} else {
		q.Add("_pragma", "journal_mode(WAL)")
		q.Add("_pragma", "synchronous(NORMAL)")
		q.Add("_pragma", "cache_size(-16000)") // 16 MB per writer; a bigger cache does not speed up imports
		q.Set("_txlock", "immediate")
	}
	return "file:" + path + "?" + q.Encode()
}

func (db *DB) Close() error {
	return errors.Join(db.RO.Close(), db.W.Close())
}

func (db *DB) Ping(ctx context.Context) error {
	var one int
	return db.RO.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

func migrate(ctx context.Context, db *sql.DB, schema fs.FS) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("migrations table: %w", err)
	}
	names, err := fs.Glob(schema, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		var n int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE version = ?", version).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := fs.ReadFile(schema, name)
		if err != nil {
			return err
		}
		err = InTx(ctx, db, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", version, time.Now().UnixMilli())
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

// InTx runs fn in a transaction, committing on nil error and rolling back otherwise.
func InTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Querier is satisfied by *sql.DB, *sql.Tx and *sql.Conn.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// AppSettingsKey is the settings row holding the user's settings (derive.Settings) as JSON.
const AppSettingsKey = "app"

// GetSetting decodes the JSON value stored under key into dst. It reports whether the key exists.
func GetSetting(ctx context.Context, q Querier, key string, dst any) (bool, error) {
	var raw string
	err := q.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), dst)
}

func SetSetting(ctx context.Context, q Querier, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, string(raw))
	return err
}

// Backup writes a consistent copy of the database to path using VACUUM INTO.
func (db *DB) Backup(ctx context.Context, path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	if _, err := db.W.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600) // health data: owner only
}

// BoolInt stores a bool as SQLite's 0/1.
func BoolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// MSTime converts a nullable epoch-millisecond column to a time (nil when NULL).
func MSTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64)
	return &t
}
