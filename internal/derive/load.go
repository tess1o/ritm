package derive

import (
	"context"
	"database/sql"
	"slices"

	"ritm/internal/days"
	"ritm/internal/mathx"
	"ritm/internal/store"
)

const (
	ctlDays = 42
	atlDays = 7
)

// MinRatedForScale is how many workouts with RPE the sRPE model needs before it scales TRIMP
// to sRPE units for the unrated ones; with fewer it uses TRIMP as is.
const MinRatedForScale = 5

// srpeScaleKey stores the SRPEScale used by the last recompute.
const srpeScaleKey = "derive.srpe_scale"

// SRPEScale describes how the sRPE load model fills in workouts without RPE: their Edwards TRIMP
// times Factor, where Factor = Σ sRPE / Σ TRIMP over the Rated workouts that have both.
type SRPEScale struct {
	Factor float64 `json:"factor"`
	Rated  int     `json:"rated"`
}

// LoadSRPEScale returns the scale stored by the last recompute (Factor 1 when none).
func LoadSRPEScale(ctx context.Context, q store.Querier) (SRPEScale, error) {
	sc := SRPEScale{Factor: 1}
	_, err := store.GetSetting(ctx, q, srpeScaleKey, &sc)
	return sc, err
}

type loadRow struct {
	id                      int64
	day                     days.Day
	typ                     string
	edwards, banister, srpe sql.NullFloat64
}

// workoutLoad is one workout's load in the selected model. sRPE (RPE × minutes) and TRIMP
// are different scales, so unrated workouts get TRIMP converted with the scale factor.
func workoutLoad(model string, w loadRow, sc SRPEScale) float64 {
	switch model {
	case "banister":
		return w.banister.Float64
	case "srpe":
		if w.srpe.Valid {
			return w.srpe.Float64
		}
		return w.edwards.Float64 * sc.Factor
	}
	return w.edwards.Float64
}

// srpeScale computes the TRIMP → sRPE factor from workouts that have both values.
func srpeScale(ws []loadRow) SRPEScale {
	var sumS, sumT float64
	n := 0
	for _, w := range ws {
		if w.srpe.Valid && w.edwards.Valid && w.edwards.Float64 > 0 {
			sumS, sumT, n = sumS+w.srpe.Float64, sumT+w.edwards.Float64, n+1
		}
	}
	if n < MinRatedForScale || sumT == 0 {
		return SRPEScale{Factor: 1, Rated: n}
	}
	return SRPEScale{Factor: mathx.Round(sumS/sumT, 3), Rated: n}
}

