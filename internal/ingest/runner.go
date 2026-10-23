package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"ritm/internal/store"
	"ritm/internal/uid"
)

// Import statuses stored in imports.status.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// ImportRow is one row of the imports table.
type ImportRow struct {
	ID           string       `json:"id"`
	Status       string       `json:"status" enum:"queued,running,succeeded,failed"`
	FileName     string       `json:"file_name"`
	FileSize     int64        `json:"file_size"`
	ExportedAt   *time.Time   `json:"exported_at" doc:"ExportDate of the Apple Health export"`
	FullReimport bool         `json:"full_reimport"`
	StartedAt    *time.Time   `json:"started_at"`
	FinishedAt   *time.Time   `json:"finished_at"`
	Stats        *ImportStats `json:"stats,omitempty"`
	Error        string       `json:"error,omitempty"`
}

func NewID() string { return uid.New() }

// CreateImport records a new import with the given status (queued or running).
func CreateImport(ctx context.Context, db *store.DB, id, status, fileName string, size int64, full bool, now time.Time) error {
	_, err := db.W.ExecContext(ctx, `INSERT INTO imports (id, status, file_name, file_size, full_reimport, started_at)
		VALUES (?, ?, ?, ?, ?, ?)`, id, status, fileName, size, store.BoolInt(full), now.UnixMilli())
	return err
}

// MarkRunning moves a queued import to running once it has a server-wide slot.
func MarkRunning(ctx context.Context, db *store.DB, id string, now time.Time) error {
	_, err := db.W.ExecContext(ctx, "UPDATE imports SET status = ?, started_at = ? WHERE id = ?", StatusRunning, now.UnixMilli(), id)
	return err
}

// FinishImport records the outcome of an import.
func FinishImport(ctx context.Context, db *store.DB, id string, st *ImportStats, runErr error, now time.Time) error {
	status := StatusSucceeded
	var errMsg, statsJSON, exportDate any
	if runErr != nil {
		status = StatusFailed
		errMsg = runErr.Error()
	}
	if st != nil {
		b, _ := json.Marshal(st)
		statsJSON = string(b)
		if st.ExportDateMS > 0 {
			exportDate = st.ExportDateMS
		}
	}
	// A context canceled by shutdown must not prevent recording the failure.
	ctx = context.WithoutCancel(ctx)
	_, err := db.W.ExecContext(ctx, `UPDATE imports SET status = ?, finished_at = ?, stats_json = ?, error = ?, export_date = ? WHERE id = ?`,
		status, now.UnixMilli(), statsJSON, errMsg, exportDate, id)
	return err
}

// RecoverInterrupted marks imports left running by a crash or restart as failed.
func RecoverInterrupted(ctx context.Context, db *store.DB, now time.Time) (int64, error) {
	res, err := db.W.ExecContext(ctx, `UPDATE imports SET status = ?, finished_at = ?, error = 'interrupted by server restart'
		WHERE status IN (?, ?)`, StatusFailed, now.UnixMilli(), StatusRunning, StatusQueued)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func GetImport(ctx context.Context, q store.Querier, id string) (ImportRow, error) {
	rows, err := listImports(ctx, q, "WHERE id = ?", id)
	if err != nil {
		return ImportRow{}, err
	}
	if len(rows) == 0 {
		return ImportRow{}, sql.ErrNoRows
	}
	return rows[0], nil
}

func ListImports(ctx context.Context, q store.Querier, limit int) ([]ImportRow, error) {
	return listImports(ctx, q, "ORDER BY started_at DESC LIMIT ?", limit)
}

func listImports(ctx context.Context, q store.Querier, tail string, args ...any) ([]ImportRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, status, ifnull(file_name, ''), ifnull(file_size, 0), export_date, full_reimport,
		started_at, finished_at, stats_json, ifnull(error, '') FROM imports `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImportRow{}
	for rows.Next() {
		var (
			r                      ImportRow
			full                   int
			exp, started, finished sql.NullInt64
			stats                  sql.NullString
		)
		if err := rows.Scan(&r.ID, &r.Status, &r.FileName, &r.FileSize, &exp, &full, &started, &finished, &stats, &r.Error); err != nil {
			return nil, err
		}
		r.FullReimport = full == 1
		r.ExportedAt, r.StartedAt, r.FinishedAt = store.MSTime(exp), store.MSTime(started), store.MSTime(finished)
		if stats.Valid {
			r.Stats = &ImportStats{}
			if err := json.Unmarshal([]byte(stats.String), r.Stats); err != nil {
				return nil, fmt.Errorf("import %s stats: %w", r.ID, err)
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PostImportFunc recomputes derived data from the earliest affected day. It is called
// after every successful import, with "" when nothing new was stored, so it can also finish a
// recompute that an earlier failure left pending.
type PostImportFunc func(ctx context.Context, earliestDay string) error

// RunRecorded wraps Import with bookkeeping in the imports table and the post-import hook. A
// failed recompute fails the import: the data is stored, but derived tables and insights are out
// of date until the next recompute repairs them.
func RunRecorded(ctx context.Context, db *store.DB, path, fileName string, size int64, opts Options, post PostImportFunc) (*ImportStats, error) {
	if opts.ImportID == "" {
		opts.ImportID = NewID()
		if err := CreateImport(ctx, db, opts.ImportID, StatusRunning, fileName, size, opts.Full, opts.now()); err != nil {
			return nil, err
		}
	}
	st, err := Import(ctx, db, path, opts)
	if err == nil && post != nil {
		if perr := post(ctx, st.EarliestDay); perr != nil {
			err = fmt.Errorf("data was imported, but updating dashboards and insights failed (retried on the next import): %w", perr)
		}
	}
	if ferr := FinishImport(ctx, db, opts.ImportID, st, err, opts.now()); ferr != nil {
		return st, errors.Join(err, ferr)
	}
	return st, err
}
