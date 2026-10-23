package derive

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"ritm/internal/clock"
	"ritm/internal/days"
	"ritm/internal/store"
)

const chunkDays = 60

// Engine recomputes derived tables; recomputes are serialised.
type Engine struct {
	db    *store.DB
	zone  *days.Zone
	clock clock.Clock
	log   *slog.Logger

	mu    sync.Mutex
	after func(ctx context.Context, from days.Day, res *Resolved) error
}

func NewEngine(db *store.DB, zone *days.Zone, clk clock.Clock, log *slog.Logger) *Engine {
	return &Engine{db: db, zone: zone, clock: clk, log: log}
}

// OnRecomputed registers a hook run after derived tables are updated (the insights engine).
func (e *Engine) OnRecomputed(fn func(ctx context.Context, from days.Day, res *Resolved) error) {
	e.after = fn
}

// Resolved loads settings and fills automatic values.
func (e *Engine) Resolved(ctx context.Context, q store.Querier) (*Resolved, error) {
	s, err := LoadSettings(ctx, q)
	if err != nil {
		return nil, err
	}
	return Resolve(ctx, q, s, e.clock.Now())
}

// pendingKey stores the earliest day whose derived data is out of date because a recompute
// failed (e.g. it was interrupted by shutdown). The next recompute starts no later than that day.
const pendingKey = "derive.pending_from"

// AfterImport recomputes from the import's earliest affected day ("" = nothing new), or from an
// earlier day left pending by a failed recompute.
func (e *Engine) AfterImport(ctx context.Context, earliestDay string) error {
	if earliestDay == "" {
		var pending string
		if ok, err := store.GetSetting(ctx, e.db.W, pendingKey, &pending); err != nil || !ok {
			return err
		}
		earliestDay = pending
	}
	return e.Recompute(ctx, earliestDay)
}

// Recompute rebuilds derived data from fromDay (YYYY-MM-DD, "" = everything) to today.
// The start day is recorded as pending before the work begins and cleared when it succeeds, so
// a failed or killed recompute is repaired by the next one.
func (e *Engine) Recompute(ctx context.Context, fromDay string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	// `ritm recompute` may run next to the server; the file lock serialises them.
	unlock, err := store.LockFile(e.db.Path+store.DeriveLockSuffix, true)
	if err != nil {
		return err
	}
	defer unlock()

	var pending string
	if _, err := store.GetSetting(ctx, e.db.W, pendingKey, &pending); err != nil {
		return err
	}
	mark := fromDay
	if mark == "" {
		mark = "1970-01-01" // everything
	}
	if pending != "" && pending < mark {
		mark, fromDay = pending, pending
	}
	if mark != pending {
		if err := store.SetSetting(ctx, e.db.W, pendingKey, mark); err != nil {
			return err
		}
	}
	if err := e.recompute(ctx, fromDay); err != nil {
		return err
	}
	_, err = e.db.W.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", pendingKey)
	return err
}

// HasPending reports whether a failed recompute left derived data out of date.
func (e *Engine) HasPending(ctx context.Context) (bool, error) {
	var pending string
	return store.GetSetting(ctx, e.db.RO, pendingKey, &pending)
}

func (e *Engine) recompute(ctx context.Context, fromDay string) error {
	started := time.Now()

	res, err := e.Resolved(ctx, e.db.W)
	if err != nil {
		return err
	}
	first, last, ok, err := dataSpan(ctx, e.db.W)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	from := first
	if fromDay != "" {
		d, err := days.Parse(fromDay)
		if err != nil {
			return err
		}
		from = max(d, first)
	}
	to := max(last, e.zone.Today(e.clock.Now()))

	for c := from; c <= to; c += chunkDays {
		r := days.Range{From: c, To: min(c+chunkDays-1, to)}
		err := store.InTx(ctx, e.db.W, func(tx *sql.Tx) error {
			asleep, err := e.computeSleep(ctx, tx, r, res)
			if err != nil {
				return fmt.Errorf("sleep: %w", err)
			}
			if err := e.computeHeart(ctx, tx, r, res, asleep); err != nil {
				return fmt.Errorf("heart: %w", err)
			}
			if err := e.computeActivity(ctx, tx, r, res); err != nil {
				return fmt.Errorf("activity: %w", err)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("recompute %s: %w", r, err)
		}
	}
	err = store.InTx(ctx, e.db.W, func(tx *sql.Tx) error {
		if err := e.computeWorkouts(ctx, tx, from, res); err != nil {
			return fmt.Errorf("workouts: %w", err)
		}
		if err := e.computeLoad(ctx, tx, res, to); err != nil {
			return err
		}
		return store.BumpDataVersion(ctx, tx)
	})
	if err != nil {
		return err
	}
	e.log.Info("derived data recomputed", "from", from.String(), "to", to.String(), "duration", time.Since(started).Round(time.Millisecond))
	if e.after != nil {
		if err := e.after(ctx, from, res); err != nil {
			return fmt.Errorf("insights: %w", err)
		}
	}
	return nil
}
