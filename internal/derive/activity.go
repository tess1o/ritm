package derive

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"

	"ritm/internal/days"
	"ritm/internal/ingest"
	"ritm/internal/mathx"
	"ritm/internal/store"
)

// ActiveHourSteps is the step threshold for an hour to count as active (§7.2).
const ActiveHourSteps = 250

type hourKey struct {
	day  days.Day
	typ  string
	hour int
}

// hourlyTotals returns per-day, per-type, per-hour values using the highest-priority source that
// has data in each hour (SRC-2).
func (e *Engine) hourlyTotals(ctx context.Context, tx *sql.Tx, r days.Range, res *Resolved) (map[hourKey]float64, map[days.Day]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT type, source_name, local_day, local_hour, sum(value) FROM hourly_activity
		WHERE local_day BETWEEN ? AND ? GROUP BY type, source_name, local_day, local_hour`, r.From.String(), r.To.String())
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	rank := res.rank()
	type best struct {
		rank int
		v    float64
	}
	chosen := map[hourKey]best{}
	watchDays := map[days.Day]bool{}
	for rows.Next() {
		var (
			typ, src, dayS string
			hour           int
			v              float64
		)
		if err := rows.Scan(&typ, &src, &dayS, &hour, &v); err != nil {
			return nil, nil, err
		}
		d, err := days.Parse(dayS)
		if err != nil {
			continue
		}
		if res.isWatch(src) {
			watchDays[d] = true
		}
		k := hourKey{d, typ, hour}
		rk := rank(src)
		if b, ok := chosen[k]; !ok || rk < b.rank {
			chosen[k] = best{rk, v}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	out := make(map[hourKey]float64, len(chosen))
	for k, b := range chosen {
		out[k] = b.v
	}
	return out, watchDays, nil
}

type summaryRow struct {
	activeKcal, activeGoal, exMin, exGoal, standH, standGoal sql.NullFloat64
}

func (e *Engine) computeActivity(ctx context.Context, tx *sql.Tx, r days.Range, res *Resolved) error {
	totals, watchDays, err := e.hourlyTotals(ctx, tx, r, res)
	if err != nil {
		return err
	}

	// Stand hours (AppleStandHour "Stood"), keyed by local day and hour.
	stood := map[days.Day]map[int]bool{}
	fromMS, toMS := r.From.StartMS(e.zone.Location())-msDay, r.To.Add(1).StartMS(e.zone.Location())+msDay
	rows, err := tx.QueryContext(ctx, `SELECT start_ms, tz_offset_min FROM records
		WHERE type = ? AND category_value = 'HKCategoryValueAppleStandHourStood' AND start_ms >= ? AND start_ms < ?`,
		ingest.TypeStandHour, fromMS, toMS)
	if err != nil {
		return err
	}
	for rows.Next() {
		var ms int64
		var off int
		if err := rows.Scan(&ms, &off); err != nil {
			rows.Close()
			return err
		}
		d := days.FromMS(ms, off)
		if !r.Contains(d) {
			continue
		}
		if stood[d] == nil {
			stood[d] = map[int]bool{}
		}
		stood[d][days.LocalHour(ms, off)] = true
		watchDays[d] = true
	}
	rows.Close()

	gym := map[days.Day]bool{}
	rows, err = tx.QueryContext(ctx, "SELECT start_ms, tz_offset_min FROM workouts WHERE activity_type = ? AND start_ms >= ? AND start_ms < ?",
		res.MainType, fromMS, toMS)
	if err != nil {
		return err
	}
	for rows.Next() {
		var ms int64
		var off int
		if err := rows.Scan(&ms, &off); err != nil {
			rows.Close()
			return err
		}
		gym[days.FromMS(ms, off)] = true
	}
	rows.Close()

	summaries := map[days.Day]summaryRow{}
	rows, err = tx.QueryContext(ctx, `SELECT day, active_kcal, active_kcal_goal, exercise_min, exercise_min_goal, stand_hours, stand_hours_goal
		FROM activity_summaries WHERE day BETWEEN ? AND ?`, r.From.String(), r.To.String())
	if err != nil {
		return err
	}
	for rows.Next() {
		var dayS string
		var s summaryRow
		if err := rows.Scan(&dayS, &s.activeKcal, &s.activeGoal, &s.exMin, &s.exGoal, &s.standH, &s.standGoal); err != nil {
			rows.Close()
			return err
		}
		if d, err := days.Parse(dayS); err == nil {
			summaries[d] = s
		}
	}
	rows.Close()

	if _, err := tx.ExecContext(ctx, "DELETE FROM daily_activity WHERE day BETWEEN ? AND ?", r.From.String(), r.To.String()); err != nil {
		return err
	}
	ins, err := tx.PrepareContext(ctx, `INSERT INTO daily_activity (day, steps, distance_m, active_kcal, basal_kcal, total_kcal, flights,
		exercise_min, stand_min, stand_hours, active_hours, longest_sedentary_h, steps_by_hour_json, is_gym_day, summary_mismatch,
		ring_move_kcal, ring_move_goal, ring_exercise_min, ring_exercise_goal, ring_stand_hours, ring_stand_goal, has_watch_data)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()

	present := map[days.Day]bool{}
	for k := range totals {
		present[k.day] = true
	}
	for d := range summaries {
		present[d] = true
	}
	for d := range stood {
		present[d] = true
	}
	now := e.clock.Now().In(e.zone.Location())
	today := days.FromTime(now)
	for d := r.From; d <= r.To; d++ {
		if !present[d] {
			continue
		}
		var steps [24]float64
		sum := map[string]float64{}
		has := map[string]bool{}
		for _, typ := range ingest.CumulativeTypes {
			for h := 0; h < 24; h++ {
				if v, ok := totals[hourKey{d, typ, h}]; ok {
					sum[typ] += v
					has[typ] = true
					if typ == ingest.TypeSteps {
						steps[h] = v
					}
				}
			}
		}
		val := func(typ string, scale float64) any {
			if !has[typ] {
				return nil
			}
			return mathx.Round(sum[typ]*scale, 2)
		}
		activeHours := 0
		for _, s := range steps {
			if s >= ActiveHourSteps {
				activeHours++
			}
		}
		var sedentary any
		if watchDays[d] {
			endHour := res.WakingEndHour
			if d == today {
				endHour = min(endHour, now.Hour())
			}
			sedentary = longestSedentary(steps, stood[d], res.WakingStartHour, endHour)
		}
		var total any
		if has[ingest.TypeActiveEnergy] || has[ingest.TypeBasalEnergy] {
			total = mathx.Round(sum[ingest.TypeActiveEnergy]+sum[ingest.TypeBasalEnergy], 2)
		}
		s := summaries[d]
		var mismatch any
		if s.activeKcal.Valid && s.activeKcal.Float64 > 0 && has[ingest.TypeActiveEnergy] {
			mismatch = store.BoolInt(math.Abs(sum[ingest.TypeActiveEnergy]-s.activeKcal.Float64)/s.activeKcal.Float64 > 0.10)
		}
		hourJSON, _ := json.Marshal(roundAll(steps[:]))
		var standHours any
		if watchDays[d] {
			standHours = len(stood[d])
		}
		if _, err := ins.ExecContext(ctx, d.String(), val(ingest.TypeSteps, 1), val(ingest.TypeDistance, 1),
			val(ingest.TypeActiveEnergy, 1), val(ingest.TypeBasalEnergy, 1), total, val(ingest.TypeFlights, 1),
			val(ingest.TypeExerciseTime, 1.0/60), val(ingest.TypeStandTime, 1.0/60), standHours, activeHours, sedentary,
			string(hourJSON), store.BoolInt(gym[d]), mismatch,
			nullF(s.activeKcal), nullF(s.activeGoal), nullF(s.exMin), nullF(s.exGoal), nullF(s.standH), nullF(s.standGoal),
			store.BoolInt(watchDays[d])); err != nil {
			return err
		}
	}
	return nil
}

// longestSedentary returns the longest run of consecutive waking hours with fewer than
// ActiveHourSteps steps and no stand hour, in hours.
func longestSedentary(steps [24]float64, stood map[int]bool, start, end int) float64 {
	best, cur := 0, 0
	for h := start; h < end && h < 24; h++ {
		if steps[h] < ActiveHourSteps && !stood[h] {
			cur++
			best = max(best, cur)
		} else {
			cur = 0
		}
	}
	return float64(best)
}

const msDay = days.MSPerDay

func roundAll(v []float64) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = mathx.Round(x, 0)
	}
	return out
}

func nullF(v sql.NullFloat64) any {
	if !v.Valid {
		return nil
	}
	return v.Float64
}
