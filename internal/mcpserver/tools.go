package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"ritm/internal/days"
	"ritm/internal/insights"
	"ritm/internal/period"
	"ritm/internal/query"
	"ritm/internal/tenant"
)

// tools binds the tool handlers to one user's data. t is nil only when building the catalog.
type tools struct {
	deps Deps
	t    *tenant.Tenant
}

// toolOrder is the order tools are listed in the UI.
var toolOrder = []string{"get_overview", "get_insights", "get_daily_activity", "get_day_detail", "get_heart_metrics",
	"get_sleep", "list_workouts", "get_workout", "get_training_load", "compare_periods", "get_settings", "query_sql"}

type rangeIn struct {
	Period string `json:"period,omitempty" jsonschema:"Period: today, yesterday, last_7d, last_30d, last_90d, last_<n>d, this_week, last_week, this_month, last_month, ytd, YYYY-MM-DD or FROM..TO. Overrides from/to."`
	From   string `json:"from,omitempty" jsonschema:"First day, YYYY-MM-DD (or a relative name such as last_month)."`
	To     string `json:"to,omitempty" jsonschema:"Last day, YYYY-MM-DD (default today)."`
}

type seriesIn struct {
	Period      string `json:"period,omitempty" jsonschema:"Period: today, yesterday, last_7d, last_30d, last_90d, last_<n>d, this_week, last_week, this_month, last_month, ytd, YYYY-MM-DD or FROM..TO. Overrides from/to."`
	From        string `json:"from,omitempty" jsonschema:"First day, YYYY-MM-DD (or a relative name such as last_month)."`
	To          string `json:"to,omitempty" jsonschema:"Last day, YYYY-MM-DD (default today)."`
	Granularity string `json:"granularity,omitempty" jsonschema:"auto (default: day up to 3 months, week up to 2 years, else month), day, week or month. Weeks/months hold per-day averages."`
	Limit       int    `json:"limit,omitempty" jsonschema:"Maximum rows (default 100, max 500); the most recent rows are kept."`
}

func (s seriesIn) rng() rangeIn { return rangeIn{Period: s.Period, From: s.From, To: s.To} }

func (t *tools) resolve(in rangeIn, defaultDays int) (days.Range, error) {
	today := t.t.Query.Today()
	if in.Period != "" {
		return period.Resolve(in.Period, today)
	}
	return period.FromTo(in.From, in.To, today, defaultDays)
}

func limitOf(n, def, max int) int {
	if n <= 0 {
		return def
	}
	return min(n, max)
}

// series shapes daily rows into the requested granularity with a row cap.
func series(rg days.Range, rows []row, in seriesIn) (map[string]any, error) {
	gran, err := resolveGranularity(in.Granularity, rg)
	if err != nil {
		return nil, err
	}
	rows, truncated := capRows(aggregate(rows, gran), limitOf(in.Limit, 100, 500))
	out := map[string]any{"from": rg.From.String(), "to": rg.To.String(), "granularity": gran, "rows": rows}
	if truncated {
		out["truncated"] = true
	}
	return out, nil
}

