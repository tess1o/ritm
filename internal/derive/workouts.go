package derive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"ritm/internal/days"
	"ritm/internal/ingest"
	"ritm/internal/mathx"
	"ritm/internal/store"
)

// MaxHoldMS caps how long one HR sample is assumed to hold during zone integration (§7.5).
const MaxHoldMS = 30_000

type HRSample struct {
	MS  int64   `json:"t_ms"`
	BPM float64 `json:"bpm"`
}

// Zones holds seconds spent below zone 1 and in zones 1–5.
type Zones struct {
	Below float64    `json:"below"`
	Z     [5]float64 `json:"z"`
}

func (z Zones) Total() float64 {
	t := z.Below
	for _, v := range z.Z {
		t += v
	}
	return t
}

// ZoneOf returns 0 for below zone 1, else 1–5.
func ZoneOf(bpm float64, lower [5]float64) int {
	for i := 4; i >= 0; i-- {
		if bpm >= lower[i] {
			return i + 1
		}
	}
	return 0
}

// IntegrateZones integrates HR samples into zones; each sample holds until the next one, at most
// MaxHoldMS, and never past end. It also returns Banister TRIMP.
func IntegrateZones(samples []HRSample, end int64, lower [5]float64, restHR, maxHR float64, female bool) (Zones, float64) {
	var z Zones
	var banister float64
	a, b := 0.64, 1.92
	if female {
		a, b = 0.86, 1.67
	}
	for i, s := range samples {
		next := end
		if i+1 < len(samples) {
			next = samples[i+1].MS
		}
		dt := min(next-s.MS, MaxHoldMS, end-s.MS)
		if dt <= 0 {
			continue
		}
		sec := float64(dt) / 1000
		if zi := ZoneOf(s.BPM, lower); zi == 0 {
			z.Below += sec
		} else {
			z.Z[zi-1] += sec
		}
		if maxHR > restHR {
			hrr := math.Max(0, math.Min(1, (s.BPM-restHR)/(maxHR-restHR)))
			banister += sec / 60 * hrr * a * math.Exp(b*hrr)
		}
	}
	return z, banister
}

// EdwardsTRIMP is Σ minutes in zone z × z.
func EdwardsTRIMP(z Zones) float64 {
	var t float64
	for i, s := range z.Z {
		t += s / 60 * float64(i+1)
	}
	return t
}

// WorkoutEvent is a stored workout event; Type may carry the HKWorkoutEventType prefix or not.
type WorkoutEvent struct {
	Type    string
	StartMS int64
}

// PauseIntervals pairs pause/resume events into paused [start, end] intervals (epoch ms) clamped
// to the workout's [start, end].
func PauseIntervals(events []WorkoutEvent, start, end int64) [][2]int64 {
	events = slices.Clone(events)
	sort.SliceStable(events, func(i, j int) bool { return events[i].StartMS < events[j].StartMS })
	var out [][2]int64
	var pausedAt int64 = -1
	for _, ev := range events {
		switch strings.TrimPrefix(ev.Type, "HKWorkoutEventType") {
		case "Pause", "MotionPaused":
			if pausedAt < 0 {
				pausedAt = ev.StartMS
			}
		case "Resume", "MotionResumed":
			if pausedAt >= 0 {
				out = append(out, [2]int64{max(pausedAt, start), min(ev.StartMS, end)})
				pausedAt = -1
			}
		}
	}
	if pausedAt >= 0 && pausedAt < end {
		out = append(out, [2]int64{max(pausedAt, start), end})
	}
	return out
}

// WorkoutKey identifies a workout across re-imports for annotations.
func WorkoutKey(activityType string, startMS, endMS int64) string {
	return fmt.Sprintf("%s|%d|%d", activityType, startMS, endMS)
}

