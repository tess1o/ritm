package query

import (
	"context"
	"database/sql"
	"sort"

	"ritm/internal/days"
)

// CalendarDay is one day with data, for the date pickers' calendar.
type CalendarDay struct {
	Day      string   `json:"day"`
	Activity bool     `json:"activity" doc:"Daily activity is stored for the day"`
	Steps    *float64 `json:"steps"`
	Watch    bool     `json:"watch" doc:"The Watch recorded the day; without it, steps come from the phone alone"`
	Workouts int      `json:"workouts" doc:"Workouts that started that local day"`
	Route    bool     `json:"route" doc:"At least one of the day's workouts has a GPS route"`
}

// Calendar lists the days in r that have daily activity or a workout, in day order. Days with
// neither are left out. daily_load is not consulted: it has a row for every day after the
// first workout.
func (s *Service) Calendar(ctx context.Context, r days.Range) ([]CalendarDay, error) {
	byDay := map[string]*CalendarDay{}
	rows, err := s.db.RO.QueryContext(ctx, "SELECT day, steps, coalesce(has_watch_data, 0) FROM daily_activity WHERE day BETWEEN ? AND ?", r.From.String(), r.To.String())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var day string
		var steps sql.NullFloat64
		var watch bool
		if err := rows.Scan(&day, &steps, &watch); err != nil {
			rows.Close()
			return nil, err
		}
		d := &CalendarDay{Day: day, Activity: true, Watch: watch}
		if steps.Valid {
			d.Steps = &steps.Float64
		}
		byDay[day] = d
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// start_ms bounds (a day wider, for UTC offsets) let SQLite use the workouts_start index, as in Workouts.
	from, to := s.dayBounds(r)
	rows, err = s.db.RO.QueryContext(ctx, `SELECT local_day, count(*), max(has_route) FROM v_workouts
		WHERE start_ms >= ? AND start_ms < ? AND local_day BETWEEN ? AND ? GROUP BY local_day`,
		from-days.MSPerDay, to+days.MSPerDay, r.From.String(), r.To.String())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var day string
		var n, route int
		if err := rows.Scan(&day, &n, &route); err != nil {
			rows.Close()
			return nil, err
		}
		d := byDay[day]
		if d == nil {
			d = &CalendarDay{Day: day}
			byDay[day] = d
		}
		d.Workouts, d.Route = n, route == 1
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]CalendarDay, 0, len(byDay))
	for _, d := range byDay {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out, nil
}

// DataExtent is the first and last day with daily activity or a workout; both are nil without data.
func (s *Service) DataExtent(ctx context.Context) (first, last *string, err error) {
	var f, l sql.NullString
	err = s.db.RO.QueryRowContext(ctx, `WITH w AS (SELECT min(start_ms + tz_offset_min * 60000) lo, max(start_ms + tz_offset_min * 60000) hi FROM workouts)
		SELECT min(d), max(d) FROM (
			SELECT min(day) d FROM daily_activity UNION ALL SELECT max(day) FROM daily_activity
			UNION ALL SELECT strftime('%Y-%m-%d', lo / 1000, 'unixepoch') FROM w
			UNION ALL SELECT strftime('%Y-%m-%d', hi / 1000, 'unixepoch') FROM w)`).Scan(&f, &l)
	if err != nil {
		return nil, nil, err
	}
	if f.Valid {
		first = &f.String
	}
	if l.Valid {
		last = &l.String
	}
	return first, last, nil
}