func (t *tools) register(s *mcp.Server) {
	addTool(s, t, "get_overview", "Overview",
		"Summary of a period (default last_7d): activity, heart, sleep and training averages with deltas vs the personal 4-week baseline, top insights, summary scores and data freshness. Start here.",
		func(ctx context.Context, in struct {
			Period string `json:"period,omitempty" jsonschema:"Period (default last_7d): last_7d, last_30d, this_week, last_week, this_month, last_month, ytd, YYYY-MM-DD or FROM..TO."`
		}) (any, error) {
			p := in.Period
			if p == "" {
				p = "last_7d"
			}
			rg, err := period.Resolve(p, t.t.Query.Today())
			if err != nil {
				return nil, err
			}
			return t.t.Query.Overview(ctx, rg, t.deps.Cfg.StaleAfter)
		})

	addTool(s, t, "get_insights", "Insights",
		"Rule-based insights (current period vs personal baseline) with evidence. Without a period only the latest insight per rule is returned.",
		func(ctx context.Context, in struct {
			Period     string `json:"period,omitempty" jsonschema:"Only insights whose period overlaps this one (same syntax as other tools)."`
			Category   string `json:"category,omitempty" jsonschema:"activity, heart, training or recovery."`
			Polarity   string `json:"polarity,omitempty" jsonschema:"positive, neutral or attention."`
			LatestOnly *bool  `json:"latest_only,omitempty" jsonschema:"Only the most recent period per rule (default true without a period)."`
			Limit      int    `json:"limit,omitempty" jsonschema:"Maximum insights (default 30, max 200)."`
		}) (any, error) {
			if in.Category != "" && !slices.Contains([]string{"activity", "heart", "training", "recovery"}, in.Category) {
				return nil, errors.New("category must be activity, heart, training or recovery")
			}
			if in.Polarity != "" && !slices.Contains([]string{"positive", "neutral", "attention"}, in.Polarity) {
				return nil, errors.New("polarity must be positive, neutral or attention")
			}
			limit := limitOf(in.Limit, 30, 200)
			f := insights.Filter{Today: t.t.Query.Today(), Category: in.Category, Polarity: in.Polarity, Limit: limit + 1,
				LatestOnly: in.Period == ""}
			if in.LatestOnly != nil {
				f.LatestOnly = *in.LatestOnly
			}
			if in.Period != "" {
				rg, err := period.Resolve(in.Period, f.Today)
				if err != nil {
					return nil, err
				}
				f.From, f.To = rg.From.String(), rg.To.String()
			}
			list, err := t.t.Query.Insights(ctx, f)
			if err != nil {
				return nil, err
			}
			out := map[string]any{}
			if len(list) > limit {
				list, out["truncated"] = list[:limit], true
			}
			out["insights"] = list
			return out, nil
		})

	addTool(s, t, "get_daily_activity", "Daily activity",
		"Per-day steps, walking distance, active/basal/total kcal, flights, exercise and stand minutes, stand and active hours, longest sedentary stretch, gym day and Activity rings (default last 30 days).",
		func(ctx context.Context, in seriesIn) (any, error) {
			rg, err := t.resolve(in.rng(), 30)
			if err != nil {
				return nil, err
			}
			list, err := t.t.Query.DailyActivity(ctx, rg, false)
			if err != nil {
				return nil, err
			}
			rows, err := toRows(list)
			if err != nil {
				return nil, err
			}
			return series(rg, rows, in)
		})

	addTool(s, t, "get_day_detail", "Day detail",
		"One day in detail: activity totals with hourly steps, heart summary, HR curve in 15-minute buckets, stood hours, workouts and sleep stages.",
		func(ctx context.Context, in struct {
			Day string `json:"day" jsonschema:"YYYY-MM-DD, today or yesterday."`
		}) (any, error) {
			d, err := period.Day(in.Day, t.t.Query.Today())
			if err != nil {
				return nil, err
			}
			return t.t.Query.DayDetail(ctx, d, 900)
		})

	addTool(s, t, "get_heart_metrics", "Heart metrics",
		"Time series of one heart metric with summary statistics (default last 90 days). Metrics: resting_hr, hrv (SDNN), walking_hr, vo2max, hr_daily (min/avg/max/sleep), spo2, resp_rate, wrist_temp.",
		func(ctx context.Context, in struct {
			Metric      string `json:"metric" jsonschema:"resting_hr, hrv, walking_hr, vo2max, hr_daily, spo2, resp_rate or wrist_temp."`
			Period      string `json:"period,omitempty" jsonschema:"Period (same syntax as other tools). Overrides from/to."`
			From        string `json:"from,omitempty" jsonschema:"First day, YYYY-MM-DD."`
			To          string `json:"to,omitempty" jsonschema:"Last day, YYYY-MM-DD (default today)."`
			Rolling     int    `json:"rolling,omitempty" jsonschema:"Add a trailing rolling mean over this many days (1-90)."`
			Granularity string `json:"granularity,omitempty" jsonschema:"auto (default), day, week or month."`
			Limit       int    `json:"limit,omitempty" jsonschema:"Maximum rows (default 100, max 500)."`
		}) (any, error) {
			fields, ok := heartMetrics[in.Metric]
			if !ok {
				return nil, fmt.Errorf("metric must be one of %s", strings.Join(heartMetricNames, ", "))
			}
			if in.Rolling < 0 || in.Rolling > 90 {
				return nil, errors.New("rolling must be between 1 and 90 days")
			}
			rg, err := t.resolve(rangeIn{Period: in.Period, From: in.From, To: in.To}, 90)
			if err != nil {
				return nil, err
			}
			list, err := t.t.Query.DailyHeart(ctx, rg)
			if err != nil {
				return nil, err
			}
			all, err := toRows(list)
			if err != nil {
				return nil, err
			}
			rows := heartSeries(all, fields, in.Rolling)
			out, err := series(rg, rows, seriesIn{Granularity: in.Granularity, Limit: in.Limit})
			if err != nil {
				return nil, err
			}
			out["metric"] = in.Metric
			out["summary"] = summarize(rows, fields[0])
			return out, nil
		})

	addTool(s, t, "get_sleep", "Sleep",
		"Nightly sleep (assigned to the wake-up day): in bed, asleep, core/deep/REM/awake minutes, bedtime and wake time (default last 30 days). Empty when the Watch is not worn at night.",
		func(ctx context.Context, in seriesIn) (any, error) {
			rg, err := t.resolve(in.rng(), 30)
			if err != nil {
				return nil, err
			}
			list, err := t.t.Query.Sleep(ctx, rg)
			if err != nil {
				return nil, err
			}
			rows, err := toRows(list)
			if err != nil {
				return nil, err
			}
			out, err := series(rg, rows, in)
			if err != nil {
				return nil, err
			}
			if len(rows) == 0 {
				out["note"] = "No sleep data in this range."
			}
			return out, nil
		})

	addTool(s, t, "list_workouts", "Workouts",
		"Workouts with key stats: duration, active kcal, avg/max HR, % time in zone 3+, training load (TRIMP), HR recovery, RPE and notes (default last 90 days, newest first).",
		func(ctx context.Context, in struct {
			Period       string `json:"period,omitempty" jsonschema:"Period (same syntax as other tools). Overrides from/to."`
			From         string `json:"from,omitempty" jsonschema:"First day, YYYY-MM-DD."`
			To           string `json:"to,omitempty" jsonschema:"Last day, YYYY-MM-DD (default today)."`
			ActivityType string `json:"activity_type,omitempty" jsonschema:"HealthKit workout type, e.g. FunctionalStrengthTraining or HKWorkoutActivityTypeWalking."`
			Limit        int    `json:"limit,omitempty" jsonschema:"Maximum workouts (default 100, max 500)."`
		}) (any, error) {
			rg, err := t.resolve(rangeIn{Period: in.Period, From: in.From, To: in.To}, 90)
			if err != nil {
				return nil, err
			}
			typ := strings.ReplaceAll(strings.TrimSpace(in.ActivityType), " ", "")
			if typ != "" && !strings.HasPrefix(typ, "HKWorkoutActivityType") {
				typ = "HKWorkoutActivityType" + typ
			}
			list, truncated, err := t.t.Query.Workouts(ctx, query.WorkoutFilter{Range: rg, ActivityType: typ}, limitOf(in.Limit, 100, 500))
			if err != nil {
				return nil, err
			}
			out := map[string]any{"from": rg.From.String(), "to": rg.To.String(), "workouts": list}
			if truncated {
				out["truncated"] = true
			}
			return out, nil
		})

	addTool(s, t, "get_workout", "Workout detail",
		"One workout: stats, time in HR zones, pauses and events, Apple statistics, RPE/notes, the previous workout of the same type, the HR series (about 120 points) and, for outdoor workouts with GPS, a route summary (distance, elevation gain, bounds).",
		func(ctx context.Context, in struct {
			ID int64 `json:"id" jsonschema:"Workout id from list_workouts."`
		}) (any, error) {
			w, err := t.t.Query.Workout(ctx, in.ID)
			if errors.Is(err, query.ErrNotFound) {
				return nil, fmt.Errorf("workout %d not found", in.ID)
			}
			if err != nil {
				return nil, err
			}
			hr, err := t.t.Query.WorkoutHR(ctx, in.ID, 120)
			if err != nil {
				return nil, err
			}
			out := map[string]any{"workout": w, "hr_series": hr}
			if w.HasRoute {
				// A summary only: raw GPS points would flood the context.
				rt, err := t.t.Query.Route(ctx, in.ID, 2)
				if err != nil && !errors.Is(err, query.ErrNotFound) {
					return nil, err
				}
				if rt != nil {
					out["route"] = rt.RouteSummary
				}
			}
			return out, nil
		})

	addTool(s, t, "get_training_load", "Training load",
		"Daily training load with fitness (CTL, 42-day), fatigue (ATL, 7-day), form (TSB = CTL - ATL) and ACWR, plus weekly session consistency vs the target and streaks (default last 90 days).",
		func(ctx context.Context, in seriesIn) (any, error) {
			rg, err := t.resolve(in.rng(), 90)
			if err != nil {
				return nil, err
			}
			load, err := t.t.Query.TrainingLoad(ctx, rg)
			if err != nil {
				return nil, err
			}
			rows, err := toRows(load.Days)
			if err != nil {
				return nil, err
			}
			out, err := series(rg, rows, in)
			if err != nil {
				return nil, err
			}
			weekly, err := t.t.Query.TrainingWeekly(ctx, rg)
			if err != nil {
				return nil, err
			}
			cons, err := toAny(weekly)
			if err != nil {
				return nil, err
			}
			delete(cons.(row), "sessions") // list_workouts has them
			out["load_model"] = load.LoadModel
			out["latest"] = load.Latest
			out["consistency"] = cons
			return out, nil
		})

	addTool(s, t, "compare_periods", "Compare periods",
		"Side-by-side per-day averages of activity, heart and sleep plus training totals for two periods, with delta = a - b and delta_pct relative to b. Training sessions count the main workout type (see get_settings); all_workouts counts every workout.",
		func(ctx context.Context, in struct {
			A string `json:"a" jsonschema:"First period, e.g. last_month or 2026-09-01..2026-09-30."`
			B string `json:"b" jsonschema:"Second period to compare against."`
		}) (any, error) {
			today := t.t.Query.Today()
			ra, err := period.Resolve(in.A, today)
			if err != nil {
				return nil, fmt.Errorf("a: %w", err)
			}
			rb, err := period.Resolve(in.B, today)
			if err != nil {
				return nil, fmt.Errorf("b: %w", err)
			}
			sa, err := t.summary(ctx, ra)
			if err != nil {
				return nil, err
			}
			sb, err := t.summary(ctx, rb)
			if err != nil {
				return nil, err
			}
			out := map[string]any{
				"a": map[string]any{"from": ra.From.String(), "to": ra.To.String(), "days": ra.Len()},
				"b": map[string]any{"from": rb.From.String(), "to": rb.To.String(), "days": rb.Len()},
			}
			for _, k := range []string{"activity", "heart", "sleep", "training"} {
				out[k] = compare(sa[k], sb[k])
			}
			return out, nil
		})

	addTool(s, t, "get_settings", "Settings",
		"Goals, heart-rate zones in bpm (model, max and resting HR), training-load model, main workout type, waking hours and data-source priority.",
		func(ctx context.Context, _ struct{}) (any, error) {
			return t.t.Query.Resolved(ctx)
		})

	t.registerSQL(s)
}

