// Package derive computes the derived daily tables, workout metrics and training load (§7).
package derive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"ritm/internal/days"
	"ritm/internal/ingest"
	"ritm/internal/mathx"
	"ritm/internal/store"
)

type Goals struct {
	StepsPerDay     float64 `json:"steps_per_day"`
	SessionsPerWeek int     `json:"sessions_per_week"`
	StandHours      float64 `json:"stand_hours"`
	ExerciseMin     float64 `json:"exercise_min"`
	// UseRingGoals uses the Watch's ring goals (from ActivitySummary) for stand/exercise.
	UseRingGoals bool `json:"use_ring_goals"`
}

// Settings are the user-editable settings (§7.7). Nil pointers mean "derive automatically".
type Settings struct {
	MaxHR           *float64   `json:"max_hr"`
	RestingHR       *float64   `json:"resting_hr"`
	ZoneModel       string     `json:"zone_model" enum:"karvonen,hrmax"`
	ZoneBounds      [5]float64 `json:"zone_bounds"` // lower bound of zones 1–5 as fractions
	LoadModel       string     `json:"load_model" enum:"edwards,banister,srpe"`
	Goals           Goals      `json:"goals"`
	MainWorkoutType string     `json:"main_workout_type"` // "" = most frequent type
	SourcePriority  []string   `json:"source_priority"`   // empty = Watch → iPhone → other
	WakingStartHour int        `json:"waking_start_hour"`
	WakingEndHour   int        `json:"waking_end_hour"`
	StaleAfterHours *float64   `json:"stale_after_hours"` // nil = RITM_STALE_AFTER
	ExtraTypes      []string   `json:"extra_types"`
	AllowSQL        bool       `json:"allow_sql"` // let MCP assistants run read-only SQL (query_sql)
	// Timezone is the IANA zone of the user's local days; the web app fills it in from the browser
	// at sign-in.
	Timezone string `json:"timezone" doc:"IANA time zone of local days, such as Europe/Berlin; empty = the server's RITM_TZ"`
}

// AffectsDerived reports whether changing from s to next requires recomputing derived tables
// and insights. Freshness, ingest and assistant settings do not.
func (s Settings) AffectsDerived(next Settings) bool {
	strip := func(v Settings) Settings {
		v.StaleAfterHours, v.ExtraTypes, v.AllowSQL = nil, nil, false
		return v
	}
	return !reflect.DeepEqual(strip(s), strip(next))
}

func DefaultSettings() Settings {
	return Settings{
		ZoneModel:       "karvonen",
		ZoneBounds:      [5]float64{0.5, 0.6, 0.7, 0.8, 0.9},
		LoadModel:       "edwards",
		Goals:           Goals{StepsPerDay: 8000, SessionsPerWeek: 3, StandHours: 12, ExerciseMin: 30},
		WakingStartHour: 8,
		WakingEndHour:   23,
		SourcePriority:  []string{},
		ExtraTypes:      []string{},
	}
}

func (s *Settings) Validate() error {
	var errs []error
	if s.ZoneModel != "karvonen" && s.ZoneModel != "hrmax" {
		errs = append(errs, errors.New("zone_model must be karvonen or hrmax"))
	}
	if s.LoadModel != "edwards" && s.LoadModel != "banister" && s.LoadModel != "srpe" {
		errs = append(errs, errors.New("load_model must be edwards, banister or srpe"))
	}
	for i, b := range s.ZoneBounds {
		if b <= 0 || b >= 1 || (i > 0 && b <= s.ZoneBounds[i-1]) {
			errs = append(errs, errors.New("zone_bounds must be increasing fractions between 0 and 1"))
			break
		}
	}
	if s.MaxHR != nil && (*s.MaxHR < 100 || *s.MaxHR > 230) {
		errs = append(errs, errors.New("max_hr must be between 100 and 230"))
	}
	if s.RestingHR != nil && (*s.RestingHR < 25 || *s.RestingHR > 120) {
		errs = append(errs, errors.New("resting_hr must be between 25 and 120"))
	}
	if s.Goals.StepsPerDay <= 0 || s.Goals.SessionsPerWeek <= 0 || s.Goals.SessionsPerWeek > 14 ||
		s.Goals.StandHours <= 0 || s.Goals.StandHours > 24 || s.Goals.ExerciseMin <= 0 {
		errs = append(errs, errors.New("goals must be positive (sessions per week ≤ 14, stand hours ≤ 24)"))
	}
	if s.WakingStartHour < 0 || s.WakingEndHour > 24 || s.WakingStartHour >= s.WakingEndHour {
		errs = append(errs, errors.New("waking hours must satisfy 0 ≤ start < end ≤ 24"))
	}
	if s.StaleAfterHours != nil && *s.StaleAfterHours <= 0 {
		errs = append(errs, errors.New("stale_after_hours must be positive"))
	}
	if s.Timezone != "" {
		if _, err := LoadTimezone(s.Timezone); err != nil {
			errs = append(errs, errors.New("timezone must be an IANA time zone such as Europe/Berlin"))
		}
	}
	return errors.Join(errs...)
}

