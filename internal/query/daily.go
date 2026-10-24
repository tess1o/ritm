package query

import (
	"context"
	"database/sql"
	"encoding/json"

	"ritm/internal/days"
	"ritm/internal/insights"
	"ritm/internal/mathx"
)

type Rings struct {
	MoveKcal        *float64 `json:"move_kcal"`
	MoveGoalKcal    *float64 `json:"move_goal_kcal"`
	ExerciseMin     *float64 `json:"exercise_min"`
	ExerciseGoalMin *float64 `json:"exercise_goal_min"`
	StandHours      *float64 `json:"stand_hours"`
	StandGoalHours  *float64 `json:"stand_goal_hours"`
}

type DailyActivity struct {
	Day               string    `json:"day"`
	Steps             *float64  `json:"steps"`
	DistanceM         *float64  `json:"distance_m"`
	ActiveKcal        *float64  `json:"active_kcal"`
	BasalKcal         *float64  `json:"basal_kcal"`
	TotalKcal         *float64  `json:"total_kcal"`
	Flights           *float64  `json:"flights"`
	ExerciseMin       *float64  `json:"exercise_min"`
	StandMin          *float64  `json:"stand_min"`
	StandHours        *int64    `json:"stand_hours"`
	ActiveHours       *int64    `json:"active_hours"`
	LongestSedentaryH *float64  `json:"longest_sedentary_h"`
	StepsByHour       []float64 `json:"steps_by_hour,omitempty"`
	IsGymDay          bool      `json:"is_gym_day"`
	SummaryMismatch   bool      `json:"summary_mismatch"`
	HasWatchData      bool      `json:"has_watch_data"`
	Rings             Rings     `json:"rings"`
}

