// Package insights is the deterministic, rule-based insights engine.
package insights

import (
	"context"
	"database/sql"
	"math"
	"time"

	"ritm/internal/days"
	"ritm/internal/mathx"
	"ritm/internal/store"
)

// Day holds the derived values of one local day; missing values are NaN.
type Day struct {
	Steps, DistanceM, ActiveKcal, StandHours, ExerciseMin, Sedentary float64
	MoveKcal, MoveGoal, ExGoal, StandGoal                            float64
	Watch, Gym                                                       bool
	RestingHR, WalkingHR, HRV, VO2Max                                float64
	HeartEvents                                                      int
	AsleepMin                                                        float64
	Load, CTL, ATL, TSB, ACWR                                        float64
	Sessions                                                         int
}

// Workout is a main-type session used by training rules.
type Workout struct {
	Day       days.Day
	DurationS float64
	AvgHR     float64
	PctZ3     float64
}

// Data is everything the rules need, loaded once per evaluation.
type Data struct {
	Days     map[days.Day]*Day
	Workouts []Workout // sessions (Resolved.SessionTypesEffective), ordered by day
	First    days.Day
	Anchor   days.Day // last day with Watch data
	Today    days.Day
}

func f(v sql.NullFloat64) float64 {
	if !v.Valid {
		return math.NaN()
	}
	return v.Float64
}

