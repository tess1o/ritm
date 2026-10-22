package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ritm/internal/store"
	"ritm/migrations"
)

func TestCheckSQL(t *testing.T) {
	ok := []string{
		"SELECT 1",
		"  with x as (select 1) select * from x;",
		"SELECT replace(type, 'HK', '') FROM records -- comment; with semicolon",
		"SELECT 'it''s; DELETE' AS s",
		"SELECT updated_at FROM workout_annotations",
	}
	for _, q := range ok {
		if err := checkSQL(q); err != nil {
			t.Errorf("%q: %v", q, err)
		}
	}
	bad := []string{"", "DELETE FROM records", "PRAGMA query_only = 0", "ATTACH 'x.db' AS x", "SELECT 1; SELECT 2",
		"VALUES (1)", "WITH x AS (SELECT 1) INSERT INTO settings SELECT 'a', 'b'", "EXPLAIN SELECT 1"}
	for _, q := range bad {
		if err := checkSQL(q); err == nil {
			t.Errorf("%q: expected an error", q)
		}
	}
}

// The authorizer must hold even for statements that slip past checkSQL.
func TestRunSQLAuthorizer(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.OpenPath(ctx, filepath.Join(dir, "u.db"), migrations.User)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	other, err := store.OpenPath(ctx, filepath.Join(dir, "other.db"), os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	other.Close()

	for _, q := range []string{
		"ATTACH DATABASE '" + filepath.Join(dir, "other.db") + "' AS o",
		"PRAGMA query_only = 0",
		"INSERT INTO settings (key, value) VALUES ('a', 'b')",
		"CREATE TEMP TABLE x (a)",
		"BEGIN",
		"SELECT load_extension('x')",
	} {
		if _, err := runSQL(ctx, db.RO, q); err == nil {
			t.Errorf("%q was allowed", q)
		}
	}
	res, err := runSQL(ctx, db.RO, "SELECT count(*) FROM settings")
	if err != nil || res.RowCount != 1 {
		t.Fatalf("plain select: %v %+v", err, res)
	}
	// The connection goes back to the pool without the authorizer.
	var n int
	if err := db.RO.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('settings')").Scan(&n); err != nil || n == 0 {
		t.Fatalf("pool connection still restricted: %v", err)
	}

	sqlTimeout = 200 * time.Millisecond
	defer func() { sqlTimeout = 5 * time.Second }()
	_, err = runSQL(ctx, db.RO, "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c) SELECT count(*) FROM c")
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("runaway query: %v", err)
	}
}
