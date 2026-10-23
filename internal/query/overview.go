package query

import (
	"context"
	"database/sql"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"ritm/internal/days"
	"ritm/internal/derive"
	"ritm/internal/ingest"
	"ritm/internal/insights"
	"ritm/internal/mathx"
	"ritm/internal/store"
)

// Stat is a period average with its baseline and delta.
type Stat struct {
	Value    *float64 `json:"value"`
	Baseline *float64 `json:"baseline"`
	DeltaPct *float64 `json:"delta_pct"`
	Days     int      `json:"days"`
}

type Overview struct {
	Period         days.Range         `json:"-"`
	From           string             `json:"from"`
	To             string             `json:"to"`
	BaselineFrom   string             `json:"baseline_from"`
	BaselineTo     string             `json:"baseline_to"`
	StepsPerDay    Stat               `json:"steps_per_day"`
	DistanceMDay   Stat               `json:"distance_m_per_day"`
	ActiveKcalDay  Stat               `json:"active_kcal_per_day"`
	StandHoursDay  Stat               `json:"stand_hours_per_day"`
	ExerciseMinDay Stat               `json:"exercise_min_per_day"`
	RestingHRBpm   Stat               `json:"resting_hr_bpm"`
	HRVMs          Stat               `json:"hrv_sdnn_ms"`
	AsleepMin      Stat               `json:"asleep_min_per_night"`
	Sessions       int                `json:"sessions"`
	SessionsTarget float64            `json:"sessions_target"`
	Goals          map[string]float64 `json:"goals"`
	Insights       []insights.Insight `json:"insights"`
	Scores         *insights.Scores   `json:"scores"`
	Freshness      Freshness          `json:"freshness"`
	HasSleep       bool               `json:"has_sleep_data"`
}

type Freshness struct {
	LastDataAt    *time.Time `json:"last_data_at"`
	LastImportAt  *time.Time `json:"last_import_at"`
	LastCheckedAt *time.Time `json:"last_checked_at"` // last poll of /api/v1/monitor/freshness
	AgeDays       *float64   `json:"age_days"`
	Stale         bool       `json:"stale"`
	StaleAfterH   float64    `json:"stale_after_hours"`
}

type metricDef struct {
	col string
	dst func(*Overview) *Stat
}

var overviewMetrics = []metricDef{
	{"CASE WHEN a.has_watch_data = 1 THEN a.steps END", func(o *Overview) *Stat { return &o.StepsPerDay }},
	{"CASE WHEN a.has_watch_data = 1 THEN a.distance_m END", func(o *Overview) *Stat { return &o.DistanceMDay }},
	{"CASE WHEN a.has_watch_data = 1 THEN a.active_kcal END", func(o *Overview) *Stat { return &o.ActiveKcalDay }},
	{"a.stand_hours", func(o *Overview) *Stat { return &o.StandHoursDay }},
	{"CASE WHEN a.has_watch_data = 1 THEN a.exercise_min END", func(o *Overview) *Stat { return &o.ExerciseMinDay }},
	{"h.resting_hr", func(o *Overview) *Stat { return &o.RestingHRBpm }},
	{"h.hrv_sdnn", func(o *Overview) *Stat { return &o.HRVMs }},
	{"s.asleep_min", func(o *Overview) *Stat { return &o.AsleepMin }},
}

func (s *Service) periodMeans(ctx context.Context, r days.Range) ([]float64, []int, error) {
	q := "SELECT "
	for i, m := range overviewMetrics {
		if i > 0 {
			q += ", "
		}
		q += "avg(" + m.col + "), count(" + m.col + ")"
	}
	q += ` FROM (SELECT day FROM daily_activity UNION SELECT day FROM daily_heart UNION SELECT day FROM daily_sleep) d
		LEFT JOIN daily_activity a ON a.day = d.day LEFT JOIN daily_heart h ON h.day = d.day LEFT JOIN daily_sleep s ON s.day = d.day
		WHERE d.day BETWEEN ? AND ?`
	vals := make([]sql.NullFloat64, len(overviewMetrics))
	counts := make([]int, len(overviewMetrics))
	dst := make([]any, 0, 2*len(vals))
	for i := range vals {
		dst = append(dst, &vals[i], &counts[i])
	}
	if err := s.db.RO.QueryRowContext(ctx, q, r.From.String(), r.To.String()).Scan(dst...); err != nil {
		return nil, nil, err
	}
	out := make([]float64, len(vals))
	for i, v := range vals {
		out[i] = math.NaN()
		if v.Valid {
			out[i] = v.Float64
		}
	}
	return out, counts, nil
}