// WorkoutHR loads HR samples in [from, to), preferring the workout's own source.
func WorkoutHR(ctx context.Context, q store.Querier, source string, from, to int64) ([]HRSample, error) {
	rows, err := q.QueryContext(ctx, `SELECT source_name, start_ms, value FROM records
		WHERE type = ? AND start_ms >= ? AND start_ms < ? ORDER BY start_ms`, ingest.TypeHeartRate, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bySrc := map[string][]HRSample{}
	for rows.Next() {
		var src string
		var s HRSample
		if err := rows.Scan(&src, &s.MS, &s.BPM); err != nil {
			return nil, err
		}
		bySrc[src] = append(bySrc[src], s)
	}
	if s, ok := bySrc[source]; ok {
		return s, rows.Err()
	}
	var best []HRSample
	for _, s := range bySrc {
		if len(s) > len(best) {
			best = s
		}
	}
	return best, rows.Err()
}

type wstat struct {
	Sum *float64 `json:"sum"`
	Avg *float64 `json:"avg"`
	Max *float64 `json:"max"`
}

func (e *Engine) computeWorkouts(ctx context.Context, tx *sql.Tx, from days.Day, res *Resolved) error {
	rows, err := tx.QueryContext(ctx, `SELECT w.id, w.activity_type, w.source_name, w.start_ms, w.end_ms, w.tz_offset_min,
		w.duration_s, w.distance_m, w.energy_kcal, w.stats_json, a.rpe
		FROM workouts w LEFT JOIN workout_annotations a ON a.workout_key = w.activity_type || '|' || w.start_ms || '|' || w.end_ms
		WHERE w.start_ms >= ?`, from.StartMS(e.zone.Location())-msDay)
	if err != nil {
		return err
	}
	type wrow struct {
		id                int64
		typ, src          string
		start, end        int64
		off               int
		dur, dist, energy sql.NullFloat64
		stats             sql.NullString
		rpe               sql.NullInt64
	}
	var ws []wrow
	for rows.Next() {
		var w wrow
		if err := rows.Scan(&w.id, &w.typ, &w.src, &w.start, &w.end, &w.off, &w.dur, &w.dist, &w.energy, &w.stats, &w.rpe); err != nil {
			rows.Close()
			return err
		}
		if days.FromMS(w.start, w.off) >= from {
			ws = append(ws, w)
		}
	}
	rows.Close()

	female := strings.Contains(res.Sex, "Female")
	now := e.clock.Now().UnixMilli()
	for _, w := range ws {
		var events []WorkoutEvent
		er, err := tx.QueryContext(ctx, "SELECT type, start_ms FROM workout_events WHERE workout_id = ?", w.id)
		if err != nil {
			return err
		}
		for er.Next() {
			var ev WorkoutEvent
			if err := er.Scan(&ev.Type, &ev.StartMS); err != nil {
				er.Close()
				return err
			}
			events = append(events, ev)
		}
		er.Close()
		var paused float64
		for _, p := range PauseIntervals(events, w.start, w.end) {
			paused += float64(p[1]-p[0]) / 1000
		}
		moving := math.Max(0, float64(w.end-w.start)/1000-paused)

		samples, err := WorkoutHR(ctx, tx, w.src, w.start, w.end+120_000)
		if err != nil {
			return err
		}
		var inWin []HRSample
		for _, s := range samples {
			if s.MS <= w.end {
				inWin = append(inWin, s)
			}
		}
		var st map[string]wstat
		if w.stats.Valid {
			_ = json.Unmarshal([]byte(w.stats.String), &st)
		}
		var avgHR, maxHR, zoneJSON, pctZ3, edwards, banister any
		if len(inWin) > 0 {
			zones, ban := IntegrateZones(inWin, w.end, res.ZoneLowerBpm, res.RestingHRBpm, res.MaxHRBpm, female)
			_, hi, mean := mathx.MinMaxMean(hrVals(inWin))
			avgHR, maxHR = mathx.Round(mean, 1), hi
			zj, _ := json.Marshal(zones)
			zoneJSON = string(zj)
			if t := zones.Total(); t > 0 {
				pctZ3 = mathx.Round((zones.Z[2]+zones.Z[3]+zones.Z[4])/t*100, 1)
			}
			edwards, banister = mathx.Round(EdwardsTRIMP(zones), 1), mathx.Round(ban, 1)
		} else if hs, ok := st[ingest.TypeHeartRate]; ok {
			if hs.Avg != nil {
				avgHR = *hs.Avg
			}
			if hs.Max != nil {
				maxHR = *hs.Max
			}
		}
		var kcalPerMin any
		if w.energy.Valid && moving > 0 {
			kcalPerMin = mathx.Round(w.energy.Float64/(moving/60), 2)
		}
		var srpe any
		if w.rpe.Valid {
			srpe = mathx.Round(float64(w.rpe.Int64)*moving/60, 1)
		}
		hrr, err := e.hrr60(ctx, tx, w.start, w.end, samples)
		if err != nil {
			return err
		}
		var pace any
		if w.dist.Valid && w.dist.Float64 > 0 {
			pace = mathx.Round(moving/(w.dist.Float64/1000), 1)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO workout_metrics (workout_id, moving_s, avg_hr, max_hr, zone_s_json,
			pct_z3_plus, kcal_per_min, trimp_edwards, trimp_banister, srpe_load, hrr60, pace_s_per_km, computed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, w.id, mathx.Round(moving, 0), avgHR, maxHR, zoneJSON, pctZ3,
			kcalPerMin, edwards, banister, srpe, hrr, pace, now); err != nil {
			return err
		}
	}
	return nil
}

// hrr60 uses Apple's HeartRateRecoveryOneMinute when present, else the HR drop in the 60 s
// after the workout end if samples exist.
func (e *Engine) hrr60(ctx context.Context, tx *sql.Tx, start, end int64, samples []HRSample) (any, error) {
	var v sql.NullFloat64
	err := tx.QueryRowContext(ctx, `SELECT value FROM records WHERE type = ? AND start_ms >= ? AND start_ms <= ?
		ORDER BY abs(start_ms - ?) LIMIT 1`, ingest.TypeHRRecovery, start, end+3_600_000, end).Scan(&v)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if v.Valid {
		return mathx.Round(v.Float64, 1), nil
	}
	var atEnd, after *HRSample
	for i := range samples {
		s := &samples[i]
		if s.MS <= end && s.MS >= end-30_000 {
			atEnd = s
		}
		if d := s.MS - (end + 60_000); d >= -15_000 && d <= 15_000 {
			if after == nil || abs64(d) < abs64(after.MS-(end+60_000)) {
				after = s
			}
		}
	}
	if atEnd == nil || after == nil {
		return nil, nil
	}
	return atEnd.BPM - after.BPM, nil
}

func hrVals(s []HRSample) []float64 {
	out := make([]float64, len(s))
	for i, x := range s {
		out[i] = x.BPM
	}
	return out
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
