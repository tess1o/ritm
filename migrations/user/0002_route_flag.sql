-- v_workouts gains has_route (a GPS route is stored for the workout).
DROP VIEW v_workouts;
CREATE VIEW v_workouts AS
SELECT w.id, w.activity_type, w.source_name,
  strftime('%Y-%m-%d', (w.start_ms / 1000) + w.tz_offset_min * 60, 'unixepoch') AS local_day,
  w.start_ms, w.end_ms, w.tz_offset_min,
  w.duration_s, w.distance_m, w.energy_kcal AS energy_kcal, w.indoor,
  m.moving_s, m.avg_hr AS avg_hr_bpm, m.max_hr AS max_hr_bpm, m.zone_s_json, m.pct_z3_plus,
  m.kcal_per_min, m.trimp_edwards, m.trimp_banister, m.srpe_load, m.hrr60 AS hrr60_bpm,
  m.pace_s_per_km, a.rpe, a.notes,
  EXISTS (SELECT 1 FROM workout_routes r WHERE r.workout_id = w.id) AS has_route
FROM workouts w
LEFT JOIN workout_metrics m ON m.workout_id = w.id
LEFT JOIN workout_annotations a ON a.workout_key = w.activity_type || '|' || w.start_ms || '|' || w.end_ms;
