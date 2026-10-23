package query

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"ritm/internal/days"
	"ritm/internal/derive"
	"ritm/internal/ingest"
	"ritm/internal/mathx"
	"ritm/internal/store"
)

type WorkoutSummary struct {
	ID            int64          `json:"id" doc:"Row id; restarts after a data reset (use key to refer to a workout over time)"`
	Key           string         `json:"key" example:"HKWorkoutActivityTypeRunning|1760000000000|1760003600000" doc:"Stable identity: activity_type|start_ms|end_ms. It survives re-imports and data resets; RPE and notes are stored under it"`
	ActivityType  string         `json:"activity_type"`
	Category      string         `json:"category" enum:"walk,run,hike,cycle,swim,row,paddle,sail,ski,strength,cardio,other" doc:"Normalised kind of activity"`
	DistanceBased bool           `json:"distance_based" doc:"The activity covers a distance (it may have a GPS route)"`
	Route         *RouteLocation `json:"route" doc:"Where the GPS route starts and ends and the area it covers; null without a route"`
	Name          string         `json:"name"`
	Source        string         `json:"source"`
	Day           string         `json:"day"`
	StartMS       int64          `json:"start_ms"`
	EndMS         int64          `json:"end_ms"`
	TZOffsetMin   int            `json:"tz_offset_min"`
	DurationS     *float64       `json:"duration_s"`
	MovingS       *float64       `json:"moving_s"`
	DistanceM     *float64       `json:"distance_m"`
	EnergyKcal    *float64       `json:"energy_kcal"`
	Indoor        *int64         `json:"indoor"`
	AvgHRBpm      *float64       `json:"avg_hr_bpm"`
	MaxHRBpm      *float64       `json:"max_hr_bpm"`
	PctZ3Plus     *float64       `json:"pct_z3_plus"`
	KcalPerMin    *float64       `json:"kcal_per_min"`
	TrimpEdwards  *float64       `json:"trimp_edwards"`
	TrimpBanist   *float64       `json:"trimp_banister"`
	SRPELoad      *float64       `json:"srpe_load"`
	Load          *float64       `json:"load"` // in the selected load model
	HRR60Bpm      *float64       `json:"hrr60_bpm"`
	PaceSPerKm    *float64       `json:"pace_s_per_km"`
	RPE           *int64         `json:"rpe"`
	Notes         *string        `json:"notes"`
	HasRoute      bool           `json:"has_route"`
}

type LatLon struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// RouteLocation places a workout's GPS route without its points.
type RouteLocation struct {
	Start  LatLon      `json:"start"`
	End    LatLon      `json:"end"`
	Bounds RouteBounds `json:"bounds"`
	Points int         `json:"points" doc:"Number of recorded track points"`
}

// WorkoutFilter selects workouts. Without StartMS/EndMS they are picked by local day (Range).
type WorkoutFilter struct {
	Range          days.Range
	StartMS, EndMS int64 // exact window: workouts overlapping [StartMS, EndMS)
	ActivityType   string
	Category       string
	Near           *Near        // route starts within RadiusM of a point
	BBox           *RouteBounds // route bounds intersect this box (MinLon > MaxLon crosses the antimeridian)
	RouteOnly      bool         // only workouts with a GPS route
	After          *Cursor      // continue after this workout, in newest-first order
}

type Near struct {
	Lat, Lon, RadiusM float64
}

// maxWorkoutMS bounds how long a workout is assumed to last, so an exact window can bound start_ms
// from below and use the workouts_start index.
const maxWorkoutMS = 7 * days.MSPerDay

const workoutCols = `id, workout_key, activity_type, source_name, local_day, start_ms, end_ms, tz_offset_min, duration_s, moving_s,
	distance_m, energy_kcal, indoor, avg_hr_bpm, max_hr_bpm, pct_z3_plus, kcal_per_min, trimp_edwards, trimp_banister, srpe_load, load,
	hrr60_bpm, pace_s_per_km, rpe, notes, has_route, route_points, start_lat, start_lon, end_lat, end_lon, min_lat, min_lon, max_lat, max_lon`

