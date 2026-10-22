-- workout_metrics.load is the workout's training load in the selected load model (sRPE estimates
-- unrated workouts from TRIMP); workouts.has_route_files marks workouts whose export lists GPX files.
ALTER TABLE workout_metrics ADD COLUMN load REAL;
ALTER TABLE workouts ADD COLUMN has_route_files INTEGER NOT NULL DEFAULT 0;
UPDATE workouts SET has_route_files = 1 WHERE metadata_json LIKE '%"ritm:route_files"%';

-- Extra ingest types are read from the "app" settings row, which always held them too.
DELETE FROM settings WHERE key = 'ingest.extra_types';

-- Ask the next start-up to recompute, so load is filled for existing workouts.
INSERT INTO settings (key, value) VALUES ('derive.pending_from', json_quote(date('now')))
  ON CONFLICT (key) DO NOTHING;

DROP VIEW v_workouts;
CREATE VIEW v_workouts AS
SELECT w.id, w.activity_type, w.source_name,
  strftime('%Y-%m-%d', (w.start_ms / 1000) + w.tz_offset_min * 60, 'unixepoch') AS local_day,
  w.start_ms, w.end_ms, w.tz_offset_min,
  w.duration_s, w.distance_m, w.energy_kcal AS energy_kcal, w.indoor,
  m.moving_s, m.avg_hr AS avg_hr_bpm, m.max_hr AS max_hr_bpm, m.zone_s_json, m.pct_z3_plus,
  m.kcal_per_min, m.trimp_edwards, m.trimp_banister, m.srpe_load, m.load, m.hrr60 AS hrr60_bpm,
  m.pace_s_per_km, a.rpe, a.notes,
  EXISTS (SELECT 1 FROM workout_routes r WHERE r.workout_id = w.id) AS has_route
FROM workouts w
LEFT JOIN workout_metrics m ON m.workout_id = w.id
LEFT JOIN workout_annotations a ON a.workout_key = w.activity_type || '|' || w.start_ms || '|' || w.end_ms;
