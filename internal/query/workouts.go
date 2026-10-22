package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"ritm/internal/days"
	"ritm/internal/derive"
	"ritm/internal/ingest"
	"ritm/internal/mathx"
)

type WorkoutSummary struct {
	ID           int64    `json:"id"`
	ActivityType string   `json:"activity_type"`
	Name         string   `json:"name"`
	Source       string   `json:"source"`
	Day          string   `json:"day"`
	StartMS      int64    `json:"start_ms"`
	EndMS        int64    `json:"end_ms"`
	TZOffsetMin  int      `json:"tz_offset_min"`
	DurationS    *float64 `json:"duration_s"`
	MovingS      *float64 `json:"moving_s"`
	DistanceM    *float64 `json:"distance_m"`
	EnergyKcal   *float64 `json:"energy_kcal"`
	Indoor       *int64   `json:"indoor"`
	AvgHRBpm     *float64 `json:"avg_hr_bpm"`
	MaxHRBpm     *float64 `json:"max_hr_bpm"`
	PctZ3Plus    *float64 `json:"pct_z3_plus"`
	KcalPerMin   *float64 `json:"kcal_per_min"`
	TrimpEdwards *float64 `json:"trimp_edwards"`
	TrimpBanist  *float64 `json:"trimp_banister"`
	SRPELoad     *float64 `json:"srpe_load"`
	Load         *float64 `json:"load"` // in the selected load model
	HRR60Bpm     *float64 `json:"hrr60_bpm"`
	PaceSPerKm   *float64 `json:"pace_s_per_km"`
	RPE          *int64   `json:"rpe"`
	Notes        *string  `json:"notes"`
	HasRoute     bool     `json:"has_route"`
}

type WorkoutFilter struct {
	Range        days.Range
	ActivityType string
}

const workoutCols = `id, activity_type, source_name, local_day, start_ms, end_ms, tz_offset_min, duration_s, moving_s, distance_m,
	energy_kcal, indoor, avg_hr_bpm, max_hr_bpm, pct_z3_plus, kcal_per_min, trimp_edwards, trimp_banister, srpe_load, load, hrr60_bpm,
	pace_s_per_km, rpe, notes, has_route`

func scanWorkout(sc interface{ Scan(...any) error }) (WorkoutSummary, error) {
	var w WorkoutSummary
	var f [15]sql.NullFloat64
	var indoor, rpe sql.NullInt64
	var notes sql.NullString
	err := sc.Scan(&w.ID, &w.ActivityType, &w.Source, &w.Day, &w.StartMS, &w.EndMS, &w.TZOffsetMin, &f[0], &f[1], &f[2], &f[3], &indoor,
		&f[4], &f[5], &f[6], &f[7], &f[8], &f[9], &f[10], &f[11], &f[12], &f[13], &rpe, &notes, &w.HasRoute)
	if err != nil {
		return w, err
	}
	w.DurationS, w.MovingS, w.DistanceM, w.EnergyKcal = nf(f[0]), nf(f[1]), nf(f[2]), nf(f[3])
	w.AvgHRBpm, w.MaxHRBpm, w.PctZ3Plus, w.KcalPerMin = nf(f[4]), nf(f[5]), nf(f[6]), nf(f[7])
	w.TrimpEdwards, w.TrimpBanist, w.SRPELoad, w.Load = nf(f[8]), nf(f[9]), nf(f[10]), nf(f[11])
	w.HRR60Bpm, w.PaceSPerKm = nf(f[12]), nf(f[13])
	w.Indoor, w.RPE = ni(indoor), ni(rpe)
	if notes.Valid {
		w.Notes = &notes.String
	}
	w.Name = ingest.WorkoutName(w.ActivityType)
	return w, nil
}

