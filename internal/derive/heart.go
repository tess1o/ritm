package derive

import (
	"context"
	"database/sql"
	"sort"

	"ritm/internal/days"
	"ritm/internal/ingest"
	"ritm/internal/mathx"
	"ritm/internal/store"
)

var heartTypes = []string{
	ingest.TypeHeartRate, ingest.TypeRestingHR, ingest.TypeWalkingHR, ingest.TypeHRV, ingest.TypeVO2Max,
	ingest.TypeSpO2, ingest.TypeRespiratoryRate, ingest.TypeWristTemperature,
	ingest.TypeHighHREvent, ingest.TypeLowHREvent, ingest.TypeIrregularRhythm,
}

type daySeries struct {
	vals []float64
	ms   []int64
}

// computeHeart fills daily_heart. For each day and type, samples of the highest-priority
// source present that day are used.
func (e *Engine) computeHeart(ctx context.Context, tx *sql.Tx, r days.Range, res *Resolved, asleep map[days.Day][]interval) error {
	fromMS, toMS := r.From.StartMS(e.zone.Location())-msDay, r.To.Add(1).StartMS(e.zone.Location())+msDay
	// Wrist temperature deviation needs a 30-night baseline before the range.
	baseFrom := fromMS - 31*msDay

	type key struct {
		day days.Day
		typ string
	}
	rank := res.rank()
	type pick struct {
		rank int
		s    *daySeries
	}
	data := map[key]*pick{}
	events := map[days.Day]int{}
	wristByDay := map[days.Day][]float64{}
	type hrSample struct {
		ms int64
		v  float64
	}
	var allHR []hrSample // ordered by time (query order)

	// Only wrist temperature needs the baseline before the range; reading it for every type
	// would load an extra month of heart-rate samples per chunk.
	others := make([]any, 0, len(heartTypes))
	for _, t := range heartTypes {
		if t != ingest.TypeWristTemperature {
			others = append(others, t)
		}
	}
	q := `SELECT * FROM (
		SELECT type, source_name, start_ms, tz_offset_min, value FROM records
			WHERE type IN (` + store.Placeholders(len(others)) + `) AND start_ms >= ? AND start_ms < ?
		UNION ALL
		SELECT type, source_name, start_ms, tz_offset_min, value FROM records
			WHERE type = ? AND start_ms >= ? AND start_ms < ?) ORDER BY start_ms`
	args := append(others, fromMS, toMS, ingest.TypeWristTemperature, baseFrom, toMS)
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var (
			typ, src string
			ms       int64
			off      int
			v        sql.NullFloat64
		)
		if err := rows.Scan(&typ, &src, &ms, &off, &v); err != nil {
			rows.Close()
			return err
		}
		d := days.FromMS(ms, off)
		switch typ {
		case ingest.TypeHighHREvent, ingest.TypeLowHREvent, ingest.TypeIrregularRhythm:
			if r.Contains(d) {
				events[d]++
			}
			continue
		case ingest.TypeWristTemperature:
			if v.Valid {
				wristByDay[d] = append(wristByDay[d], v.Float64)
			}
			continue
		}
		if typ == ingest.TypeHeartRate && v.Valid && ms >= fromMS {
			allHR = append(allHR, hrSample{ms, v.Float64})
		}
		if !v.Valid || !r.Contains(d) {
			continue
		}
		k := key{d, typ}
		rk := rank(src)
		p, ok := data[k]
		if !ok || rk < p.rank {
			p = &pick{rk, &daySeries{}}
			data[k] = p
		} else if rk > p.rank {
			continue
		}
		p.s.vals = append(p.s.vals, v.Float64)
		p.s.ms = append(p.s.ms, ms)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM daily_heart WHERE day BETWEEN ? AND ?", r.From.String(), r.To.String()); err != nil {
		return err
	}
	ins, err := tx.PrepareContext(ctx, `INSERT INTO daily_heart (day, hr_min, hr_avg, hr_max, hr_sleep_avg, resting_hr, walking_hr,
		hrv_sdnn, vo2max, spo2_avg, spo2_min, resp_rate, wrist_temp_dev, heart_events, hr_samples)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()

	wristDays := make([]days.Day, 0, len(wristByDay))
	for d := range wristByDay {
		wristDays = append(wristDays, d)
	}
	sort.Slice(wristDays, func(i, j int) bool { return wristDays[i] < wristDays[j] })

	for d := r.From; d <= r.To; d++ {
		get := func(typ string) *daySeries {
			if p, ok := data[key{d, typ}]; ok {
				return p.s
			}
			return nil
		}
		hr := get(ingest.TypeHeartRate)
		var hrMin, hrAvg, hrMax, hrSleep, hrN any
		if hr != nil {
			lo, hi, mean := mathx.MinMaxMean(hr.vals)
			hrMin, hrMax, hrAvg, hrN = lo, hi, mathx.Round(mean, 1), len(hr.vals)
		}
		if iv := asleep[d]; len(iv) > 0 {
			// Night samples straddle midnight, so search all HR samples by time.
			var sum float64
			var n int
			for _, x := range iv {
				i := sort.Search(len(allHR), func(i int) bool { return allHR[i].ms >= x.start })
				for ; i < len(allHR) && allHR[i].ms < x.end; i++ {
					sum += allHR[i].v
					n++
				}
			}
			if n > 0 {
				hrSleep = mathx.Round(sum/float64(n), 1)
			}
		}
		meanOf := func(typ string, prec int) any {
			if s := get(typ); s != nil {
				_, _, m := mathx.MinMaxMean(s.vals)
				return mathx.Round(m, prec)
			}
			return nil
		}
		var vo2 any
		if s := get(ingest.TypeVO2Max); s != nil {
			vo2 = mathx.Round(s.vals[len(s.vals)-1], 2)
		}
		var spo2Min any
		if s := get(ingest.TypeSpO2); s != nil {
			lo, _, _ := mathx.MinMaxMean(s.vals)
			spo2Min = mathx.Round(lo, 1)
		}
		var wristDev any
		if vals, ok := wristByDay[d]; ok {
			_, _, today := mathx.MinMaxMean(vals)
			var base []float64
			for _, wd := range wristDays {
				if wd >= d.Add(-30) && wd < d {
					_, _, m := mathx.MinMaxMean(wristByDay[wd])
					base = append(base, m)
				}
			}
			if len(base) >= 5 {
				wristDev = mathx.Round(today-mathx.Median(base), 2)
			}
		}
		row := []any{hrMin, hrAvg, hrMax, hrSleep, meanOf(ingest.TypeRestingHR, 1), meanOf(ingest.TypeWalkingHR, 1),
			meanOf(ingest.TypeHRV, 1), vo2, meanOf(ingest.TypeSpO2, 1), spo2Min, meanOf(ingest.TypeRespiratoryRate, 1), wristDev}
		empty := events[d] == 0
		for _, v := range row {
			if v != nil {
				empty = false
			}
		}
		if empty {
			continue
		}
		args := append([]any{d.String()}, row...)
		args = append(args, events[d], hrN)
		if _, err := ins.ExecContext(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}
