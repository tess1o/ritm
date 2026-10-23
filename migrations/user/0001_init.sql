-- imports
CREATE TABLE imports (id TEXT PRIMARY KEY, status TEXT NOT NULL, file_name TEXT,
  file_size INTEGER, export_date INTEGER, full_reimport INTEGER NOT NULL DEFAULT 0,
  started_at INTEGER, finished_at INTEGER, stats_json TEXT, error TEXT);
CREATE INDEX imports_started ON imports(started_at);
CREATE TABLE ingest_hwm (type TEXT PRIMARY KEY, hwm_ms INTEGER NOT NULL);

-- data sources seen in exports (device kind drives source priority and freshness)
CREATE TABLE sources (name TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('watch','phone','other')),
  first_seen_ms INTEGER, last_seen_ms INTEGER);

-- raw discrete samples
CREATE TABLE records (
  id INTEGER PRIMARY KEY, type TEXT NOT NULL, source_name TEXT NOT NULL,
  unit TEXT, value REAL, category_value TEXT,
  start_ms INTEGER NOT NULL, end_ms INTEGER NOT NULL, tz_offset_min INTEGER NOT NULL,
  metadata_json TEXT, import_id TEXT NOT NULL);
-- natural key; NULLs are coalesced so quantity and category records both dedupe
CREATE UNIQUE INDEX records_nk ON records(type, source_name, start_ms, end_ms,
  ifnull(value, -1e308), ifnull(category_value, ''));
CREATE INDEX records_type_start ON records(type, start_ms);

-- aggregated cumulative data
CREATE TABLE hourly_activity (
  type TEXT NOT NULL, source_name TEXT NOT NULL,
  hour_start_ms INTEGER NOT NULL, local_day TEXT NOT NULL, local_hour INTEGER NOT NULL,
  value REAL NOT NULL,
  PRIMARY KEY (type, source_name, hour_start_ms)) WITHOUT ROWID;
CREATE INDEX hourly_activity_day ON hourly_activity(local_day, type);
CREATE INDEX hourly_activity_type_day ON hourly_activity(type, local_day);

-- per-import accumulation of cumulative samples; emptied at the start of each import
CREATE TABLE hourly_staging (
  type TEXT NOT NULL, source_name TEXT NOT NULL,
  hour_start_ms INTEGER NOT NULL, local_day TEXT NOT NULL, local_hour INTEGER NOT NULL,
  value REAL NOT NULL,
  PRIMARY KEY (type, source_name, hour_start_ms)) WITHOUT ROWID;

CREATE TABLE activity_summaries (
  day TEXT PRIMARY KEY, active_kcal REAL, active_kcal_goal REAL,
  move_min REAL, move_min_goal REAL, exercise_min REAL, exercise_min_goal REAL,
  stand_hours REAL, stand_hours_goal REAL);

-- workouts
CREATE TABLE workouts (
  id INTEGER PRIMARY KEY, activity_type TEXT NOT NULL, source_name TEXT NOT NULL,
  start_ms INTEGER NOT NULL, end_ms INTEGER NOT NULL, tz_offset_min INTEGER NOT NULL,
  duration_s REAL, distance_m REAL, energy_kcal REAL, indoor INTEGER,
  metadata_json TEXT, stats_json TEXT, import_id TEXT NOT NULL,
  UNIQUE (activity_type, source_name, start_ms, end_ms));
CREATE INDEX workouts_start ON workouts(start_ms);
CREATE TABLE workout_events (workout_id INTEGER NOT NULL REFERENCES workouts(id) ON DELETE CASCADE,
  type TEXT NOT NULL, start_ms INTEGER NOT NULL, end_ms INTEGER,
  PRIMARY KEY (workout_id, type, start_ms));
CREATE TABLE workout_activities (workout_id INTEGER NOT NULL REFERENCES workouts(id) ON DELETE CASCADE,
  activity_type TEXT NOT NULL, start_ms INTEGER NOT NULL, end_ms INTEGER NOT NULL, stats_json TEXT,
  PRIMARY KEY (workout_id, start_ms));
CREATE TABLE workout_routes (workout_id INTEGER PRIMARY KEY REFERENCES workouts(id) ON DELETE CASCADE,
  points BLOB NOT NULL);
CREATE TABLE workout_annotations (            -- user-entered, survives re-imports
  workout_key TEXT PRIMARY KEY,                -- activity_type|start_ms|end_ms
  rpe INTEGER, notes TEXT, updated_at INTEGER NOT NULL);

-- derived
CREATE TABLE workout_metrics (workout_id INTEGER PRIMARY KEY REFERENCES workouts(id) ON DELETE CASCADE,
  moving_s REAL, avg_hr REAL, max_hr REAL, zone_s_json TEXT, pct_z3_plus REAL,
  kcal_per_min REAL, trimp_edwards REAL, trimp_banister REAL, srpe_load REAL,
  hrr60 REAL, pace_s_per_km REAL, computed_at INTEGER NOT NULL);