// computeLoad stores each workout's load and rebuilds daily_load: daily load, CTL/ATL EWMAs, TSB
// and ACWR.
func (e *Engine) computeLoad(ctx context.Context, tx *sql.Tx, res *Resolved, lastDay days.Day) error {
	rows, err := tx.QueryContext(ctx, `SELECT w.id, w.start_ms, w.tz_offset_min, w.activity_type, m.trimp_edwards, m.trimp_banister, m.srpe_load
		FROM workouts w LEFT JOIN workout_metrics m ON m.workout_id = w.id ORDER BY w.start_ms`)
	if err != nil {
		return err
	}
	var ws []loadRow
	for rows.Next() {
		var w loadRow
		var ms int64
		var off int
		if err := rows.Scan(&w.id, &ms, &off, &w.typ, &w.edwards, &w.banister, &w.srpe); err != nil {
			rows.Close()
			return err
		}
		w.day = days.FromMS(ms, off)
		ws = append(ws, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	sc := srpeScale(ws)
	if err := store.SetSetting(ctx, tx, srpeScaleKey, sc); err != nil {
		return err
	}
	upd, err := tx.PrepareContext(ctx, "UPDATE workout_metrics SET load = ? WHERE workout_id = ?")
	if err != nil {
		return err
	}
	defer upd.Close()
	load := map[days.Day]float64{}
	sessions := map[days.Day]int{}
	first := days.Day(-1)
	for _, w := range ws {
		l := workoutLoad(res.LoadModel, w, sc)
		if _, err := upd.ExecContext(ctx, mathx.Round(l, 1), w.id); err != nil {
			return err
		}
		if first < 0 || w.day < first {
			first = w.day
		}
		load[w.day] += l
		if res.IsSessionType(w.typ) {
			sessions[w.day]++
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM daily_load"); err != nil {
		return err
	}
	if first < 0 {
		return nil
	}
	ins, err := tx.PrepareContext(ctx, "INSERT INTO daily_load (day, load, ctl, atl, tsb, acwr, sessions) VALUES (?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer ins.Close()
	var ctl, atl float64
	var window []float64
	for d := first; d <= lastDay; d++ {
		l := load[d]
		ctl += (l - ctl) / ctlDays
		atl += (l - atl) / atlDays
		window = append(window, l)
		if len(window) > 28 {
			window = window[1:]
		}
		var acwr any
		if len(window) == 28 {
			acute, chronic := mathx.Mean(window[21:]), mathx.Mean(window)
			if chronic > 0 {
				acwr = mathx.Round(acute/chronic, 2)
			}
		}
		if _, err := ins.ExecContext(ctx, d.String(), mathx.Round(l, 1), mathx.Round(ctl, 2), mathx.Round(atl, 2), mathx.Round(ctl-atl, 2), acwr, sessions[d]); err != nil {
			return err
		}
	}
	return nil
}

// Week is one ISO week of training consistency.
type Week struct {
	Start    string  `json:"week_start"`
	Sessions int     `json:"sessions"`
	Target   int     `json:"target"`
	Met      bool    `json:"met"`
	Hours    float64 `json:"hours"`
	Load     float64 `json:"load"`
	Partial  bool    `json:"partial"` // the week is still in progress
}

// WeeklySessions returns ISO weeks between from and to with counts of sessions (workouts of types).
func WeeklySessions(ctx context.Context, q store.Querier, r days.Range, target int, types []string, today days.Day) ([]Week, error) {
	start := r.From.ISOWeekStart()
	rows, err := q.QueryContext(ctx, `SELECT strftime('%Y-%m-%d', (start_ms / 1000) + tz_offset_min * 60, 'unixepoch') AS d,
		activity_type, duration_s FROM workouts WHERE start_ms >= ? AND start_ms < ?`,
		int64(start-1)*msDay, int64(r.To+2)*msDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	weeks := map[days.Day]*Week{}
	var order []days.Day
	for w := start; w <= r.To; w += 7 {
		wk := &Week{Start: w.String(), Target: target, Partial: today >= w && today < w+7}
		weeks[w] = wk
		order = append(order, w)
	}
	for rows.Next() {
		var ds, typ string
		var dur sql.NullFloat64
		if err := rows.Scan(&ds, &typ, &dur); err != nil {
			return nil, err
		}
		d, err := days.Parse(ds)
		if err != nil || d < start || d > r.To || !slices.Contains(types, typ) {
			continue
		}
		if wk := weeks[d.ISOWeekStart()]; wk != nil {
			wk.Sessions++
			wk.Hours += dur.Float64 / 3600
		}
	}
	lr, err := q.QueryContext(ctx, "SELECT day, load FROM daily_load WHERE day BETWEEN ? AND ?", start.String(), r.To.String())
	if err != nil {
		return nil, err
	}
	defer lr.Close()
	for lr.Next() {
		var ds string
		var l float64
		if err := lr.Scan(&ds, &l); err != nil {
			return nil, err
		}
		if d, err := days.Parse(ds); err == nil {
			if wk := weeks[d.ISOWeekStart()]; wk != nil {
				wk.Load += l
			}
		}
	}
	out := make([]Week, 0, len(order))
	for _, w := range order {
		wk := weeks[w]
		wk.Met = wk.Sessions >= target
		wk.Hours = mathx.Round(wk.Hours, 2)
		wk.Load = mathx.Round(wk.Load, 1)
		out = append(out, *wk)
	}
	return out, nil
}

// Streaks returns the current and longest run of consecutive weeks meeting the target.
// An in-progress week extends the current streak only once it meets the target.
func Streaks(weeks []Week) (current, longest int) {
	run := 0
	for _, w := range weeks {
		if w.Met {
			run++
			longest = max(longest, run)
		} else if !w.Partial {
			run = 0
		}
	}
	return run, longest
}