func scanWorkout(sc interface{ Scan(...any) error }) (WorkoutSummary, error) {
	var w WorkoutSummary
	var f [15]sql.NullFloat64
	var indoor, rpe, points sql.NullInt64
	var notes sql.NullString
	var loc [8]sql.NullFloat64
	err := sc.Scan(&w.ID, &w.Key, &w.ActivityType, &w.Source, &w.Day, &w.StartMS, &w.EndMS, &w.TZOffsetMin, &f[0], &f[1], &f[2], &f[3], &indoor,
		&f[4], &f[5], &f[6], &f[7], &f[8], &f[9], &f[10], &f[11], &f[12], &f[13], &rpe, &notes, &w.HasRoute,
		&points, &loc[0], &loc[1], &loc[2], &loc[3], &loc[4], &loc[5], &loc[6], &loc[7])
	if err != nil {
		return w, err
	}
	if points.Valid {
		w.Route = &RouteLocation{Start: LatLon{loc[0].Float64, loc[1].Float64}, End: LatLon{loc[2].Float64, loc[3].Float64},
			Bounds: RouteBounds{MinLat: loc[4].Float64, MinLon: loc[5].Float64, MaxLat: loc[6].Float64, MaxLon: loc[7].Float64},
			Points: int(points.Int64)}
	}
	w.Category, w.DistanceBased = ingest.WorkoutCategory(w.ActivityType)
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

// Workouts lists workouts, newest first (by start, then id), capped at limit; truncated reports
// whether more exist, which CursorAfter of the last item continues.
func (s *Service) Workouts(ctx context.Context, f WorkoutFilter, limit int) ([]WorkoutSummary, bool, error) {
	var where []string
	var args []any
	cond := func(c string, a ...any) {
		where = append(where, c)
		args = append(args, a...)
	}
	if f.EndMS > 0 {
		cond("start_ms >= ? AND start_ms < ? AND end_ms > ?", f.StartMS-maxWorkoutMS, f.EndMS, f.StartMS)
	} else {
		// local_day is computed, so start_ms bounds (a day wider, for UTC offsets) let SQLite use
		// the workouts_start index.
		from, to := s.dayBounds(f.Range)
		cond("start_ms >= ? AND start_ms < ? AND local_day BETWEEN ? AND ?", from-days.MSPerDay, to+days.MSPerDay,
			f.Range.From.String(), f.Range.To.String())
	}
	if f.ActivityType != "" {
		cond("activity_type = ?", f.ActivityType)
	}
	if f.Category != "" {
		types := ingest.CategoryTypes(f.Category)
		in := "activity_type IN (" + store.Placeholders(len(types)) + ")"
		if f.Category == ingest.CategoryOther {
			in = "activity_type NOT IN (" + store.Placeholders(len(types)) + ")"
		}
		cond(in, anys(types)...)
	}
	if f.RouteOnly || f.Near != nil || f.BBox != nil {
		cond("route_points IS NOT NULL")
	}
	if b := f.BBox; b != nil {
		cond("max_lat >= ? AND min_lat <= ?", b.MinLat, b.MaxLat)
		if b.MinLon <= b.MaxLon {
			cond("max_lon >= ? AND min_lon <= ?", b.MinLon, b.MaxLon)
		} else {
			cond("(max_lon >= ? OR min_lon <= ?)", b.MinLon, b.MaxLon)
		}
	}
	if n := f.Near; n != nil {
		box := n.box()
		cond("start_lat BETWEEN ? AND ?", box.MinLat, box.MaxLat)
		switch {
		case box.MinLon <= -180 && box.MaxLon >= 180: // no longitude bound
		case box.MinLon < -180:
			cond("(start_lon >= ? OR start_lon <= ?)", box.MinLon+360, box.MaxLon)
		case box.MaxLon > 180:
			cond("(start_lon >= ? OR start_lon <= ?)", box.MinLon, box.MaxLon-360)
		default:
			cond("start_lon BETWEEN ? AND ?", box.MinLon, box.MaxLon)
		}
	}
	if c := f.After; c != nil {
		cond("(start_ms < ? OR (start_ms = ? AND id < ?))", c.StartMS, c.StartMS, c.ID)
	}
	q := "SELECT " + workoutCols + " FROM v_workouts WHERE " + strings.Join(where, " AND ") + " ORDER BY start_ms DESC, id DESC"
	if f.Near == nil { // the radius is checked below, so rows past the limit may still be needed
		q += " LIMIT ?"
		args = append(args, limit+1)
	}
	rows, err := s.db.RO.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []WorkoutSummary{}
	for rows.Next() && len(out) <= limit {
		w, err := scanWorkout(rows)
		if err != nil {
			return nil, false, err
		}
		if f.Near != nil && distanceM(f.Near.Lat, f.Near.Lon, w.Route.Start.Lat, w.Route.Start.Lon) > f.Near.RadiusM {
			continue
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(out) > limit
	if truncated {
		out = out[:limit]
	}
	return out, truncated, nil
}

// box is a latitude/longitude box around the circle, slightly larger; longitudes may run past
// ±180, or cover everything near the poles.
func (n Near) box() RouteBounds {
	const mPerDeg = 111_000.0 // a little under the true 111.2 km, so the box is never too small
	dLat := n.RadiusM / mPerDeg
	b := RouteBounds{MinLat: n.Lat - dLat, MaxLat: n.Lat + dLat, MinLon: -180, MaxLon: 180}
	if c := math.Cos(min(math.Abs(n.Lat)+dLat, 90) * math.Pi / 180); c > 0.01 {
		if dLon := dLat / c; dLon < 180 {
			b.MinLon, b.MaxLon = n.Lon-dLon, n.Lon+dLon
		}
	}
	return b
}

// Cursor marks a position in the newest-first workout order.
type Cursor struct {
	StartMS, ID int64
}

// CursorAfter returns the cursor that continues a listing after w.
func CursorAfter(w WorkoutSummary) Cursor { return Cursor{w.StartMS, w.ID} }

// String encodes the cursor as an opaque token.
func (c Cursor) String() string {
	return base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "%d.%d", c.StartMS, c.ID))
}

// ParseCursor decodes a token from Cursor.String.
func ParseCursor(s string) (Cursor, error) {
	var c Cursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err == nil {
		_, err = fmt.Sscanf(string(b), "%d.%d", &c.StartMS, &c.ID)
	}
	if err != nil {
		return c, errors.New("invalid cursor")
	}
	return c, nil
}

func anys(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
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
	return store.InTx(ctx, s.db.W, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO workout_annotations (workout_key, rpe, notes, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (workout_key) DO UPDATE SET rpe = excluded.rpe, notes = excluded.notes, updated_at = excluded.updated_at`,
			derive.WorkoutKey(typ, start, end), rpeV, notesV, s.clock.Now().UnixMilli())
		if err != nil {
			return err
		}
		return store.BumpDataVersion(ctx, tx)
	})
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

// Tracks returns the routes of the given workouts downsampled to about maxPoints each, keyed by
// workout id; workouts without a route are left out.
func (s *Service) Tracks(ctx context.Context, ids []int64, maxPoints int) (map[int64]*Route, error) {
	out := map[int64]*Route{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.RO.QueryContext(ctx, "SELECT workout_id, points FROM workout_routes WHERE workout_id IN ("+
		store.Placeholders(len(ids))+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		if pts, err := ingest.DecodeRoute(blob); err == nil && len(pts) > 0 {
			out[id] = buildRoute(pts, maxPoints)
		}
	}
	return out, rows.Err()
}

// WorkoutSpan returns when the first workout started and the last one ended (nil without workouts).
func (s *Service) WorkoutSpan(ctx context.Context) (first, last *time.Time, err error) {
	var a, b sql.NullInt64
	err = s.db.RO.QueryRowContext(ctx, "SELECT min(start_ms), max(end_ms) FROM workouts").Scan(&a, &b)
	return store.MSTime(a), store.MSTime(b), err
}

// DataVersion goes up with every change to the user's data (store.BumpDataVersion).
func (s *Service) DataVersion(ctx context.Context) (int64, error) {
	var v int64
	err := s.db.RO.QueryRowContext(ctx, "SELECT data_version FROM data_freshness WHERE id = 1").Scan(&v)
	return v, err
}