var heartMetrics = map[string][]string{
	"resting_hr": {"resting_hr_bpm"},
	"hrv":        {"hrv_sdnn_ms"},
	"walking_hr": {"walking_hr_bpm"},
	"vo2max":     {"vo2max_ml_kg_min"},
	"hr_daily":   {"hr_avg_bpm", "hr_min_bpm", "hr_max_bpm", "hr_sleep_avg_bpm"},
	"spo2":       {"spo2_avg_pct", "spo2_min_pct"},
	"resp_rate":  {"resp_rate_bpm"},
	"wrist_temp": {"wrist_temp_dev_c"},
}

var heartMetricNames = []string{"resting_hr", "hrv", "walking_hr", "vo2max", "hr_daily", "spo2", "resp_rate", "wrist_temp"}

// heartSeries keeps the days that have the metric, with an optional trailing rolling mean of
// the first field over the previous n calendar days.
func heartSeries(all []row, fields []string, n int) []row {
	var out []row
	for _, r := range all {
		o := row{"day": r["day"]}
		has := false
		for _, f := range fields {
			if v, ok := r[f].(float64); ok {
				o[f], has = v, true
			}
		}
		if has {
			out = append(out, o)
		}
	}
	if n <= 0 {
		return out
	}
	main := fields[0]
	for i, r := range out {
		d, _ := days.Parse(r["day"].(string))
		sum, cnt := 0.0, 0
		for j := i; j >= 0; j-- {
			dj, _ := days.Parse(out[j]["day"].(string))
			if d-dj >= days.Day(n) {
				break
			}
			if v, ok := out[j][main].(float64); ok {
				sum, cnt = sum+v, cnt+1
			}
		}
		if cnt > 0 {
			r[fmt.Sprintf("%s_rolling_%dd", main, n)] = sum / float64(cnt)
		}
	}
	return out
}

