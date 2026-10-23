-- Where each stored route starts and ends and the area it covers, so workout lists can place and
-- filter workouts without decoding the points. Existing routes are filled in at start-up
-- (ingest.BackfillRouteSummaries), since SQL cannot decode the point blob.
ALTER TABLE workout_routes ADD COLUMN point_count INTEGER;
ALTER TABLE workout_routes ADD COLUMN start_lat REAL;
ALTER TABLE workout_routes ADD COLUMN start_lon REAL;
ALTER TABLE workout_routes ADD COLUMN end_lat REAL;
ALTER TABLE workout_routes ADD COLUMN end_lon REAL;
ALTER TABLE workout_routes ADD COLUMN min_lat REAL;
ALTER TABLE workout_routes ADD COLUMN min_lon REAL;
ALTER TABLE workout_routes ADD COLUMN max_lat REAL;
ALTER TABLE workout_routes ADD COLUMN max_lon REAL;

-- Goes up with every change to the user's data (imports, recomputes, settings, RPE and notes);
-- API clients use it, through ETags, to tell whether what they cached is still current.
ALTER TABLE data_freshness ADD COLUMN data_version INTEGER NOT NULL DEFAULT 0;

-- v_workouts gains the stable workout key and the route's location columns.
DROP VIEW v_workouts;
CREATE VIEW v_workouts AS
SELECT w.id, w.activity_type || '|' || w.start_ms || '|' || w.end_ms AS workout_key,
  w.activity_type, w.source_name,
  strftime('%Y-%m-%d', (w.start_ms / 1000) + w.tz_offset_min * 60, 'unixepoch') AS local_day,
  w.start_ms, w.end_ms, w.tz_offset_min,
  w.duration_s, w.distance_m, w.energy_kcal AS energy_kcal, w.indoor,
  m.moving_s, m.avg_hr AS avg_hr_bpm, m.max_hr AS max_hr_bpm, m.zone_s_json, m.pct_z3_plus,
  m.kcal_per_min, m.trimp_edwards, m.trimp_banister, m.srpe_load, m.load, m.hrr60 AS hrr60_bpm,
  m.pace_s_per_km, a.rpe, a.notes,
  r.workout_id IS NOT NULL AS has_route, r.point_count AS route_points,
  r.start_lat, r.start_lon, r.end_lat, r.end_lon, r.min_lat, r.min_lon, r.max_lat, r.max_lon
FROM workouts w
LEFT JOIN workout_metrics m ON m.workout_id = w.id
LEFT JOIN workout_annotations a ON a.workout_key = w.activity_type || '|' || w.start_ms || '|' || w.end_ms
LEFT JOIN workout_routes r ON r.workout_id = w.id;