// Overview summarises a period with deltas vs a baseline: the 28 days before it for periods up to
// 28 days, otherwise the preceding period of equal length.
func (s *Service) Overview(ctx context.Context, r days.Range, staleAfter time.Duration) (*Overview, error) {
	res, err := s.Resolved(ctx)
	if err != nil {
		return nil, err
	}
	base := days.Range{From: r.From.Add(-28), To: r.From.Add(-1)}
	if r.Len() > 28 {
		base = days.Range{From: r.From.Add(-r.Len()), To: r.From.Add(-1)}
	}
	o := &Overview{Period: r, From: r.From.String(), To: r.To.String(), BaselineFrom: base.From.String(), BaselineTo: base.To.String()}
	cur, curN, err := s.periodMeans(ctx, r)
	if err != nil {
		return nil, err
	}
	bv, _, err := s.periodMeans(ctx, base)
	if err != nil {
		return nil, err
	}
	for i, m := range overviewMetrics {
		st := m.dst(o)
		st.Value, st.Baseline, st.Days = mathx.RoundPtr(cur[i], 1), mathx.RoundPtr(bv[i], 1), curN[i]
		if !math.IsNaN(cur[i]) && !math.IsNaN(bv[i]) && bv[i] != 0 {
			st.DeltaPct = mathx.RoundPtr((cur[i]-bv[i])/math.Abs(bv[i])*100, 1)
		}
	}
	if o.HasSleep = o.AsleepMin.Days > 0; !o.HasSleep {
		if o.HasSleep, err = s.HasSleep(ctx); err != nil {
			return nil, err
		}
	}

	ws, _, err := s.Workouts(ctx, WorkoutFilter{Range: r, ActivityType: res.MainType}, 1000)
	if err != nil {
		return nil, err
	}
	o.Sessions = len(ws)
	o.SessionsTarget = math.Round(float64(res.Goals.SessionsPerWeek)*float64(r.Len())/7*10) / 10
	o.Goals = map[string]float64{"steps_per_day": res.Goals.StepsPerDay, "sessions_per_week": float64(res.Goals.SessionsPerWeek),
		"stand_hours": res.Goals.StandHours, "exercise_min": res.Goals.ExerciseMin}

	if o.Insights, err = s.Insights(ctx, insights.Filter{LatestOnly: true, Limit: 5}); err != nil {
		return nil, err
	}
	// Scores look at most 97 days back (7-day window + 90-day baseline) from the period's end.
	end := min(r.To, s.Today())
	data, err := insights.Load(ctx, s.db.RO, res.MainType, end.Add(-100), end)
	if err != nil {
		return nil, err
	}
	if data.Anchor >= 0 {
		sc := insights.ComputeScores(data, res, data.Anchor)
		o.Scores = &sc
	}
	if o.Freshness, err = s.Freshness(ctx, staleAfter); err != nil {
		return nil, err
	}
	return o, nil
}

// HasSleep reports whether any night of sleep is stored (the Sleep page is hidden otherwise).
func (s *Service) HasSleep(ctx context.Context) (bool, error) {
	var ok bool
	err := s.db.RO.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM daily_sleep)").Scan(&ok)
	return ok, err
}

// StaleAfter returns the effective no-new-data threshold (Settings override env). It reads only
// the stored settings, so the monitoring endpoint stays a couple of single-row reads.
func (s *Service) StaleAfter(ctx context.Context, envDefault time.Duration) (time.Duration, error) {
	st, err := derive.LoadSettings(ctx, s.db.RO)
	if err != nil {
		return 0, err
	}
	if st.StaleAfterHours != nil {
		return time.Duration(*st.StaleAfterHours * float64(time.Hour)), nil
	}
	return envDefault, nil
}

func (s *Service) Freshness(ctx context.Context, envDefault time.Duration) (Freshness, error) {
	var last, imp, checked sql.NullInt64
	if err := s.db.RO.QueryRowContext(ctx, "SELECT last_data_at, last_import_at, last_checked_at FROM data_freshness WHERE id = 1").
		Scan(&last, &imp, &checked); err != nil && err != sql.ErrNoRows {
		return Freshness{}, err
	}
	stale, err := s.StaleAfter(ctx, envDefault)
	if err != nil {
		return Freshness{}, err
	}
	f := Freshness{LastDataAt: store.MSTime(last), LastImportAt: store.MSTime(imp), LastCheckedAt: store.MSTime(checked), StaleAfterH: stale.Hours()}
	if f.LastDataAt != nil {
		age := s.clock.Now().Sub(*f.LastDataAt).Hours() / 24
		f.AgeDays = mathx.RoundPtr(age, 1)
		f.Stale = s.clock.Now().Sub(*f.LastDataAt) >= stale
	}
	return f, nil
}

// MarkChecked records a monitoring poll in data_freshness.last_checked_at, at most once a minute
// . The write runs in the background so a long import transaction never delays the probe;
// Wait blocks until pending writes finish.
func (s *Service) MarkChecked(now time.Time) {
	ms := now.UnixMilli()
	prev := s.lastChecked.Load()
	if ms-prev < time.Minute.Milliseconds() || !s.lastChecked.CompareAndSwap(prev, ms) {
		return
	}
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = s.db.W.ExecContext(ctx, "UPDATE data_freshness SET last_checked_at = ? WHERE id = 1", ms)
	}()
}

// Wait blocks until background writes started by MarkChecked have finished.
func (s *Service) Wait() { s.bg.Wait() }

