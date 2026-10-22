package query

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"ritm/internal/clock"
	"ritm/internal/days"
	"ritm/internal/derive"
	"ritm/internal/ingest"
	"ritm/internal/insights"
	"ritm/internal/store"
)

func newService(t *testing.T, now time.Time) (*Service, *store.DB) {
	t.Helper()
	db, err := store.OpenUser(context.Background(), t.TempDir(), "q")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clk := clock.NewFake(now)
	eng := derive.NewEngine(db, days.NewZone(time.UTC), clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return New(db, days.NewZone(time.UTC), clk, eng), db
}

// Totals and monthly volume come from SQL, not from a capped workout list, and count only the
// main workout type.
func TestTrainingTotalsAreUncapped(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	s, db := newService(t, start.AddDate(0, 0, 300))
	ins := func(typ string, at time.Time) {
		if _, err := db.W.ExecContext(ctx, `INSERT INTO workouts (activity_type, source_name, start_ms, end_ms, tz_offset_min, duration_s, import_id)
			VALUES (?, 'Apple Watch', ?, ?, 0, 3600, 'x')`, typ, at.UnixMilli(), at.Add(time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	for d := range 300 {
		day := start.AddDate(0, 0, d)
		ins(ingest.WorkoutFunctionalStrength, day)
		ins(ingest.WorkoutFunctionalStrength, day.Add(6*time.Hour))
		if d%3 == 0 {
			ins("HKWorkoutActivityTypeWalking", day.Add(3*time.Hour))
		}
	}
	r := days.Range{From: days.FromTime(start), To: days.FromTime(start.AddDate(0, 0, 299))}
	wk, err := s.TrainingWeekly(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if wk.Totals.Sessions != 600 || wk.Totals.Hours != 600 {
		t.Fatalf("totals %+v, want 600 sessions / 600 h", wk.Totals)
	}
	n := 0
	for _, m := range wk.Monthly {
		n += m.Sessions
	}
	if n != 600 || wk.Monthly[0].Month != "2026-01" || wk.Monthly[0].Sessions != 62 {
		t.Fatalf("monthly %+v", wk.Monthly)
	}
	if list, truncated, _ := s.Workouts(ctx, WorkoutFilter{Range: r}, 500); len(list) != 500 || !truncated {
		t.Fatalf("workout list should be capped: %d %v", len(list), truncated)
	}
}

func TestReadModelWrappers(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	s, db := newService(t, now)
	if err := ingest.CreateImport(ctx, db, "imp", ingest.StatusRunning, "export.zip", 1, false, now); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.Imports(ctx, 10); err != nil || len(rows) != 1 || rows[0].ID != "imp" {
		t.Fatalf("imports %v %v", rows, err)
	}
	if _, err := s.Import(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing import: %v", err)
	}
	if st, err := s.Settings(ctx); err != nil || st.LoadModel != "edwards" {
		t.Fatalf("settings %+v %v", st, err)
	}
	if _, err := db.W.ExecContext(ctx, `INSERT INTO insights (rule_id, category, polarity, period_kind, period_start, period_end,
		title, body, created_at) VALUES ('r', 'activity', 'positive', 'weekly', '2026-10-12', '2026-10-18', 'Steps up {period}', 'b', 0)`); err != nil {
		t.Fatal(err)
	}
	list, err := s.Insights(ctx, insights.Filter{})
	if err != nil || len(list) != 1 || list[0].Title != "Steps up last week" {
		t.Fatalf("insights %+v %v", list, err)
	}
}