func summarize(rows []row, field string) map[string]any {
	var vals []float64
	var first, last row
	for _, r := range rows {
		if v, ok := r[field].(float64); ok {
			vals = append(vals, v)
			if first == nil {
				first = r
			}
			last = r
		}
	}
	if len(vals) == 0 {
		return map[string]any{"field": field, "n": 0}
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return map[string]any{"field": field, "n": len(vals), "mean": sum / float64(len(vals)),
		"min": slices.Min(vals), "max": slices.Max(vals),
		"first": first[field], "first_day": first["day"], "last": last[field], "last_day": last["day"],
		"change": last[field].(float64) - first[field].(float64)}
}

// summary is one period of compare_periods: per-day means plus training totals.
func (t *tools) summary(ctx context.Context, rg days.Range) (map[string]row, error) {
	act, err := t.t.Query.DailyActivity(ctx, rg, false)
	if err != nil {
		return nil, err
	}
	heart, err := t.t.Query.DailyHeart(ctx, rg)
	if err != nil {
		return nil, err
	}
	sleep, err := t.t.Query.Sleep(ctx, rg)
	if err != nil {
		return nil, err
	}
	out := map[string]row{}
	for k, v := range map[string]any{"activity": act, "heart": heart, "sleep": sleep} {
		rows, err := toRows(v)
		if err != nil {
			return nil, err
		}
		out[k] = mean(rows)
	}
	delete(out["activity"], "has_watch_data_days")
	delete(out["activity"], "summary_mismatch_days")

	// Sessions are main-type workouts, as in get_overview and get_training_load; walks and
	// other workouts only count in all_workouts.
	res, err := t.t.Query.Resolved(ctx)
	if err != nil {
		return nil, err
	}
	all, _, err := t.t.Query.Workouts(ctx, query.WorkoutFilter{Range: rg}, 2000)
	if err != nil {
		return nil, err
	}
	var ws []query.WorkoutSummary
	for _, w := range all {
		if w.ActivityType == res.MainType {
			ws = append(ws, w)
		}
	}
	load, err := t.t.Query.TrainingLoad(ctx, rg)
	if err != nil {
		return nil, err
	}
	tr := row{"sessions": float64(len(ws)), "sessions_per_week": float64(len(ws)) * 7 / float64(rg.Len()),
		"all_workouts": float64(len(all))}
	var hours, hr, z3 float64
	var nHR, nZ3 int
	for _, w := range ws {
		if w.DurationS != nil {
			hours += *w.DurationS / 3600
		}
		if w.AvgHRBpm != nil {
			hr, nHR = hr+*w.AvgHRBpm, nHR+1
		}
		if w.PctZ3Plus != nil {
			z3, nZ3 = z3+*w.PctZ3Plus, nZ3+1
		}
	}
	tr["total_h"] = hours
	if len(ws) > 0 {
		tr["avg_duration_min"] = hours * 60 / float64(len(ws))
	}
	if nHR > 0 {
		tr["avg_session_hr_bpm"] = hr / float64(nHR)
	}
	if nZ3 > 0 {
		tr["avg_pct_z3_plus"] = z3 / float64(nZ3)
	}
	total := 0.0
	for _, d := range load.Days {
		total += d.Load
	}
	tr["total_load"] = total
	out["training"] = tr
	return out, nil
}

func compare(a, b row) row {
	out := row{}
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	for k := range keys {
		av, aok := num(a[k])
		bv, bok := num(b[k])
		c := row{}
		if aok {
			c["a"] = av
		}
		if bok {
			c["b"] = bv
		}
		if aok && bok {
			c["delta"] = av - bv
			if bv != 0 {
				c["delta_pct"] = (av - bv) / abs(bv) * 100
			}
		}
		if len(c) > 0 {
			out[k] = c
		}
	}
	return out
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	}
	return 0, false
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