// Workouts lists workouts, newest first, capped at limit; truncated reports whether more exist.
func (s *Service) Workouts(ctx context.Context, f WorkoutFilter, limit int) ([]WorkoutSummary, bool, error) {
	// local_day is computed, so start_ms bounds (a day wider, for UTC offsets) let SQLite use the
	// workouts_start index.
	from, to := s.dayBounds(f.Range)
	q := "SELECT " + workoutCols + " FROM v_workouts WHERE start_ms >= ? AND start_ms < ? AND local_day BETWEEN ? AND ?"
	args := []any{from - days.MSPerDay, to + days.MSPerDay, f.Range.From.String(), f.Range.To.String()}
	if f.ActivityType != "" {
		q += " AND activity_type = ?"
		args = append(args, f.ActivityType)
	}
	q += " ORDER BY start_ms DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.RO.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []WorkoutSummary{}
	for rows.Next() {
		w, err := scanWorkout(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, w)
	}
	truncated := len(out) > limit
	if truncated {
		out = out[:limit]
	}
	return out, truncated, rows.Err()
}

type WorkoutEvent struct {
	Type    string `json:"type"`
	StartMS int64  `json:"start_ms"`
	EndMS   *int64 `json:"end_ms"`
}

type ZoneTime struct {
	Zone     int     `json:"zone"` // 0 = below zone 1
	LowerBpm float64 `json:"lower_bpm"`
	Seconds  float64 `json:"seconds"`
}

type WorkoutDetail struct {
	WorkoutSummary
	Zones        []ZoneTime                `json:"zones"`
	Events       []WorkoutEvent            `json:"events"`
	Pauses       [][2]int64                `json:"pauses_ms"`
	Stats        map[string]map[string]any `json:"statistics"`
	Metadata     map[string]any            `json:"metadata"`
	PreviousID   *int64                    `json:"previous_same_type_id"`
	ZoneLowerBpm [5]float64                `json:"zone_lower_bpm"`
	MaxHRSetting float64                   `json:"max_hr_bpm_setting"`
	LoadModel    string                    `json:"load_model"`
}

var ErrNotFound = errors.New("not found")

