package derive

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"math"
	"path/filepath"
	"testing"
	"time"

	"ritm/internal/clock"
	"ritm/internal/days"
	"ritm/internal/ingest"
	"ritm/internal/store"
	"ritm/internal/synth"
)

var loc = time.FixedZone("EET", 2*3600)

func setup(t *testing.T, o synth.Options) (*store.DB, *Engine) {
	t.Helper()
	ctx := context.Background()
	db, err := store.OpenUser(ctx, t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	p := filepath.Join(t.TempDir(), "e.zip")
	if err := synth.WriteZip(p, o); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(o.Start.AddDate(0, 0, o.Days).Add(-time.Hour))
	eng := NewEngine(db, days.NewZone(loc), clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err = ingest.RunRecorded(ctx, db, p, "e.zip", 0, ingest.Options{DeltaWindow: 7 * 24 * time.Hour, Loc: loc}, eng.AfterImport)
	if err != nil {
		t.Fatal(err)
	}
	return db, eng
}

func opts() synth.Options {
	return synth.Options{Start: time.Date(2026, 1, 5, 0, 0, 0, 0, loc), Days: 35, Loc: loc, Seed: 3, Sleep: true, IPhone: true, HRIntervalSec: 600, WorkoutHRIntervalSec: 10}
}

func TestDailyActivityUsesWatchPriority(t *testing.T) {
	db, _ := setup(t, opts())
	ctx := context.Background()
	// Expected: Watch value per hour, iPhone only in hours without Watch data (SRC-2).
	var watchSteps, daily float64
	db.RO.QueryRowContext(ctx, `SELECT sum(v) FROM (
		SELECT local_hour, coalesce(max(CASE WHEN source_name LIKE 'Apple Watch%' THEN value END), max(value)) AS v
		FROM hourly_activity WHERE type = ? AND local_day = '2026-01-07' GROUP BY local_hour)`,
		ingest.TypeSteps).Scan(&watchSteps)
	db.RO.QueryRowContext(ctx, "SELECT steps FROM daily_activity WHERE day = '2026-01-07'").Scan(&daily)
	if watchSteps == 0 || math.Abs(daily-watchSteps) > 0.5 {
		t.Fatalf("daily steps %v, watch-only %v", daily, watchSteps)
	}
	var gym, standHours, activeHours int
	var sedentary, exMin float64
	db.RO.QueryRowContext(ctx, "SELECT is_gym_day, stand_hours, active_hours, longest_sedentary_h, exercise_min FROM daily_activity WHERE day = '2026-01-07'").
		Scan(&gym, &standHours, &activeHours, &sedentary, &exMin)
	if gym != 1 || standHours == 0 || activeHours == 0 || exMin < 60 {
		t.Fatalf("gym=%d stand=%d active=%d sedentary=%v ex=%v", gym, standHours, activeHours, sedentary, exMin)
	}
	var n int
	db.RO.QueryRowContext(ctx, "SELECT count(*) FROM daily_activity").Scan(&n)
	if n != 35 {
		t.Fatalf("daily_activity rows %d", n)
	}
}

func TestDailyHeartAndSleep(t *testing.T) {
	db, _ := setup(t, opts())
	ctx := context.Background()
	var rhr, hrv, hrMin, hrMax, hrSleep, spo2 float64
	if err := db.RO.QueryRowContext(ctx, "SELECT resting_hr, hrv_sdnn, hr_min, hr_max, hr_sleep_avg, spo2_avg FROM daily_heart WHERE day = '2026-01-07'").
		Scan(&rhr, &hrv, &hrMin, &hrMax, &hrSleep, &spo2); err != nil {
		t.Fatal(err)
	}
	if rhr < 56 || rhr > 59 || hrv < 40 || hrv > 60 || hrMax < 140 || hrSleep < 54 || hrSleep > 74 || spo2 < 95 {
		t.Fatalf("rhr=%v hrv=%v min=%v max=%v sleep=%v spo2=%v", rhr, hrv, hrMin, hrMax, hrSleep, spo2)
	}
	var asleep, deep, rem, inBed float64
	if err := db.RO.QueryRowContext(ctx, "SELECT asleep_min, deep_min, rem_min, in_bed_min FROM daily_sleep WHERE day = '2026-01-07'").
		Scan(&asleep, &deep, &rem, &inBed); err != nil {
		t.Fatal(err)
	}
	if asleep != 415 || deep != 50 || rem != 100 || inBed != 450 {
		t.Fatalf("asleep=%v deep=%v rem=%v inBed=%v", asleep, deep, rem, inBed)
	}
	var wrist int
	db.RO.QueryRowContext(ctx, "SELECT count(*) FROM daily_heart WHERE wrist_temp_dev IS NOT NULL").Scan(&wrist)
	if wrist == 0 {
		t.Fatal("no wrist temperature deviation computed")
	}
}

func TestWorkoutMetricsAndLoad(t *testing.T) {
	db, _ := setup(t, opts())
	ctx := context.Background()
	var moving, avg, edwards, pct float64
	var zones string
	if err := db.RO.QueryRowContext(ctx, "SELECT moving_s, avg_hr, trimp_edwards, pct_z3_plus, zone_s_json FROM workout_metrics LIMIT 1").
		Scan(&moving, &avg, &edwards, &pct, &zones); err != nil {
		t.Fatal(err)
	}
	if moving != 3480 || avg < 100 || edwards <= 0 || pct <= 0 {
		t.Fatalf("moving=%v avg=%v edwards=%v pct=%v zones=%s", moving, avg, edwards, pct, zones)
	}
	var hrr float64
	db.RO.QueryRowContext(ctx, "SELECT ifnull(hrr60, -1) FROM workout_metrics LIMIT 1").Scan(&hrr)
	if hrr <= 0 {
		t.Fatalf("hrr60 = %v", hrr)
	}
	var days, sessions int
	var ctl float64
	db.RO.QueryRowContext(ctx, "SELECT count(*), sum(sessions), max(ctl) FROM daily_load").Scan(&days, &sessions, &ctl)
	if sessions != 15 || ctl <= 0 || days < 34 {
		t.Fatalf("load days=%d sessions=%d ctl=%v", days, sessions, ctl)
	}
}

func TestZonesAndPauses(t *testing.T) {
	lower := [5]float64{100, 120, 140, 160, 180}
	samples := []HRSample{{0, 90}, {10_000, 125}, {20_000, 150}, {120_000, 185}}
	z, ban := IntegrateZones(samples, 130_000, lower, 60, 190, false)
	if z.Below != 10 || z.Z[1] != 10 || z.Z[2] != 30 || z.Z[4] != 10 {
		t.Fatalf("zones %+v", z)
	}
	if ban <= 0 || EdwardsTRIMP(z) != (10.0*2+30*3+10*5)/60 {
		t.Fatalf("trimp edwards=%v banister=%v", EdwardsTRIMP(z), ban)
	}
	p := PauseIntervals([]WorkoutEvent{{"HKWorkoutEventTypeResume", 50}, {"HKWorkoutEventTypePause", 20}, {"Pause", 80}}, 0, 100)
	if len(p) != 2 || p[0] != [2]int64{20, 50} || p[1] != [2]int64{80, 100} {
		t.Fatalf("pauses %+v", p)
	}
}

func TestStreaksAndSedentary(t *testing.T) {
	weeks := []Week{{Met: true}, {Met: false}, {Met: true}, {Met: true}, {Met: false, Partial: true}}
	if c, l := Streaks(weeks); c != 2 || l != 2 {
		t.Fatalf("streaks %d %d", c, l)
	}
	var steps [24]float64
	for h := 8; h < 23; h++ {
		steps[h] = 300
	}
	steps[10], steps[11], steps[12] = 0, 0, 0
	if s := longestSedentary(steps, map[int]bool{11: true}, 8, 23); s != 1 {
		t.Fatalf("sedentary %v", s)
	}
	if s := longestSedentary(steps, nil, 8, 23); s != 3 {
		t.Fatalf("sedentary %v", s)
	}
}

func TestSRPEScaleAndWorkoutLoad(t *testing.T) {
	trimp := func(v float64) sql.NullFloat64 { return sql.NullFloat64{Float64: v, Valid: true} }
	var ws []loadRow
	for i := range 5 {
		ws = append(ws, loadRow{edwards: trimp(100), srpe: trimp(float64(300 + 100*(i%2)))}) // Σ sRPE 1700 / Σ TRIMP 500
	}
	unrated := loadRow{edwards: trimp(80)}
	ws = append(ws, unrated)
	sc := srpeScale(ws)
	if sc.Rated != 5 || math.Abs(sc.Factor-3.4) > 1e-9 {
		t.Fatalf("scale %+v, want factor 3.4 from 5 rated", sc)
	}
	if got := workoutLoad("srpe", unrated, sc); math.Abs(got-272) > 1e-9 {
		t.Fatalf("unrated sRPE load %v, want 80 × 3.4", got)
	}
	if got := workoutLoad("srpe", ws[0], sc); got != 300 {
		t.Fatalf("rated sRPE load %v, want its own sRPE", got)
	}
	if got := workoutLoad("edwards", ws[0], sc); got != 100 {
		t.Fatalf("edwards load %v", got)
	}
	// Too few rated workouts: TRIMP is used as is.
	if sc := srpeScale(ws[3:]); sc.Factor != 1 || sc.Rated != 2 {
		t.Fatalf("scale with 2 rated: %+v", sc)
	}
}

func TestRingGoals(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenUser(ctx, t.TempDir(), "rings")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.W.ExecContext(ctx, `INSERT INTO activity_summaries (day, exercise_min_goal, stand_hours_goal) VALUES
		('2026-10-01', 30, 12), ('2026-10-02', 45, 10)`); err != nil {
		t.Fatal(err)
	}
	s := DefaultSettings()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	r, err := Resolve(ctx, db.RO, s, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.GoalsSource != "settings" || r.Goals.ExerciseMin != 30 || r.Goals.StandHours != 12 {
		t.Fatalf("without ring goals: %s %+v", r.GoalsSource, r.Goals)
	}
	s.Goals.UseRingGoals = true
	if r, err = Resolve(ctx, db.RO, s, now); err != nil {
		t.Fatal(err)
	}
	if r.GoalsSource != "rings" || r.Goals.ExerciseMin != 45 || r.Goals.StandHours != 10 || r.Goals.StepsPerDay != 8000 {
		t.Fatalf("with ring goals (latest day): %s %+v", r.GoalsSource, r.Goals)
	}
}