func (s *Service) DailyActivity(ctx context.Context, r days.Range, withHours bool) ([]DailyActivity, error) {
	rows, err := s.db.RO.QueryContext(ctx, `SELECT day, steps, distance_m, active_kcal, basal_kcal, total_kcal, flights,
		exercise_min, stand_min, stand_hours, active_hours, longest_sedentary_h, steps_by_hour_json, ifnull(is_gym_day, 0),
		ifnull(summary_mismatch, 0), ifnull(has_watch_data, 0),
		ring_move_kcal, ring_move_goal, ring_exercise_min, ring_exercise_goal, ring_stand_hours, ring_stand_goal
		FROM daily_activity WHERE day BETWEEN ? AND ? ORDER BY day`, r.From.String(), r.To.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DailyActivity{}
	for rows.Next() {
		var (
			a                                        DailyActivity
			steps, dist, ak, bk, tk, fl, ex, sm, sed sql.NullFloat64
			sh, ah                                   sql.NullInt64
			hours                                    sql.NullString
			gym, mismatch, watch                     int
			rm, rmg, re, reg, rs, rsg                sql.NullFloat64
		)
		if err := rows.Scan(&a.Day, &steps, &dist, &ak, &bk, &tk, &fl, &ex, &sm, &sh, &ah, &sed, &hours, &gym, &mismatch, &watch,
			&rm, &rmg, &re, &reg, &rs, &rsg); err != nil {
			return nil, err
		}
		a.Steps, a.DistanceM, a.ActiveKcal, a.BasalKcal, a.TotalKcal, a.Flights = nf(steps), nf(dist), nf(ak), nf(bk), nf(tk), nf(fl)
		a.ExerciseMin, a.StandMin, a.LongestSedentaryH = nf(ex), nf(sm), nf(sed)
		a.StandHours, a.ActiveHours = ni(sh), ni(ah)
		a.IsGymDay, a.SummaryMismatch, a.HasWatchData = gym == 1, mismatch == 1, watch == 1
		a.Rings = Rings{nf(rm), nf(rmg), nf(re), nf(reg), nf(rs), nf(rsg)}
		if withHours && hours.Valid {
			_ = json.Unmarshal([]byte(hours.String), &a.StepsByHour)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type DailyHeart struct {
	Day          string   `json:"day"`
	HRMinBpm     *float64 `json:"hr_min_bpm"`
	HRAvgBpm     *float64 `json:"hr_avg_bpm"`
	HRMaxBpm     *float64 `json:"hr_max_bpm"`
	HRSleepBpm   *float64 `json:"hr_sleep_avg_bpm"`
	RestingBpm   *float64 `json:"resting_hr_bpm"`
	WalkingBpm   *float64 `json:"walking_hr_bpm"`
	HRVMs        *float64 `json:"hrv_sdnn_ms"`
	VO2Max       *float64 `json:"vo2max_ml_kg_min"`
	SpO2AvgPct   *float64 `json:"spo2_avg_pct"`
	SpO2MinPct   *float64 `json:"spo2_min_pct"`
	RespRateBpm  *float64 `json:"resp_rate_bpm"`
	WristTempDev *float64 `json:"wrist_temp_dev_c"`
	HeartEvents  int64    `json:"heart_events"`
}

func (s *Service) DailyHeart(ctx context.Context, r days.Range) ([]DailyHeart, error) {
	rows, err := s.db.RO.QueryContext(ctx, `SELECT day, hr_min, hr_avg, hr_max, hr_sleep_avg, resting_hr, walking_hr, hrv_sdnn,
		vo2max, spo2_avg, spo2_min, resp_rate, wrist_temp_dev, ifnull(heart_events, 0)
		FROM daily_heart WHERE day BETWEEN ? AND ? ORDER BY day`, r.From.String(), r.To.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DailyHeart{}
	for rows.Next() {
		var h DailyHeart
		var v [12]sql.NullFloat64
		if err := rows.Scan(&h.Day, &v[0], &v[1], &v[2], &v[3], &v[4], &v[5], &v[6], &v[7], &v[8], &v[9], &v[10], &v[11], &h.HeartEvents); err != nil {
			return nil, err
		}
		h.HRMinBpm, h.HRAvgBpm, h.HRMaxBpm, h.HRSleepBpm = nf(v[0]), nf(v[1]), nf(v[2]), nf(v[3])
		h.RestingBpm, h.WalkingBpm, h.HRVMs, h.VO2Max = nf(v[4]), nf(v[5]), nf(v[6]), nf(v[7])
		h.SpO2AvgPct, h.SpO2MinPct, h.RespRateBpm, h.WristTempDev = nf(v[8]), nf(v[9]), nf(v[10]), nf(v[11])
		out = append(out, h)
	}
	return out, rows.Err()
}

type SleepNight struct {
	Day       string   `json:"day"` // wake-up day
	InBedMin  *float64 `json:"in_bed_min"`
	AsleepMin *float64 `json:"asleep_min"`
	CoreMin   *float64 `json:"core_min"`
	DeepMin   *float64 `json:"deep_min"`
	REMMin    *float64 `json:"rem_min"`
	AwakeMin  *float64 `json:"awake_min"`
	BedtimeMS *int64   `json:"bedtime_ms"`
	WakeMS    *int64   `json:"wake_ms"`
}

func (s *Service) Sleep(ctx context.Context, r days.Range) ([]SleepNight, error) {
	rows, err := s.db.RO.QueryContext(ctx, `SELECT day, in_bed_min, asleep_min, core_min, deep_min, rem_min, awake_min, bedtime_ms, wake_ms
		FROM daily_sleep WHERE day BETWEEN ? AND ? ORDER BY day`, r.From.String(), r.To.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SleepNight{}
	for rows.Next() {
		var n SleepNight
		var v [6]sql.NullFloat64
		var bed, wake sql.NullInt64
		if err := rows.Scan(&n.Day, &v[0], &v[1], &v[2], &v[3], &v[4], &v[5], &bed, &wake); err != nil {
			return nil, err
		}
		n.InBedMin, n.AsleepMin, n.CoreMin, n.DeepMin, n.REMMin, n.AwakeMin = nf(v[0]), nf(v[1]), nf(v[2]), nf(v[3]), nf(v[4]), nf(v[5])
		n.BedtimeMS, n.WakeMS = ni(bed), ni(wake)
		out = append(out, n)
	}
	return out, rows.Err()
}

type HeartEvent struct {
	Type     string            `json:"type" enum:"high,low,irregular"`
	StartMS  int64             `json:"start_ms"`
	EndMS    int64             `json:"end_ms"`
	Source   string            `json:"source"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func (s *Service) HeartEvents(ctx context.Context, r days.Range) ([]HeartEvent, error) {
	from, to := s.dayBounds(r)
	rows, err := s.db.RO.QueryContext(ctx, `SELECT type, start_ms, end_ms, source_name, metadata_json FROM records
		WHERE type IN ('HKCategoryTypeIdentifierHighHeartRateEvent', 'HKCategoryTypeIdentifierLowHeartRateEvent',
			'HKCategoryTypeIdentifierIrregularHeartRhythmEvent') AND start_ms >= ? AND start_ms < ? ORDER BY start_ms DESC`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HeartEvent{}
	for rows.Next() {
		var e HeartEvent
		var typ string
		var meta sql.NullString
		if err := rows.Scan(&typ, &e.StartMS, &e.EndMS, &e.Source, &meta); err != nil {
			return nil, err
		}
		switch typ {
		case "HKCategoryTypeIdentifierHighHeartRateEvent":
			e.Type = "high"
		case "HKCategoryTypeIdentifierLowHeartRateEvent":
			e.Type = "low"
		default:
			e.Type = "irregular"
		}
		if meta.Valid {
			_ = json.Unmarshal([]byte(meta.String), &e.Metadata)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RecoveryGroup is the mean next-day resting HR and HRV after one kind of day.
type RecoveryGroup struct {
	Days         int      `json:"days"`
	RestingHRBpm *float64 `json:"resting_hr_bpm"`
	HRVMs        *float64 `json:"hrv_sdnn_ms"`
}

// Recovery compares the mornings after training days with those after rest days.
type Recovery struct {
	AfterTraining RecoveryGroup `json:"after_training"`
	AfterRest     RecoveryGroup `json:"after_rest"`
}

// Recovery computes next-day recovery for the days in r, with the same rule as the
// recovery.next_day insight.
func (s *Service) Recovery(ctx context.Context, r days.Range) (*Recovery, error) {
	res, err := s.Resolved(ctx)
	if err != nil {
		return nil, err
	}
	data, err := insights.Load(ctx, s.db.RO, res.SessionTypesEffective, r.From.Add(-1), r.To)
	if err != nil {
		return nil, err
	}
	v := data.NextDay(r)
	group := func(rhr, hrv []float64) RecoveryGroup {
		return RecoveryGroup{Days: len(rhr), RestingHRBpm: mathx.RoundPtr(mathx.Mean(rhr), 1), HRVMs: mathx.RoundPtr(mathx.Mean(hrv), 1)}
	}
	return &Recovery{AfterTraining: group(v.AfterGymRHR, v.AfterGymHRV), AfterRest: group(v.AfterRestRHR, v.AfterRestHRV)}, nil
}