type TypeCoverage struct {
	Type  string `json:"type"`
	Name  string `json:"name"` // human-readable type
	Kind  string `json:"kind" enum:"record,hourly,workout,summary"`
	From  string `json:"from"`
	To    string `json:"to"`
	Count int64  `json:"count"`
}

// SourceInfo is a device or app seen in exports.
type SourceInfo struct {
	Name        string     `json:"name"`
	Kind        string     `json:"kind" enum:"watch,phone,other"`
	FirstSeenAt *time.Time `json:"first_seen_at"`
	LastSeenAt  *time.Time `json:"last_seen_at"`
}

type Coverage struct {
	Types     []TypeCoverage   `json:"types"`
	Sources   []SourceInfo     `json:"sources"`
	DBBytes   int64            `json:"db_bytes"`
	WALBytes  int64            `json:"wal_bytes"`
	RowCounts map[string]int64 `json:"row_counts"`
}

func (s *Service) Coverage(ctx context.Context) (*Coverage, error) {
	c := &Coverage{Sources: []SourceInfo{}, RowCounts: map[string]int64{}}
	types, counts, err := s.dataCoverage(ctx)
	if err != nil {
		return nil, err
	}
	c.Types = types
	for t, n := range counts {
		c.RowCounts[t] = n
	}
	rows, err := s.db.RO.QueryContext(ctx, "SELECT name, kind, first_seen_ms, last_seen_ms FROM sources ORDER BY last_seen_ms DESC")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var src SourceInfo
		var first, last sql.NullInt64
		if err := rows.Scan(&src.Name, &src.Kind, &first, &last); err != nil {
			rows.Close()
			return nil, err
		}
		src.FirstSeenAt, src.LastSeenAt = store.MSTime(first), store.MSTime(last)
		c.Sources = append(c.Sources, src)
	}
	rows.Close()
	for _, t := range []string{"insights", "imports"} {
		var n int64
		if err := s.db.RO.QueryRowContext(ctx, "SELECT count(*) FROM "+t).Scan(&n); err != nil {
			return nil, err
		}
		c.RowCounts[t] = n
	}
	if fi, err := os.Stat(s.db.Path); err == nil {
		c.DBBytes = fi.Size()
	}
	if fi, err := os.Stat(s.db.Path + "-wal"); err == nil {
		c.WALBytes = fi.Size()
	}
	return c, nil
}

// coverageCache holds the per-type ranges and row counts of the imported data. They take a full
// scan of records to compute and change only when an import finishes, so they are cached until
// the imports table changes.
type coverageCache struct {
	mu     sync.Mutex
	key    string
	types  []TypeCoverage
	counts map[string]int64
}

func (s *Service) dataCoverage(ctx context.Context) ([]TypeCoverage, map[string]int64, error) {
	var key string
	if err := s.db.RO.QueryRowContext(ctx, "SELECT count(*) || '/' || ifnull(max(ifnull(finished_at, started_at)), 0) FROM imports").Scan(&key); err != nil {
		return nil, nil, err
	}
	c := &s.coverage
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.types != nil && c.key == key {
		return c.types, c.counts, nil
	}
	types := []TypeCoverage{}
	q := `SELECT type, 'record', strftime('%Y-%m-%d', min(start_ms) / 1000, 'unixepoch'), strftime('%Y-%m-%d', max(start_ms) / 1000, 'unixepoch'), count(*) FROM records GROUP BY type
		UNION ALL SELECT type, 'hourly', min(local_day), max(local_day), count(*) FROM hourly_activity GROUP BY type
		UNION ALL SELECT activity_type, 'workout', strftime('%Y-%m-%d', min(start_ms) / 1000, 'unixepoch'), strftime('%Y-%m-%d', max(start_ms) / 1000, 'unixepoch'), count(*) FROM workouts GROUP BY activity_type
		UNION ALL SELECT 'ActivitySummary', 'summary', min(day), max(day), count(*) FROM activity_summaries HAVING count(*) > 0
		ORDER BY 2, 1`
	rows, err := s.db.RO.QueryContext(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	counts := map[string]int64{"records": 0, "hourly_activity": 0, "workouts": 0, "activity_summaries": 0}
	table := map[string]string{"record": "records", "hourly": "hourly_activity", "workout": "workouts", "summary": "activity_summaries"}
	for rows.Next() {
		var t TypeCoverage
		if err := rows.Scan(&t.Type, &t.Kind, &t.From, &t.To, &t.Count); err != nil {
			return nil, nil, err
		}
		t.Name = typeLabel(t.Type, t.Kind)
		types = append(types, t)
		counts[table[t.Kind]] += t.Count
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	c.key, c.types, c.counts = key, types, counts
	return types, counts, nil
}

// typeLabel turns HealthKit identifiers into readable names, e.g. "Heart Rate" or "Walking".
func typeLabel(t, kind string) string {
	if kind == "workout" {
		return ingest.WorkoutName(t)
	}
	t = strings.TrimPrefix(strings.TrimPrefix(t, "HKQuantityTypeIdentifier"), "HKCategoryTypeIdentifier")
	return ingest.WorkoutName(t)
}
