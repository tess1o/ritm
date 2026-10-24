package mcpserver

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	uriSchema   = "ritm://schema"
	uriCoverage = "ritm://data-coverage"
)

// schemaObjects are the tables and views described by ritm://schema, in reading order.
var schemaObjects = []struct{ name, about string }{
	{"v_daily", "View, one row per local day: activity, heart, sleep and training load joined. Start here."},
	{"v_workouts", "View, one row per workout with derived metrics, RPE and notes, the stable workout_key, and for workouts with a GPS route its start and end point and bounds (start_lat … max_lon)."},
	{"daily_activity", "Per local day: activity totals, rings (ring_* columns), steps_by_hour_json (24 values)."},
	{"daily_heart", "Per local day: HR range, resting/walking HR, HRV, VO2max, SpO2, respiration, wrist temperature."},
	{"daily_sleep", "Per night, keyed by the wake-up day; bedtime_ms/wake_ms are UTC epoch ms."},
	{"daily_load", "Per day: training load (TRIMP), CTL (42-day), ATL (7-day), TSB = CTL - ATL, ACWR, sessions."},
	{"workouts", "Raw workouts from Apple Health; stats_json holds Apple's WorkoutStatistics."},
	{"workout_metrics", "Derived per-workout metrics; zone_s_json = seconds per HR zone [below, z1..z5]."},
	{"workout_annotations", "User RPE (1-10) and notes, keyed by activity_type|start_ms|end_ms."},
	{"records", "Raw discrete samples (heart rate, HRV, VO2max, SpO2, sleep stages, heart events, stand hours…); type is the HealthKit identifier, category_value holds category samples."},
	{"hourly_activity", "Cumulative samples (steps, distance, energy, exercise/stand time, flights) summed per source and local hour."},
	{"activity_summaries", "Apple's daily Activity ring values and goals."},
	{"insights", "Generated insights; evidence_json holds the supporting values."},
	{"sources", "Devices and apps seen in exports (kind: watch, phone or other)."},
}

const schemaIntro = `# Ritm database schema

One SQLite database per user; query_sql reads it (read-only, SELECT/WITH only).

Conventions:
- Units are in column names: _bpm beats/min, _ms milliseconds (HRV) or UTC epoch milliseconds
  (start_ms, end_ms, *_at), _kcal, _m metres, _min minutes, _s seconds, _h hours, _pct percent,
  _c degrees Celsius. pct_z3_plus is a percentage (0-100).
- Epoch times come with tz_offset_min (the sample's own UTC offset). day / local_day are local
  YYYY-MM-DD. Convert with e.g. datetime(start_ms / 1000 + tz_offset_min * 60, 'unixepoch').
- records.type and hourly_activity.type are HealthKit identifiers such as
  HKQuantityTypeIdentifierHeartRate, HKQuantityTypeIdentifierStepCount,
  HKCategoryTypeIdentifierSleepAnalysis.
- hourly_activity keeps every source; for totals prefer the daily_* tables, which apply the
  source priority (Watch first) to avoid double counting.
`

func (t *tools) registerResources(s *mcp.Server) {
	s.AddResource(&mcp.Resource{URI: uriSchema, Name: "schema", Title: "Database schema", MIMEType: "text/markdown",
		Description: "Tables and views with columns and units, for query_sql."},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			text, err := t.schema(ctx)
			if err != nil {
				return nil, err
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uriSchema, MIMEType: "text/markdown", Text: text}}}, nil
		})
	s.AddResource(&mcp.Resource{URI: uriCoverage, Name: "data-coverage", Title: "Data coverage", MIMEType: "application/json",
		Description: "Date range and row count per data type, data sources, and data freshness."},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			cov, err := t.t.Query.Coverage(ctx)
			if err != nil {
				return nil, err
			}
			fresh, err := t.t.Query.Freshness(ctx, t.deps.Cfg.StaleAfter)
			if err != nil {
				return nil, err
			}
			text, err := t.encode(map[string]any{"coverage": cov, "freshness": fresh})
			if err != nil {
				return nil, err
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uriCoverage, MIMEType: "application/json", Text: text}}}, nil
		})
}

func (t *tools) schema(ctx context.Context) (string, error) {
	var b strings.Builder
	b.WriteString(schemaIntro)
	for _, o := range schemaObjects {
		rows, err := t.t.DB.RO.QueryContext(ctx, "SELECT name, type FROM pragma_table_info(?)", o.name)
		if err != nil {
			return "", err
		}
		var cols []string
		for rows.Next() {
			var name, typ string
			if err := rows.Scan(&name, &typ); err != nil {
				rows.Close()
				return "", err
			}
			if typ != "" {
				name += " " + strings.ToLower(typ)
			}
			cols = append(cols, name)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "\n## %s\n%s\n\n%s\n", o.name, o.about, strings.Join(cols, ", "))
	}
	return b.String(), nil
}