// LoadTimezone loads an IANA time zone. Unlike time.LoadLocation it rejects "" and "Local", which
// would silently mean UTC or the server's own zone.
func LoadTimezone(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, fmt.Errorf("unknown time zone %q", name)
	}
	return time.LoadLocation(name)
}

// Location is the zone of the user's local days: Timezone, or fallback (RITM_TZ) when unset.
func (s Settings) Location(fallback *time.Location) *time.Location {
	if loc, err := LoadTimezone(s.Timezone); err == nil {
		return loc
	}
	return fallback
}

func LoadSettings(ctx context.Context, q store.Querier) (Settings, error) {
	s := DefaultSettings()
	if _, err := store.GetSetting(ctx, q, store.AppSettingsKey, &s); err != nil {
		return s, err
	}
	// Lists are never null in the API, also for settings saved before that rule.
	if s.SourcePriority == nil {
		s.SourcePriority = []string{}
	}
	if s.ExtraTypes == nil {
		s.ExtraTypes = []string{}
	}
	return s, nil
}

func SaveSettings(ctx context.Context, db *store.DB, s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	return store.SetSetting(ctx, db.W, store.AppSettingsKey, s)
}

// Resolved holds settings with automatic values filled in from data.
type Resolved struct {
	Settings
	Age             *float64   `json:"age"`
	Sex             string     `json:"sex"`
	MaxHRBpm        float64    `json:"max_hr_bpm"`
	MaxHRSource     string     `json:"max_hr_source" enum:"manual,age,default"`
	RestingHRBpm    float64    `json:"resting_hr_bpm"`
	RestingHRSource string     `json:"resting_hr_source" enum:"manual,30d_median,default"`
	ZoneLowerBpm    [5]float64 `json:"zone_lower_bpm"`
	Sources         []Source   `json:"sources"` // effective priority order
	MainType        string     `json:"main_workout_type_effective"`
	MainTypeName    string     `json:"main_workout_name"`
	// GoalsSource tells where Goals.StandHours and Goals.ExerciseMin come from: "settings", or
	// "rings" when UseRingGoals is on and the Watch's ring goals are known.
	GoalsSource string `json:"goals_source" enum:"settings,rings"`
}

type Source struct {
	Name string `json:"name"`
	Kind string `json:"kind" enum:"watch,phone,other"`
}

// Resolve fills automatic settings from the data (Me, resting HR history, workouts, sources).
func Resolve(ctx context.Context, q store.Querier, s Settings, now time.Time) (*Resolved, error) {
	r := &Resolved{Settings: s}

	var dob string
	if ok, err := store.GetSetting(ctx, q, "me.dob", &dob); err != nil {
		return nil, err
	} else if ok {
		if t, err := time.Parse("2006-01-02", dob); err == nil {
			age := float64(now.Year() - t.Year())
			if now.YearDay() < t.YearDay() {
				age--
			}
			if age > 0 && age < 120 {
				r.Age = &age
			}
		}
	}
	if _, err := store.GetSetting(ctx, q, "me.sex", &r.Sex); err != nil {
		return nil, err
	}

	// Ring goals (§7.7): the stand and exercise goals of the latest day that has them. Resolved
	// carries the effective goals; the stored settings keep the user's own values.
	r.GoalsSource = "settings"
	if s.Goals.UseRingGoals {
		var ex, stand sql.NullFloat64
		err := q.QueryRowContext(ctx, `SELECT exercise_min_goal, stand_hours_goal FROM activity_summaries
			WHERE exercise_min_goal > 0 OR stand_hours_goal > 0 ORDER BY day DESC LIMIT 1`).Scan(&ex, &stand)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if ex.Float64 > 0 {
			r.Goals.ExerciseMin, r.GoalsSource = ex.Float64, "rings"
		}
		if stand.Float64 > 0 {
			r.Goals.StandHours, r.GoalsSource = stand.Float64, "rings"
		}
	}

	switch {
	case s.MaxHR != nil:
		r.MaxHRBpm, r.MaxHRSource = *s.MaxHR, "manual"
	case r.Age != nil:
		r.MaxHRBpm, r.MaxHRSource = math.Round(208-0.7**r.Age), "age"
	default:
		r.MaxHRBpm, r.MaxHRSource = 185, "default"
	}

	if s.RestingHR != nil {
		r.RestingHRBpm, r.RestingHRSource = *s.RestingHR, "manual"
	} else {
		med, err := medianRestingHR(ctx, q, now)
		if err != nil {
			return nil, err
		}
		if med > 0 {
			r.RestingHRBpm, r.RestingHRSource = med, "30d_median"
		} else {
			r.RestingHRBpm, r.RestingHRSource = 60, "default"
		}
	}
	for i, b := range s.ZoneBounds {
		if s.ZoneModel == "hrmax" {
			r.ZoneLowerBpm[i] = math.Round(b * r.MaxHRBpm)
		} else {
			r.ZoneLowerBpm[i] = math.Round(r.RestingHRBpm + b*(r.MaxHRBpm-r.RestingHRBpm))
		}
	}

	srcs, err := sourcePriority(ctx, q, s.SourcePriority)
	if err != nil {
		return nil, err
	}
	r.Sources = srcs

	r.MainType = s.MainWorkoutType
	if r.MainType == "" {
		// Most frequent type over the last 90 days of workouts (ties: more total time). Recent
		// habits matter more than history, e.g. years of auto-detected walks vs current gym sessions.
		err := q.QueryRowContext(ctx, `SELECT activity_type FROM workouts
			WHERE start_ms > (SELECT max(start_ms) FROM workouts) - 90 * 86400000
			GROUP BY activity_type ORDER BY count(*) DESC, sum(duration_s) DESC, activity_type LIMIT 1`).Scan(&r.MainType)
		if errors.Is(err, sql.ErrNoRows) {
			r.MainType = ingest.WorkoutFunctionalStrength
		} else if err != nil {
			return nil, err
		}
	}
	r.MainTypeName = ingest.WorkoutName(r.MainType)
	return r, nil
}