func (s *Service) Workout(ctx context.Context, id int64) (*WorkoutDetail, error) {
	row := s.db.RO.QueryRowContext(ctx, "SELECT "+workoutCols+" FROM v_workouts WHERE id = ?", id)
	w, err := scanWorkout(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d := &WorkoutDetail{WorkoutSummary: w, Events: []WorkoutEvent{}, Pauses: [][2]int64{}, Zones: []ZoneTime{},
		Stats: map[string]map[string]any{}, Metadata: map[string]any{}}
	var stats, meta, zones sql.NullString
	if err := s.db.RO.QueryRowContext(ctx, `SELECT w.stats_json, w.metadata_json, m.zone_s_json FROM workouts w
		LEFT JOIN workout_metrics m ON m.workout_id = w.id WHERE w.id = ?`, id).Scan(&stats, &meta, &zones); err != nil {
		return nil, err
	}
	if stats.Valid {
		_ = json.Unmarshal([]byte(stats.String), &d.Stats)
	}
	if meta.Valid {
		_ = json.Unmarshal([]byte(meta.String), &d.Metadata)
	}
	res, err := s.Resolved(ctx)
	if err != nil {
		return nil, err
	}
	d.ZoneLowerBpm, d.MaxHRSetting, d.LoadModel = res.ZoneLowerBpm, res.MaxHRBpm, res.LoadModel
	if zones.Valid {
		var z derive.Zones
		if json.Unmarshal([]byte(zones.String), &z) == nil {
			d.Zones = append(d.Zones, ZoneTime{0, 0, z.Below})
			for i, sec := range z.Z {
				d.Zones = append(d.Zones, ZoneTime{i + 1, res.ZoneLowerBpm[i], sec})
			}
		}
	}
	rows, err := s.db.RO.QueryContext(ctx, "SELECT type, start_ms, end_ms FROM workout_events WHERE workout_id = ? ORDER BY start_ms", id)
	if err != nil {
		return nil, err
	}
	var evs []derive.WorkoutEvent
	for rows.Next() {
		var e WorkoutEvent
		var end sql.NullInt64
		if err := rows.Scan(&e.Type, &e.StartMS, &end); err != nil {
			rows.Close()
			return nil, err
		}
		e.EndMS = ni(end)
		e.Type = strings.TrimPrefix(e.Type, "HKWorkoutEventType")
		d.Events = append(d.Events, e)
		evs = append(evs, derive.WorkoutEvent{Type: e.Type, StartMS: e.StartMS})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if p := derive.PauseIntervals(evs, w.StartMS, w.EndMS); p != nil {
		d.Pauses = p
	}
	var prev int64
	err = s.db.RO.QueryRowContext(ctx, "SELECT id FROM workouts WHERE activity_type = ? AND start_ms < ? ORDER BY start_ms DESC LIMIT 1",
		w.ActivityType, w.StartMS).Scan(&prev)
	if err == nil {
		d.PreviousID = &prev
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return d, nil
}

// WorkoutHR returns the workout's HR curve (plus 2 minutes after the end), downsampled to ~points.
func (s *Service) WorkoutHR(ctx context.Context, id int64, points int) ([]HRPoint, error) {
	var src string
	var start, end int64
	err := s.db.RO.QueryRowContext(ctx, "SELECT source_name, start_ms, end_ms FROM workouts WHERE id = ?", id).Scan(&src, &start, &end)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	samples, err := derive.WorkoutHR(ctx, s.db.RO, src, start, end+120_000)
	if err != nil {
		return nil, err
	}
	if points <= 0 {
		points = 300
	}
	bucket := max((end+120_000-start)/int64(points), 1000)
	out := []HRPoint{}
	for i := 0; i < len(samples); {
		b := (samples[i].MS - start) / bucket
		p := HRPoint{TMS: start + b*bucket, MinBpm: samples[i].BPM, MaxBpm: samples[i].BPM}
		var sum float64
		n := 0
		for ; i < len(samples) && (samples[i].MS-start)/bucket == b; i++ {
			v := samples[i].BPM
			sum += v
			n++
			p.MinBpm, p.MaxBpm = min(p.MinBpm, v), max(p.MaxBpm, v)
		}
		p.AvgBpm = mathx.Round(sum/float64(n), 1)
		out = append(out, p)
	}
	return out, nil
}

// SetAnnotation stores RPE (1–10, nil to clear) and notes, keyed so they survive re-imports.
func (s *Service) SetAnnotation(ctx context.Context, id int64, rpe *int, notes string) error {
	var typ string
	var start, end int64
	err := s.db.W.QueryRowContext(ctx, "SELECT activity_type, start_ms, end_ms FROM workouts WHERE id = ?", id).Scan(&typ, &start, &end)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var rpeV any
	if rpe != nil {
		rpeV = *rpe
	}
	var notesV any
	if strings.TrimSpace(notes) != "" {
		notesV = notes
	}
	_, err = s.db.W.ExecContext(ctx, `INSERT INTO workout_annotations (workout_key, rpe, notes, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (workout_key) DO UPDATE SET rpe = excluded.rpe, notes = excluded.notes, updated_at = excluded.updated_at`,
		derive.WorkoutKey(typ, start, end), rpeV, notesV, s.clock.Now().UnixMilli())
	return err
}

// WorkoutType is a workout activity type with how many workouts have it.
type WorkoutType struct {
	ActivityType string `json:"activity_type" example:"HKWorkoutActivityTypeFunctionalStrengthTraining"`
	Name         string `json:"name" example:"Functional Strength Training"`
	Count        int    `json:"count"`
}

// WorkoutTypes lists activity types with counts, most frequent first.
func (s *Service) WorkoutTypes(ctx context.Context) ([]WorkoutType, error) {
	rows, err := s.db.RO.QueryContext(ctx, "SELECT activity_type, count(*) FROM workouts GROUP BY 1 ORDER BY 2 DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkoutType{}
	for rows.Next() {
		var t WorkoutType
		if err := rows.Scan(&t.ActivityType, &t.Count); err != nil {
			return nil, err
		}
		t.Name = ingest.WorkoutName(t.ActivityType)
		out = append(out, t)
	}
	return out, rows.Err()
}