// Load reads v_daily and main-type workouts from day from (negative = all history) to today.
func Load(ctx context.Context, q store.Querier, sessionTypes []string, from, today days.Day) (*Data, error) {
	rows, err := q.QueryContext(ctx, `SELECT d.day, d.steps, d.distance_m, d.active_kcal, d.stand_hours, d.exercise_min, d.longest_sedentary_h,
		d.ring_move_kcal, d.ring_move_goal, d.ring_exercise_goal, d.ring_stand_goal, ifnull(a.has_watch_data, 0), ifnull(d.is_gym_day, 0),
		d.resting_hr_bpm, d.walking_hr_bpm, d.hrv_sdnn_ms, d.vo2max_ml_kg_min, ifnull(d.heart_events, 0), ifnull(h.hr_samples, 0),
		d.asleep_min, d.load, d.ctl, d.atl, d.tsb, d.acwr, ifnull(d.sessions, 0)
		FROM v_daily d LEFT JOIN daily_activity a ON a.day = d.day LEFT JOIN daily_heart h ON h.day = d.day
		WHERE d.day BETWEEN ? AND ? ORDER BY d.day`, max(from, 0).String(), today.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	data := &Data{Days: map[days.Day]*Day{}, Today: today, First: -1, Anchor: -1}
	for rows.Next() {
		var (
			ds                                                    string
			steps, dist, kcal, standH, exMin, sed, mk, mg, eg, sg sql.NullFloat64
			watch, gym, events, hrSamples, sessions               int
			rhr, whr, hrv, vo2, asleep, load, ctl, atl, tsb, acwr sql.NullFloat64
		)
		if err := rows.Scan(&ds, &steps, &dist, &kcal, &standH, &exMin, &sed, &mk, &mg, &eg, &sg, &watch, &gym,
			&rhr, &whr, &hrv, &vo2, &events, &hrSamples, &asleep, &load, &ctl, &atl, &tsb, &acwr, &sessions); err != nil {
			return nil, err
		}
		d, err := days.Parse(ds)
		if err != nil {
			continue
		}
		day := &Day{Steps: f(steps), DistanceM: f(dist), ActiveKcal: f(kcal), StandHours: f(standH), ExerciseMin: f(exMin),
			Sedentary: f(sed), MoveKcal: f(mk), MoveGoal: f(mg), ExGoal: f(eg), StandGoal: f(sg),
			Watch: watch == 1 || hrSamples > 0, Gym: gym == 1,
			RestingHR: f(rhr), WalkingHR: f(whr), HRV: f(hrv), VO2Max: f(vo2), HeartEvents: events,
			AsleepMin: f(asleep), Load: f(load), CTL: f(ctl), ATL: f(atl), TSB: f(tsb), ACWR: f(acwr), Sessions: sessions}
		data.Days[d] = day
		if data.First < 0 {
			data.First = d
		}
		if day.Watch {
			data.Anchor = d
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	wr, err := q.QueryContext(ctx, `SELECT local_day, ifnull(duration_s, 0), ifnull(avg_hr_bpm, 0), ifnull(pct_z3_plus, -1)
		FROM v_workouts WHERE activity_type IN (`+store.Placeholders(len(sessionTypes))+`) AND start_ms >= ? ORDER BY start_ms`,
		append(store.Anys(sessionTypes), (max(from, 0)-1).StartMS(time.UTC))...)
	if err != nil {
		return nil, err
	}
	defer wr.Close()
	for wr.Next() {
		var ds string
		var w Workout
		if err := wr.Scan(&ds, &w.DurationS, &w.AvgHR, &w.PctZ3); err != nil {
			return nil, err
		}
		if d, err := days.Parse(ds); err == nil && d >= from && d <= today {
			w.Day = d
			data.Workouts = append(data.Workouts, w)
		}
	}
	return data, wr.Err()
}

// Metric extracts a value from a day; NaN means missing.
type Metric func(*Day) float64

var (
	mSteps     Metric = func(d *Day) float64 { return watchOnly(d, d.Steps) }
	mDistance  Metric = func(d *Day) float64 { return watchOnly(d, d.DistanceM) }
	mKcal      Metric = func(d *Day) float64 { return watchOnly(d, d.ActiveKcal) }
	mSedentary Metric = func(d *Day) float64 { return d.Sedentary }
	mRHR       Metric = func(d *Day) float64 { return d.RestingHR }
	mWHR       Metric = func(d *Day) float64 { return d.WalkingHR }
	mHRV       Metric = func(d *Day) float64 { return d.HRV }
	mVO2       Metric = func(d *Day) float64 { return d.VO2Max }
	mSleep     Metric = func(d *Day) float64 { return d.AsleepMin }
)

// watchOnly hides activity values on days without Watch data, so a day the Watch was not worn
// does not look like a sedentary day.
func watchOnly(d *Day, v float64) float64 {
	if !d.Watch {
		return math.NaN()
	}
	return v
}

// values returns the metric's non-missing values in r.
func (data *Data) values(m Metric, r days.Range, filter func(days.Day, *Day) bool) []float64 {
	var out []float64
	for d := r.From; d <= r.To; d++ {
		day, ok := data.Days[d]
		if !ok || (filter != nil && !filter(d, day)) {
			continue
		}
		if v := m(day); !math.IsNaN(v) {
			out = append(out, v)
		}
	}
	return out
}

func (data *Data) mean(m Metric, r days.Range) (float64, int) {
	v := data.values(m, r, nil)
	return mathx.Mean(v), len(v)
}

// weeklyMeans returns the per-week means of the 7-day blocks ending before r.From (most recent
// last), skipping weeks with fewer than minDays values.
func (data *Data) weeklyMeans(m Metric, before days.Day, weeks, minDays int) []float64 {
	var out []float64
	for i := weeks; i >= 1; i-- {
		r := days.Range{From: before.Add(-7 * i), To: before.Add(-7*i + 6)}
		v := data.values(m, r, nil)
		if len(v) >= minDays {
			out = append(out, mathx.Mean(v))
		}
	}
	return out
}

func (data *Data) watchDays(r days.Range) int {
	n := 0
	for d := r.From; d <= r.To; d++ {
		if day, ok := data.Days[d]; ok && day.Watch {
			n++
		}
	}
	return n
}

func (data *Data) workoutsIn(r days.Range) []Workout {
	var out []Workout
	for _, w := range data.Workouts {
		if r.Contains(w.Day) {
			out = append(out, w)
		}
	}
	return out
}
