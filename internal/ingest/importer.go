package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ritm/internal/clock"
	"ritm/internal/store"
)

const batchSize = 5000

// Options control one import run.
type Options struct {
	ImportID    string
	Full        bool // ignore high-water marks
	DeltaWindow time.Duration
	Loc         *time.Location // fallback zone for dates without an offset
	// Progress is called periodically with consumed and total uncompressed bytes.
	Progress func(consumed, total int64)
	Logger   *slog.Logger
	Clock    clock.Clock // nil = system clock
}

func (o *Options) now() time.Time {
	if o.Clock == nil {
		return time.Now()
	}
	return o.Clock.Now()
}

type TypeStats struct {
	Scanned     int64 `json:"scanned"`
	Inserted    int64 `json:"inserted"`
	Duplicate   int64 `json:"duplicate"`
	Skipped     int64 `json:"skipped"`
	BelowCutoff int64 `json:"below_cutoff"`
}

// ImportStats is the import summary stored in imports.stats_json.
type ImportStats struct {
	ExportDateMS     int64                 `json:"export_date_ms,omitempty"`
	XMLBytes         int64                 `json:"xml_bytes"`
	DurationMS       int64                 `json:"duration_ms"`
	Types            map[string]*TypeStats `json:"types"`
	Workouts         TypeStats             `json:"workouts"`
	SummariesChanged int64                 `json:"summaries_changed"`
	DaysReaggregated int                   `json:"days_reaggregated"`
	NewWorkouts      int64                 `json:"new_workouts"`
	RoutesStored     int64                 `json:"routes_stored,omitempty"`
	IgnoredRecords   int                   `json:"ignored_records"`
	OlderExport      bool                  `json:"older_export"`
	Full             bool                  `json:"full_reimport"`
	EarliestDay      string                `json:"earliest_affected_day,omitempty"`
	LastDataMS       int64                 `json:"last_data_ms,omitempty"`
	Warnings         []string              `json:"warnings,omitempty"`
	Errors           []string              `json:"errors,omitempty"`
}

type item struct {
	rec *Record
	wo  *Workout
	sum *Summary
	me  *Me
	exp int64
}

type batcher struct {
	ctx context.Context
	ch  chan []item
	cur []item
}

func (b *batcher) add(it item) {
	b.cur = append(b.cur, it)
	if len(b.cur) >= batchSize {
		b.flush()
	}
}

func (b *batcher) flush() {
	if len(b.cur) == 0 {
		return
	}
	select {
	case b.ch <- b.cur:
	case <-b.ctx.Done():
	}
	b.cur = make([]item, 0, batchSize)
}

func (b *batcher) ExportDate(ms int64) { b.add(item{exp: ms}) }
func (b *batcher) Me(m Me)             { b.add(item{me: &m}) }
func (b *batcher) Record(r *Record)    { b.add(item{rec: r}) }
func (b *batcher) Workout(w *Workout)  { b.add(item{wo: w}) }
func (b *batcher) Summary(s *Summary)  { b.add(item{sum: s}) }