// medianRestingHR is the median of resting HR samples in the 30 days up to the latest sample.
func medianRestingHR(ctx context.Context, q store.Querier, now time.Time) (float64, error) {
	var latest sql.NullInt64
	if err := q.QueryRowContext(ctx, "SELECT max(start_ms) FROM records WHERE type = ?", ingest.TypeRestingHR).Scan(&latest); err != nil {
		return 0, err
	}
	if !latest.Valid {
		return 0, nil
	}
	end := min(latest.Int64, now.UnixMilli())
	rows, err := q.QueryContext(ctx, "SELECT value FROM records WHERE type = ? AND start_ms > ? AND start_ms <= ?",
		ingest.TypeRestingHR, end-30*24*3600*1000, latest.Int64)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var vals []float64
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			return 0, err
		}
		vals = append(vals, v)
	}
	return math.Round(mathx.Median(vals)), rows.Err()
}

// sourcePriority returns all known sources in priority order: the configured list first, then
// the remaining sources ordered Watch → iPhone → other, most recently seen first (SRC-1).
func sourcePriority(ctx context.Context, q store.Querier, configured []string) ([]Source, error) {
	rows, err := q.QueryContext(ctx, `SELECT name, kind FROM sources
		ORDER BY CASE kind WHEN 'watch' THEN 0 WHEN 'phone' THEN 1 ELSE 2 END, last_seen_ms DESC, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var auto []Source
	kinds := map[string]string{}
	for rows.Next() {
		var s Source
		if err := rows.Scan(&s.Name, &s.Kind); err != nil {
			return nil, err
		}
		auto = append(auto, s)
		kinds[s.Name] = s.Kind
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Source, 0, len(auto))
	seen := map[string]bool{}
	for _, name := range configured {
		if k, ok := kinds[name]; ok && !seen[name] {
			out = append(out, Source{name, k})
			seen[name] = true
		}
	}
	for _, s := range auto {
		if !seen[s.Name] {
			out = append(out, s)
		}
	}
	return out, nil
}

// rank maps a source name to its priority (lower is better); unknown sources rank last.
func (r *Resolved) rank() func(string) int {
	m := make(map[string]int, len(r.Sources))
	for i, s := range r.Sources {
		m[s.Name] = i
	}
	return func(name string) int {
		if i, ok := m[name]; ok {
			return i
		}
		return len(m)
	}
}

func (r *Resolved) isWatch(name string) bool {
	for _, s := range r.Sources {
		if s.Name == name {
			return s.Kind == "watch"
		}
	}
	return false
}

// dataSpan returns the first and last local day with any data.
func dataSpan(ctx context.Context, q store.Querier) (first, last days.Day, ok bool, err error) {
	var lo, hi sql.NullString
	err = q.QueryRowContext(ctx, `SELECT min(d), max(d) FROM (
		SELECT min(local_day) AS d FROM hourly_activity UNION ALL SELECT max(local_day) FROM hourly_activity
		UNION ALL SELECT strftime('%Y-%m-%d', min(start_ms) / 1000, 'unixepoch') FROM records
		UNION ALL SELECT strftime('%Y-%m-%d', max(end_ms) / 1000, 'unixepoch') FROM records
		UNION ALL SELECT strftime('%Y-%m-%d', min(start_ms) / 1000, 'unixepoch') FROM workouts
		UNION ALL SELECT min(day) FROM activity_summaries UNION ALL SELECT max(day) FROM activity_summaries)`).Scan(&lo, &hi)
	if err != nil || !lo.Valid {
		return 0, 0, false, err
	}
	f, err1 := days.Parse(lo.String)
	l, err2 := days.Parse(hi.String)
	if err1 != nil || err2 != nil {
		return 0, 0, false, fmt.Errorf("data span: %v %v", err1, err2)
	}
	return f, l, true, nil
}
