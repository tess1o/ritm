package mcpserver

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	sqlMaxRows = 500
	// sqlMaxValueBytes caps any one string or blob a query builds (SQLITE_LIMIT_LENGTH), so
	// e.g. printf('%.*c', 1e9, 'x') fails instead of allocating a gigabyte.
	sqlMaxValueBytes = 1 << 20
	// sqlMaxResultBytes caps the result as a whole; rows past it are cut off as truncated.
	sqlMaxResultBytes = 4 << 20
)

var sqlTimeout = 5 * time.Second // a variable for tests

var (
	errSQLDisabled   = errors.New("SQL access is turned off for this account. The user can turn on \"Allow AI assistants to run SQL queries\" in Ritm Settings; until then, use the other tools")
	errSQLOnlySelect = errors.New("only a single SELECT or WITH … SELECT statement is allowed")
	sqlComments      = regexp.MustCompile(`(?s)--[^\n]*|/\*.*?\*/`)
	sqlStrings       = regexp.MustCompile(`'(?:[^']|'')*'|"(?:[^"]|"")*"`)
	sqlForbidden     = regexp.MustCompile(`(?i)\b(attach|detach|pragma|insert|update|delete|replace\s+into|create|drop|alter|vacuum|reindex|analyze|begin|commit|rollback|savepoint|release)\b`)
)

// checkSQL gives a clear error for statements that are not a single read-only query. It is a
// convenience: the authorizer in querySQL is what actually enforces it.
func checkSQL(q string) error {
	bare := sqlStrings.ReplaceAllString(sqlComments.ReplaceAllString(q, " "), "''")
	bare = strings.TrimSpace(bare)
	bare = strings.TrimSpace(strings.TrimSuffix(bare, ";"))
	if strings.Contains(bare, ";") {
		return errSQLOnlySelect
	}
	first := strings.ToLower(strings.Fields(bare + " x")[0])
	if first != "select" && first != "with" {
		return errSQLOnlySelect
	}
	if m := sqlForbidden.FindString(bare); m != "" {
		return fmt.Errorf("%w (found %q)", errSQLOnlySelect, strings.ToUpper(m))
	}
	return nil
}

// readOnlyAuthorizer allows only reading the user's own database: no PRAGMA, ATTACH, writes,
// transactions or extension loading.
func readOnlyAuthorizer(action sqlite.AuthorizerActionCode, arg1, arg2, dbName, _ string) sqlite.AuthorizerReturnCode {
	switch action {
	case sqlite.AuthSelect, sqlite.AuthRecursive:
		return sqlite.AuthorizerOK
	case sqlite.AuthRead:
		if dbName == "main" || dbName == "temp" || dbName == "" {
			return sqlite.AuthorizerOK
		}
	case sqlite.AuthFunction:
		if !strings.EqualFold(arg2, "load_extension") {
			return sqlite.AuthorizerOK
		}
	}
	return sqlite.AuthorizerDeny
}

type sqlResult struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	RowCount  int      `json:"row_count"`
	Truncated bool     `json:"truncated,omitempty"`
}

func querySQL(ctx context.Context, db *sql.DB, q string) (*sqlResult, error) {
	if err := checkSQL(q); err != nil {
		return nil, err
	}
	return runSQL(ctx, db, q)
}

// sqlPragmas bound the memory of one query: sorts and temporary results spill to a temporary
// file instead of RAM, and the page cache is small. The previous values are restored afterwards.
var sqlPragmas = []string{"temp_store", "cache_size"}

var sqlPragmaValues = map[string]string{"temp_store": "FILE", "cache_size": "-2000"}

// runSQL runs q on a pooled read-only connection (mode=ro, query_only) with the authorizer and
// the memory-bounding pragmas installed for the duration of the query.
func runSQL(ctx context.Context, db *sql.DB, q string) (*sqlResult, error) {
	ctx, cancel := context.WithTimeout(ctx, sqlTimeout)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	prev := map[string]string{}
	for _, p := range sqlPragmas {
		var v string
		if err := conn.QueryRowContext(ctx, "PRAGMA "+p).Scan(&v); err != nil {
			return nil, err
		}
		prev[p] = v
	}
	defer func() {
		for _, p := range sqlPragmas {
			if _, err := conn.ExecContext(context.Background(), "PRAGMA "+p+" = "+prev[p]); err != nil {
				conn.Raw(func(any) error { return driver.ErrBadConn }) // drop the connection from the pool
				return
			}
		}
	}()
	for _, p := range sqlPragmas {
		if _, err := conn.ExecContext(ctx, "PRAGMA "+p+" = "+sqlPragmaValues[p]); err != nil {
			return nil, err
		}
	}
	setAuth := func(fn sqlite.AuthorizerFn) error {
		return conn.Raw(func(dc any) error {
			r, ok := dc.(sqlite.AuthorizerRegisterer)
			if !ok {
				return errors.New("driver does not support an authorizer")
			}
			return r.RegisterAuthorizer(fn)
		})
	}
	prevLen, err := sqlite.Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, sqlMaxValueBytes)
	if err != nil {
		return nil, err
	}
	defer func() {
		if _, err := sqlite.Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, prevLen); err != nil {
			conn.Raw(func(any) error { return driver.ErrBadConn }) // drop the connection from the pool
		}
	}()
	if err := setAuth(readOnlyAuthorizer); err != nil {
		return nil, err
	}
	defer func() {
		if setAuth(nil) != nil {
			conn.Raw(func(any) error { return driver.ErrBadConn }) // drop the connection from the pool
		}
	}()

	rows, err := conn.QueryContext(ctx, q)
	if err != nil {
		return nil, sqlErr(ctx, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res := &sqlResult{Columns: cols, Rows: [][]any{}}
	size := 0
	for rows.Next() {
		if len(res.Rows) == sqlMaxRows || size > sqlMaxResultBytes {
			res.Truncated = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, v := range vals {
			switch v := v.(type) {
			case []byte:
				vals[i] = fmt.Sprintf("<blob %d bytes>", len(v))
			case string:
				size += len(v)
			}
			size += 8
		}
		res.Rows = append(res.Rows, vals)
	}
	if err := rows.Err(); err != nil {
		return nil, sqlErr(ctx, err)
	}
	res.RowCount = len(res.Rows)
	return res, nil
}

func sqlErr(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("query exceeded the %s limit", sqlTimeout)
	}
	return fmt.Errorf("query failed: %v", err)
}

func (t *tools) registerSQL(s *mcp.Server) {
	addTool(s, t, "query_sql", "Read-only SQL",
		"Run one read-only SELECT (or WITH … SELECT) on the SQLite database, max 500 rows, 5 s timeout. Off unless the user enabled it in Ritm Settings; prefer the other tools. Read ritm://schema for tables, views and units. Prefer the views v_daily and v_workouts. Times in tables are UTC epoch milliseconds with tz_offset_min; days are local YYYY-MM-DD.",
		func(ctx context.Context, in struct {
			SQL string `json:"sql" jsonschema:"A single SELECT or WITH … SELECT statement."`
		}) (any, error) {
			st, err := t.t.Query.Settings(ctx)
			if err != nil {
				return nil, err
			}
			if !st.AllowSQL {
				return nil, errSQLDisabled
			}
			return querySQL(ctx, t.t.DB.RO, in.SQL)
		})
}