func registerPrompts(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{Name: "weekly_review", Title: "Weekly review",
		Description: "Review one week of activity, heart, training and recovery against the usual level.",
		Arguments:   []*mcp.PromptArgument{{Name: "week", Description: "Period of the week (default last_week), e.g. this_week or 2026-10-12..2026-10-18."}}},
		func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			w := arg(req, "week", "last_week")
			return prompt("Weekly review", fmt.Sprintf(`Review my week (%s) from my Ritm data.
1. Call get_overview with period=%q and get_insights with period=%q.
2. Call list_workouts for the same period, and get_sleep if I have sleep data.
3. Compare with the four weeks before using compare_periods (a = this week, b = the 28 days before it).
Write a short review with sections Activity, Heart, Training and Recovery: the key numbers, what
changed versus my usual level and whether I met my goals (get_settings has them). Finish with two or
three concrete, non-medical suggestions for next week.`, w, w, w)), nil
		})
	s.AddPrompt(&mcp.Prompt{Name: "monthly_review", Title: "Monthly review",
		Description: "Review a month: trends, goal attainment and training consistency versus the month before.",
		Arguments:   []*mcp.PromptArgument{{Name: "month", Description: "Period of the month (default last_month), e.g. this_month or 2026-09-01..2026-09-30."}}},
		func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			m := arg(req, "month", "last_month")
			return prompt("Monthly review", fmt.Sprintf(`Review my month (%s) from my Ritm data.
1. Call get_overview with period=%q and get_insights with period=%q.
2. Call compare_periods with a = %q and b = the month before it.
3. Call get_heart_metrics for resting_hr and hrv over the last 90 days with rolling=7 to see the trend,
   and get_training_load for the month.
Summarise the month in a few paragraphs: activity level, heart and recovery trends, training volume,
consistency against my sessions-per-week target, and sleep if available. Point out the most
important change and one thing to keep doing. Keep it factual; this is not medical advice.`, m, m, m, m)), nil
		})
	s.AddPrompt(&mcp.Prompt{Name: "training_review", Title: "Training review",
		Description: "Review recent gym sessions: consistency, intensity, load and recovery.",
		Arguments:   []*mcp.PromptArgument{{Name: "weeks", Description: "How many weeks to review (default 8)."}}},
		func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			n, err := strconv.Atoi(arg(req, "weeks", "8"))
			if err != nil || n < 1 || n > 52 {
				n = 8
			}
			p := fmt.Sprintf("last_%dd", n*7)
			return prompt("Training review", fmt.Sprintf(`Review my training over the last %d weeks from my Ritm data.
1. Call get_training_load with period=%q for load, fitness (CTL), fatigue (ATL), form (TSB), ACWR and
   weekly consistency against my target.
2. Call list_workouts with period=%q, and get_workout for the two most recent sessions (workout types
   counted as sessions, see get_settings) to look at HR zones and recovery.
3. Call get_insights with category=training, and compare_periods (a = the last 4 weeks, b = the 4 weeks
   before) to see the trend.
Report sessions per week versus the target and the current streak, how intensity (time in zone 3+,
average session HR) and duration are trending, whether load is in a sensible range (ACWR around
0.8-1.3), and how next-day resting HR and HRV respond to training. End with practical suggestions.`, n, p, p)), nil
		})
}

func arg(req *mcp.GetPromptRequest, name, def string) string {
	if req.Params != nil {
		if v := strings.TrimSpace(req.Params.Arguments[name]); v != "" {
			return v
		}
	}
	return def
}

func prompt(desc, text string) *mcp.GetPromptResult {
	return &mcp.GetPromptResult{Description: desc,
		Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}}}
}

// paramRank orders tool parameters in the catalog: required first, then the usual reading order.
var paramRank = []string{"metric", "day", "id", "sql", "a", "b", "period", "from", "to", "granularity", "rolling",
	"activity_type", "category", "polarity", "latest_only", "limit"}

func sortParams(ps []CatalogParam) {
	rank := func(n string) int {
		if i := slices.Index(paramRank, n); i >= 0 {
			return i
		}
		return len(paramRank)
	}
	slices.SortStableFunc(ps, func(a, b CatalogParam) int {
		if a.Required != b.Required {
			if a.Required {
				return -1
			}
			return 1
		}
		return rank(a.Name) - rank(b.Name)
	})
}