CREATE TABLE daily_activity (day TEXT PRIMARY KEY, steps REAL, distance_m REAL,
  active_kcal REAL, basal_kcal REAL, total_kcal REAL, flights REAL,
  exercise_min REAL, stand_min REAL, stand_hours INTEGER, active_hours INTEGER,
  longest_sedentary_h REAL, steps_by_hour_json TEXT, is_gym_day INTEGER,
  summary_mismatch INTEGER,
  ring_move_kcal REAL, ring_move_goal REAL, ring_exercise_min REAL, ring_exercise_goal REAL,
  ring_stand_hours REAL, ring_stand_goal REAL, has_watch_data INTEGER);
CREATE TABLE daily_heart (day TEXT PRIMARY KEY, hr_min REAL, hr_avg REAL, hr_max REAL,
  hr_sleep_avg REAL, resting_hr REAL, walking_hr REAL, hrv_sdnn REAL, vo2max REAL,
  spo2_avg REAL, spo2_min REAL, resp_rate REAL, wrist_temp_dev REAL, heart_events INTEGER,
  hr_samples INTEGER);
CREATE TABLE daily_sleep (day TEXT PRIMARY KEY, in_bed_min REAL, asleep_min REAL,
  core_min REAL, deep_min REAL, rem_min REAL, awake_min REAL,
  bedtime_ms INTEGER, wake_ms INTEGER);
CREATE TABLE daily_load (day TEXT PRIMARY KEY, load REAL NOT NULL DEFAULT 0,
  ctl REAL, atl REAL, tsb REAL, acwr REAL, sessions INTEGER NOT NULL DEFAULT 0);

CREATE TABLE insights (id INTEGER PRIMARY KEY, rule_id TEXT NOT NULL, category TEXT NOT NULL,
  polarity TEXT NOT NULL, period_kind TEXT NOT NULL, period_start TEXT NOT NULL,
  period_end TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, metric TEXT,
  current_value REAL, baseline_value REAL, delta_pct REAL, evidence_json TEXT,
  created_at INTEGER NOT NULL, UNIQUE (rule_id, period_kind, period_start));
CREATE INDEX insights_period ON insights(period_end, period_kind);

CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE data_freshness (               -- single row, updated after each import
  id INTEGER PRIMARY KEY CHECK (id = 1),
  last_data_at INTEGER,                        -- newest Watch-sourced sample
  last_import_at INTEGER,                      -- last successful import
  last_import_id TEXT,
  last_checked_at INTEGER);                    -- last call to the monitor endpoint
INSERT INTO data_freshness (id) VALUES (1);

-- user-facing views
CREATE VIEW v_workouts AS
SELECT w.id, w.activity_type, w.source_name,
  strftime('%Y-%m-%d', (w.start_ms / 1000) + w.tz_offset_min * 60, 'unixepoch') AS local_day,
  w.start_ms, w.end_ms, w.tz_offset_min,
  w.duration_s, w.distance_m, w.energy_kcal AS energy_kcal, w.indoor,
  m.moving_s, m.avg_hr AS avg_hr_bpm, m.max_hr AS max_hr_bpm, m.zone_s_json, m.pct_z3_plus,
  m.kcal_per_min, m.trimp_edwards, m.trimp_banister, m.srpe_load, m.hrr60 AS hrr60_bpm,
  m.pace_s_per_km, a.rpe, a.notes
FROM workouts w
LEFT JOIN workout_metrics m ON m.workout_id = w.id
LEFT JOIN workout_annotations a ON a.workout_key = w.activity_type || '|' || w.start_ms || '|' || w.end_ms;

CREATE VIEW v_daily AS
WITH days AS (
  SELECT day FROM daily_activity UNION SELECT day FROM daily_heart
  UNION SELECT day FROM daily_sleep UNION SELECT day FROM daily_load)
SELECT d.day,
  a.steps, a.distance_m, a.active_kcal, a.basal_kcal, a.total_kcal, a.flights,
  a.exercise_min, a.stand_min, a.stand_hours, a.active_hours, a.longest_sedentary_h,
  a.is_gym_day, a.ring_move_kcal, a.ring_move_goal, a.ring_exercise_min, a.ring_exercise_goal,
  a.ring_stand_hours, a.ring_stand_goal,
  h.hr_min AS hr_min_bpm, h.hr_avg AS hr_avg_bpm, h.hr_max AS hr_max_bpm,
  h.hr_sleep_avg AS hr_sleep_avg_bpm, h.resting_hr AS resting_hr_bpm, h.walking_hr AS walking_hr_bpm,
  h.hrv_sdnn AS hrv_sdnn_ms, h.vo2max AS vo2max_ml_kg_min, h.spo2_avg AS spo2_avg_pct,
  h.spo2_min AS spo2_min_pct, h.resp_rate AS resp_rate_bpm, h.wrist_temp_dev AS wrist_temp_dev_c,
  h.heart_events,
  s.in_bed_min, s.asleep_min, s.core_min, s.deep_min, s.rem_min, s.awake_min,
  l.load, l.ctl, l.atl, l.tsb, l.acwr, l.sessions
FROM days d
LEFT JOIN daily_activity a ON a.day = d.day
LEFT JOIN daily_heart h ON h.day = d.day
LEFT JOIN daily_sleep s ON s.day = d.day
LEFT JOIN daily_load l ON l.day = d.day;
