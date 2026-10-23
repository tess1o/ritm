// Package query is the read model shared by the REST API (and later MCP): typed results with
// units in field names.
package query

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"

	"ritm/internal/clock"
	"ritm/internal/days"
	"ritm/internal/derive"
	"ritm/internal/ingest"
	"ritm/internal/insights"
	"ritm/internal/store"
)

type Service struct {
	db     *store.DB
	zone   *days.Zone
	clock  clock.Clock
	derive *derive.Engine

	lastChecked atomic.Int64 // unix ms of the last recorded monitor poll
	bg          sync.WaitGroup
	coverage    coverageCache
}

func New(db *store.DB, zone *days.Zone, clk clock.Clock, eng *derive.Engine) *Service {
	return &Service{db: db, zone: zone, clock: clk, derive: eng}
}

func (s *Service) Today() days.Day { return s.zone.Today(s.clock.Now()) }

func (s *Service) Resolved(ctx context.Context) (*derive.Resolved, error) {
	return s.derive.Resolved(ctx, s.db.RO)
}

func nf(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	x := v.Float64
	return &x
}

func ni(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	x := v.Int64
	return &x
}

// dayBounds returns [start, end) UTC ms of the local days in r, in the configured zone.
func (s *Service) dayBounds(r days.Range) (int64, int64) {
	return r.From.StartMS(s.zone.Location()), r.To.Add(1).StartMS(s.zone.Location())
}

// Insights lists stored insights, with period phrases rendered relative to today when f.Today is unset.
func (s *Service) Insights(ctx context.Context, f insights.Filter) ([]insights.Insight, error) {
	if f.Today == 0 {
		f.Today = s.Today()
	}
	return insights.List(ctx, s.db.RO, f)
}

// Imports lists the most recent imports, newest first.
func (s *Service) Imports(ctx context.Context, limit int) ([]ingest.ImportRow, error) {
	return ingest.ListImports(ctx, s.db.RO, limit)
}

// Import returns one import, or ErrNotFound.
func (s *Service) Import(ctx context.Context, id string) (ingest.ImportRow, error) {
	row, err := ingest.GetImport(ctx, s.db.RO, id)
	if errors.Is(err, sql.ErrNoRows) {
		return row, ErrNotFound
	}
	return row, err
}

// Settings returns the stored settings (defaults for anything unset).
func (s *Service) Settings(ctx context.Context) (derive.Settings, error) {
	return derive.LoadSettings(ctx, s.db.RO)
}