// Import parses the export at path and writes new data into db. It is safe to call repeatedly
// with overlapping exports; only new data is stored (G2).
func Import(ctx context.Context, db *store.DB, path string, opts Options) (*ImportStats, error) {
	started := time.Now()
	if opts.Loc == nil {
		opts.Loc = time.UTC
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	// Imports into one database must not overlap, even across processes (server and `ritm
	// import`), because they share hourly_staging.
	unlock, err := store.LockFile(db.Path+store.ImportLockSuffix, false)
	if errors.Is(err, store.ErrLocked) {
		return nil, ErrBusy
	}
	if err != nil {
		return nil, err
	}
	defer unlock()
	src, err := openSource(path)
	if err != nil {
		return nil, err
	}
	defer src.close()

	// Extra discrete types come from the user's settings (derive.Settings.ExtraTypes).
	var settings struct {
		ExtraTypes []string `json:"extra_types"`
	}
	if _, err := store.GetSetting(ctx, db.W, store.AppSettingsKey, &settings); err != nil {
		return nil, err
	}
	w, err := newWriter(ctx, db, opts, newTypeSet(settings.ExtraTypes))
	if err != nil {
		return nil, err
	}
	defer w.close()

	var consumed atomic.Int64
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	b := &batcher{ctx: ctx, ch: make(chan []item, 2), cur: make([]item, 0, batchSize)}
	parser := NewParser(NewCleanReader(countingReader{r: src.r, n: &consumed}), opts.Loc, w.types, b)

	parseErr := make(chan error, 1)
	go func() {
		err := parser.Run(func() bool { return ctx.Err() != nil })
		b.flush()
		close(b.ch)
		parseErr <- err
	}()

	var progressStop chan struct{}
	var progressDone sync.WaitGroup
	if opts.Progress != nil {
		progressStop = make(chan struct{})
		progressDone.Add(1)
		go func() {
			defer progressDone.Done()
			t := time.NewTicker(250 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					opts.Progress(consumed.Load(), src.total)
				case <-progressStop:
					return
				}
			}
		}()
	}

	var writeErr error
	for batch := range b.ch {
		if writeErr != nil {
			continue // drain
		}
		if writeErr = w.writeBatch(ctx, batch); writeErr != nil {
			cancel()
		}
	}
	pErr := <-parseErr
	if progressStop != nil {
		close(progressStop)
		progressDone.Wait()
	}
	if writeErr != nil {
		return nil, writeErr
	}
	if pErr != nil {
		if errors.Is(pErr, errCanceled) && ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, pErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	st := w.stats
	for typ, n := range parser.Skipped {
		opts.Logger.Warn("skipped malformed elements", "type", typ, "count", n)
		w.typeStats(typ).Skipped += int64(n)
		if typ == "Workout" {
			st.Workouts.Skipped += int64(n)
			delete(st.Types, typ)
		}
	}
	if n := parser.Skipped["ActivitySummary"]; n > 0 {
		delete(st.Types, "ActivitySummary")
		st.Warnings = append(st.Warnings, fmt.Sprintf("%d malformed ActivitySummary elements skipped", n))
	}
	st.IgnoredRecords = parser.Ignored
	st.Errors = parser.Problems
	st.XMLBytes = consumed.Load()
	if src.zip != nil {
		if err := w.storeRoutes(ctx, src); err != nil {
			st.Warnings = append(st.Warnings, "routes: "+err.Error())
		}
	}
	if err := w.finish(ctx, parser.Sources); err != nil {
		return nil, err
	}
	if opts.Progress != nil {
		opts.Progress(src.total, src.total)
	}
	st.DurationMS = time.Since(started).Milliseconds()
	return st, nil
}

// writer owns the import's prepared statements and accumulated state.
type writer struct {
	db    *store.DB
	opts  Options
	types typeSet
	stats *ImportStats

	hwm         map[string]int64 // previous high-water marks
	maxStart    map[string]int64 // newest sample start per type in this import
	cutoffMS    map[string]int64
	cutoffDay   map[string]string
	prevExport  int64
	older       bool
	watchSeen   bool
	lastWatchMS int64
	lastAnyMS   int64
	newIDs      map[int64]bool // workouts inserted by this import

	insRecord, insStaging, insWorkout, insEvent, insActivity *sql.Stmt
	upsSummary, insSummary                                   *sql.Stmt
}

func newWriter(ctx context.Context, db *store.DB, opts Options, types typeSet) (*writer, error) {
	w := &writer{db: db, opts: opts, types: types,
		stats:    &ImportStats{Types: map[string]*TypeStats{}, Full: opts.Full},
		hwm:      map[string]int64{},
		maxStart: map[string]int64{}, cutoffMS: map[string]int64{}, cutoffDay: map[string]string{}, newIDs: map[int64]bool{}}

	rows, err := db.W.QueryContext(ctx, "SELECT type, hwm_ms FROM ingest_hwm")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t string
		var ms int64
		if err := rows.Scan(&t, &ms); err != nil {
			rows.Close()
			return nil, err
		}
		w.hwm[t] = ms
	}
	rows.Close()
	if !opts.Full {
		for t, ms := range w.hwm {
			c := ms - opts.DeltaWindow.Milliseconds()
			w.cutoffMS[t] = c
			// cumulative data is replaced per whole local day
			_, off := time.UnixMilli(c).In(opts.Loc).Zone()
			w.cutoffDay[t] = LocalDay(c, off/60)
		}
	}
	var prev sql.NullInt64
	if err := db.W.QueryRowContext(ctx, "SELECT max(export_date) FROM imports WHERE status = 'succeeded'").Scan(&prev); err != nil {
		return nil, err
	}
	w.prevExport = prev.Int64
	var watch int
	if err := db.W.QueryRowContext(ctx, "SELECT count(*) FROM sources WHERE kind = 'watch'").Scan(&watch); err != nil {
		return nil, err
	}
	w.watchSeen = watch > 0

	if _, err := db.W.ExecContext(ctx, "DELETE FROM hourly_staging"); err != nil {
		return nil, err
	}
	prep := []struct {
		dst **sql.Stmt
		q   string
	}{
		{&w.insRecord, `INSERT INTO records (type, source_name, unit, value, category_value, start_ms, end_ms, tz_offset_min, metadata_json, import_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`},
		{&w.insStaging, `INSERT INTO hourly_staging (type, source_name, hour_start_ms, local_day, local_hour, value)
			VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (type, source_name, hour_start_ms) DO UPDATE SET value = value + excluded.value`},
		{&w.insWorkout, `INSERT INTO workouts (activity_type, source_name, start_ms, end_ms, tz_offset_min, duration_s, distance_m, energy_kcal, indoor, metadata_json, stats_json, import_id, has_route_files)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING RETURNING id`},
		{&w.insEvent, `INSERT OR IGNORE INTO workout_events (workout_id, type, start_ms, end_ms) VALUES (?, ?, ?, ?)`},
		{&w.insActivity, `INSERT OR IGNORE INTO workout_activities (workout_id, activity_type, start_ms, end_ms, stats_json) VALUES (?, ?, ?, ?, ?)`},
		{&w.upsSummary, `INSERT INTO activity_summaries (day, active_kcal, active_kcal_goal, move_min, move_min_goal, exercise_min, exercise_min_goal, stand_hours, stand_hours_goal)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (day) DO UPDATE SET active_kcal = excluded.active_kcal, active_kcal_goal = excluded.active_kcal_goal,
				move_min = excluded.move_min, move_min_goal = excluded.move_min_goal, exercise_min = excluded.exercise_min,
				exercise_min_goal = excluded.exercise_min_goal, stand_hours = excluded.stand_hours, stand_hours_goal = excluded.stand_hours_goal
			WHERE active_kcal IS NOT excluded.active_kcal OR active_kcal_goal IS NOT excluded.active_kcal_goal
				OR move_min IS NOT excluded.move_min OR move_min_goal IS NOT excluded.move_min_goal
				OR exercise_min IS NOT excluded.exercise_min OR exercise_min_goal IS NOT excluded.exercise_min_goal
				OR stand_hours IS NOT excluded.stand_hours OR stand_hours_goal IS NOT excluded.stand_hours_goal`},
		{&w.insSummary, `INSERT INTO activity_summaries (day, active_kcal, active_kcal_goal, move_min, move_min_goal, exercise_min, exercise_min_goal, stand_hours, stand_hours_goal)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (day) DO NOTHING`},
	}
	for _, p := range prep {
		s, err := db.W.PrepareContext(ctx, p.q)
		if err != nil {
			w.close()
			return nil, fmt.Errorf("prepare: %w", err)
		}
		*p.dst = s
	}
	return w, nil
}

func (w *writer) close() {
	for _, s := range []*sql.Stmt{w.insRecord, w.insStaging, w.insWorkout, w.insEvent, w.insActivity, w.upsSummary, w.insSummary} {
		if s != nil {
			s.Close()
		}
	}
}

func (w *writer) typeStats(t string) *TypeStats {
	s, ok := w.stats.Types[t]
	if !ok {
		s = &TypeStats{}
		w.stats.Types[t] = s
	}
	return s
}

func (w *writer) affect(day string) {
	if w.stats.EarliestDay == "" || day < w.stats.EarliestDay {
		w.stats.EarliestDay = day
	}
}

func (w *writer) noteData(kind string, endMS int64) {
	if endMS > w.lastAnyMS {
		w.lastAnyMS = endMS
	}
	if kind == "watch" {
		w.watchSeen = true
		if endMS > w.lastWatchMS {
			w.lastWatchMS = endMS
		}
	}
}

type stageKey struct {
	typ, source string
	hour        int64
}

type stageVal struct {
	day   string
	hour  int
	value float64
}

func (w *writer) writeBatch(ctx context.Context, batch []item) error {
	tx, err := w.db.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	insRecord := tx.StmtContext(ctx, w.insRecord)
	stage := map[stageKey]*stageVal{}

	for _, it := range batch {
		switch {
		case it.rec != nil:
			r := it.rec
			ts := w.typeStats(r.Type)
			ts.Scanned++
			if r.StartMS > w.maxStart[r.Type] {
				w.maxStart[r.Type] = r.StartMS
			}
			if w.types.cumulative[r.Type] {
				cutDay := w.cutoffDay[r.Type]
				if cutDay != "" && LocalDay(r.EndMS, r.Off) < cutDay {
					ts.BelowCutoff++
					continue
				}
				splitHours(r.StartMS, r.EndMS, r.Off, r.Value, func(hs int64, day string, hour int, v float64) {
					if cutDay != "" && day < cutDay {
						return
					}
					k := stageKey{r.Type, r.Source, hs}
					if sv, ok := stage[k]; ok {
						sv.value += v
					} else {
						stage[k] = &stageVal{day, hour, v}
					}
				})
				ts.Inserted++ // aggregated
				w.noteData(r.SourceKind, r.EndMS)
				continue
			}
			if c, ok := w.cutoffMS[r.Type]; ok && r.StartMS < c {
				ts.BelowCutoff++
				continue
			}
			var value, category, meta any
			if r.HasValue {
				value = r.Value
			} else {
				category = r.Category
			}
			if len(r.Meta) > 0 {
				m := make(map[string]string, len(r.Meta))
				for _, kv := range r.Meta {
					m[kv[0]] = kv[1]
				}
				b, _ := json.Marshal(m)
				meta = string(b)
			}
			var unit any
			if r.Unit != "" {
				unit = r.Unit
			}
			res, err := insRecord.ExecContext(ctx, r.Type, r.Source, unit, value, category, r.StartMS, r.EndMS, r.Off, meta, w.opts.ImportID)
			if err != nil {
				return fmt.Errorf("insert record: %w", err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				ts.Inserted++
				w.affect(LocalDay(r.StartMS, r.Off))
			} else {
				ts.Duplicate++
			}
			w.noteData(r.SourceKind, r.EndMS)

		case it.wo != nil:
			if err := w.writeWorkout(ctx, tx, it.wo); err != nil {
				return err
			}

		case it.sum != nil:
			s := it.sum
			stmt := w.upsSummary
			if w.older {
				stmt = w.insSummary
			}
			res, err := tx.StmtContext(ctx, stmt).ExecContext(ctx, s.Day, s.ActiveKcal, s.ActiveKcalGoal, s.MoveMin, s.MoveMinGoal,
				s.ExerciseMin, s.ExerciseMinGoal, s.StandHours, s.StandHoursGoal)
			if err != nil {
				return fmt.Errorf("upsert summary: %w", err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				w.stats.SummariesChanged++
				w.affect(s.Day)
			}

		case it.me != nil:
			if it.me.DOB != "" {
				if err := store.SetSetting(ctx, tx, "me.dob", it.me.DOB); err != nil {
					return err
				}
			}
			if it.me.Sex != "" {
				if err := store.SetSetting(ctx, tx, "me.sex", it.me.Sex); err != nil {
					return err
				}
			}

		case it.exp != 0:
			w.stats.ExportDateMS = it.exp
			if w.prevExport > 0 && it.exp < w.prevExport {
				w.older = true
				w.stats.OlderExport = true
				w.stats.Warnings = append(w.stats.Warnings, fmt.Sprintf(
					"this export (%s) is older than the latest imported one (%s); only missing data is added",
					time.UnixMilli(it.exp).UTC().Format(time.RFC3339), time.UnixMilli(w.prevExport).UTC().Format(time.RFC3339)))
			}
		}
	}

	if len(stage) > 0 {
		ins := tx.StmtContext(ctx, w.insStaging)
		for k, v := range stage {
			if _, err := ins.ExecContext(ctx, k.typ, k.source, k.hour, v.day, v.hour, v.value); err != nil {
				return fmt.Errorf("stage hourly: %w", err)
			}
		}
	}
	return tx.Commit()
}

func (w *writer) writeWorkout(ctx context.Context, tx *sql.Tx, wo *Workout) error {
	w.stats.Workouts.Scanned++
	w.noteData(wo.SourceKind, wo.EndMS)
	meta := map[string]any{}
	for k, v := range wo.Meta {
		meta[k] = v
	}
	if len(wo.RoutePaths) > 0 {
		meta["ritm:route_files"] = wo.RoutePaths
	}
	var metaJSON, statsJSON any
	if len(meta) > 0 {
		b, _ := json.Marshal(meta)
		metaJSON = string(b)
	}
	if len(wo.Stats) > 0 {
		b, _ := json.Marshal(statsMap(wo.Stats))
		statsJSON = string(b)
	}
	var indoor any
	if wo.Indoor != nil {
		indoor = store.BoolInt(*wo.Indoor)
	}
	var id int64
	err := tx.StmtContext(ctx, w.insWorkout).QueryRowContext(ctx, wo.ActivityType, wo.Source, wo.StartMS, wo.EndMS, wo.Off,
		wo.DurationS, wo.DistanceM, wo.EnergyKcal, indoor, metaJSON, statsJSON, w.opts.ImportID, store.BoolInt(len(wo.RoutePaths) > 0)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		w.stats.Workouts.Duplicate++
		return nil
	}
	if err != nil {
		return fmt.Errorf("insert workout: %w", err)
	}
	w.stats.Workouts.Inserted++
	w.stats.NewWorkouts++
	w.newIDs[id] = true
	w.affect(LocalDay(wo.StartMS, wo.Off))

	insEvent := tx.StmtContext(ctx, w.insEvent)
	for _, ev := range wo.Events {
		var end any
		if ev.EndMS > 0 {
			end = ev.EndMS
		}
		if _, err := insEvent.ExecContext(ctx, id, ev.Type, ev.StartMS, end); err != nil {
			return fmt.Errorf("insert workout event: %w", err)
		}
	}
	insAct := tx.StmtContext(ctx, w.insActivity)
	for _, a := range wo.Activities {
		var sj any
		if len(a.Stats) > 0 {
			b, _ := json.Marshal(statsMap(a.Stats))
			sj = string(b)
		}
		if _, err := insAct.ExecContext(ctx, id, a.Type, a.StartMS, a.EndMS, sj); err != nil {
			return fmt.Errorf("insert workout activity: %w", err)
		}
	}
	return nil
}

func statsMap(stats []WorkoutStat) map[string]WorkoutStat {
	m := make(map[string]WorkoutStat, len(stats))
	for _, s := range stats {
		m[s.Type] = s
	}
	return m
}

// finish moves staged hourly buckets into hourly_activity, advances high-water marks,
// records sources and updates data freshness — all in one transaction.
func (w *writer) finish(ctx context.Context, sources map[string]*SourceInfo) error {
	return store.InTx(ctx, w.db.W, func(tx *sql.Tx) error {
		types := make([]string, 0, len(w.types.cumulative))
		for t := range w.types.cumulative {
			types = append(types, t)
		}
		sort.Strings(types)
		days := map[string]bool{}
		for _, t := range types {
			cut := w.cutoffDay[t]
			rows, err := tx.QueryContext(ctx, "SELECT DISTINCT local_day FROM hourly_staging WHERE type = ? AND local_day >= ?", t, cut)
			if err != nil {
				return err
			}
			var staged []string
			for rows.Next() {
				var d string
				if err := rows.Scan(&d); err != nil {
					rows.Close()
					return err
				}
				staged = append(staged, d)
			}
			rows.Close()
			if len(staged) == 0 {
				continue
			}
			if w.older {
				// An older export only fills days that have no data yet (B then A ⇒ no changes).
				res, err := tx.ExecContext(ctx, `INSERT INTO hourly_activity (type, source_name, hour_start_ms, local_day, local_hour, value)
					SELECT s.type, s.source_name, s.hour_start_ms, s.local_day, s.local_hour, s.value FROM hourly_staging s
					WHERE s.type = ? AND s.local_day >= ?
					  AND NOT EXISTS (SELECT 1 FROM hourly_activity h WHERE h.type = s.type AND h.local_day = s.local_day)`, t, cut)
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n > 0 {
					w.affect(staged[0])
					for _, d := range staged {
						days[d] = true
					}
				}
				continue
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM hourly_activity WHERE type = ? AND local_day >= ?", t, cut); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO hourly_activity (type, source_name, hour_start_ms, local_day, local_hour, value)
				SELECT type, source_name, hour_start_ms, local_day, local_hour, value FROM hourly_staging
				WHERE type = ? AND local_day >= ?`, t, cut); err != nil {
				return err
			}
			first := staged[0]
			if cut != "" && cut < first {
				first = cut
			}
			w.affect(first)
			for _, d := range staged {
				days[d] = true
			}
		}
		w.stats.DaysReaggregated = len(days)
		if _, err := tx.ExecContext(ctx, "DELETE FROM hourly_staging"); err != nil {
			return err
		}

		for t, ms := range w.maxStart {
			if ms <= w.hwm[t] {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO ingest_hwm (type, hwm_ms) VALUES (?, ?)
				ON CONFLICT (type) DO UPDATE SET hwm_ms = max(hwm_ms, excluded.hwm_ms)`, t, ms); err != nil {
				return err
			}
		}
		for _, s := range sources {
			if _, err := tx.ExecContext(ctx, `INSERT INTO sources (name, kind, first_seen_ms, last_seen_ms) VALUES (?, ?, ?, ?)
				ON CONFLICT (name) DO UPDATE SET
					kind = CASE WHEN sources.kind = 'other' THEN excluded.kind ELSE sources.kind END,
					first_seen_ms = min(sources.first_seen_ms, excluded.first_seen_ms),
					last_seen_ms = max(sources.last_seen_ms, excluded.last_seen_ms)`,
				s.Name, s.Kind, s.FirstMS, s.LastMS); err != nil {
				return err
			}
		}

		// Newest Watch-sourced sample; any source when no Watch was ever seen.
		last := w.lastWatchMS
		if !w.watchSeen {
			last = w.lastAnyMS
		}
		w.stats.LastDataMS = last
		_, err := tx.ExecContext(ctx, `UPDATE data_freshness SET
			last_data_at = CASE WHEN ? > ifnull(last_data_at, 0) THEN ? ELSE last_data_at END,
			last_import_at = ?, last_import_id = ?, data_version = data_version + 1 WHERE id = 1`,
			last, last, w.opts.now().UnixMilli(), w.opts.ImportID)
		return err
	})
}

// storeRoutes parses the GPX files of workouts that have route files but no stored route yet:
// new workouts, and workouts imported before routes were stored, so re-uploading an
// export fills in the history. It runs after the XML pass. Missing files are reported only for
// workouts new in this import; older ones are retried quietly on the next upload.
func (w *writer) storeRoutes(ctx context.Context, src *source) error {
	byName := map[string]int{}
	for i, f := range src.zip.File {
		byName[f.Name] = i
	}
	rows, err := w.db.W.QueryContext(ctx, `SELECT w.id, w.metadata_json FROM workouts w
		WHERE w.has_route_files = 1
		  AND NOT EXISTS (SELECT 1 FROM workout_routes r WHERE r.workout_id = w.id)`)
	if err != nil {
		return err
	}
	type pending struct {
		id    int64
		files []string
	}
	var todo []pending
	for rows.Next() {
		var id int64
		var meta string
		if err := rows.Scan(&id, &meta); err != nil {
			rows.Close()
			return err
		}
		var m struct {
			Files []string `json:"ritm:route_files"`
		}
		if json.Unmarshal([]byte(meta), &m) == nil && len(m.Files) > 0 {
			todo = append(todo, pending{id, m.Files})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var errs []string
	missing := 0
	for _, p := range todo {
		if err := ctx.Err(); err != nil {
			return err
		}
		var points []RoutePoint
		for _, name := range p.files {
			idx, ok := byName[src.dir+strings.TrimPrefix(name, "/")]
			if !ok {
				if w.newIDs[p.id] {
					missing++
				}
				continue
			}
			f := src.zip.File[idx]
			if f.UncompressedSize64 > maxGPXBytes {
				errs = append(errs, fmt.Sprintf("%s: larger than %d MB", name, maxGPXBytes>>20))
				continue
			}
			if len(points) == maxRoutePoints {
				errs = append(errs, fmt.Sprintf("%s: %v for one workout", name, errTooManyPoints))
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			pts, err := parseGPX(rc, maxRoutePoints-len(points))
			rc.Close()
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", name, err))
				if !errors.Is(err, errTooManyPoints) {
					continue
				}
			}
			points = append(points, pts...)
		}
		if len(points) == 0 {
			continue
		}
		if err := store.InTx(ctx, w.db.W, func(tx *sql.Tx) error {
			return saveRoute(ctx, tx, p.id, points)
		}); err != nil {
			return err
		}
		w.stats.RoutesStored++
	}
	if missing > 0 {
		errs = append(errs, fmt.Sprintf("%d route file(s) not in this export", missing))
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}
