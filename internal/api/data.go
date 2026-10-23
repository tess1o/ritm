package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"ritm/internal/days"
	"ritm/internal/insights"
	"ritm/internal/period"
	"ritm/internal/query"
	"ritm/internal/tenant"
)

// RangeQuery selects a range of local days; embedded in every ranged read.
type RangeQuery struct {
	Period string `query:"period" example:"last_30d" doc:"today, yesterday, last_7d, last_14d, last_30d, last_90d, last_365d, last_<n>d, this_week, last_week, this_month, last_month, ytd, YYYY-MM-DD or FROM..TO. Overrides from/to."`
	From   string `query:"from" example:"2026-09-01" doc:"First day (YYYY-MM-DD, or a period name meaning its first day)"`
	To     string `query:"to" example:"2026-09-30" doc:"Last day (YYYY-MM-DD, or a period name meaning its last day; default today)"`
}

func (q RangeQuery) empty() bool { return q.Period == "" && q.From == "" && q.To == "" }

// resolve turns the query into days; without any parameter it is the trailing defaultDays.
func (q RangeQuery) resolve(today days.Day, defaultDays int) (days.Range, error) {
	var (
		r   days.Range
		err error
	)
	if q.Period != "" {
		r, err = period.Resolve(q.Period, today)
	} else {
		r, err = period.FromTo(q.From, q.To, today, defaultDays)
	}
	if err != nil {
		return r, huma.Error400BadRequest(err.Error())
	}
	return r, nil
}

// rangeOf resolves q for the caller and returns their tenant too.
func (s *Server) rangeOf(ctx context.Context, q RangeQuery, defaultDays int) (*tenant.Tenant, days.Range, error) {
	t, err := s.tenant(ctx)
	if err != nil {
		return nil, days.Range{}, err
	}
	r, err := q.resolve(t.Query.Today(), defaultDays)
	return t, r, err
}

func ranged[T any](r days.Range, items []T) *body[RangeItems[T]] {
	return ok(RangeItems[T]{From: r.From.String(), To: r.To.String(), Items: items})
}

// FreshnessSummary is the data freshness shown in the app chrome.
type FreshnessSummary struct {
	query.Freshness
	HasSleepData    bool       `json:"has_sleep_data" doc:"Any night of sleep is stored (the Sleep page is shown)"`
	DataVersion     int64      `json:"data_version" doc:"Goes up with every change to the data (imports, recomputes, settings, RPE and notes); a client can compare it to tell whether its cache is current"`
	WorkoutsFirstAt *time.Time `json:"workouts_first_at" doc:"Start of the first workout; null without workouts"`
	WorkoutsLastAt  *time.Time `json:"workouts_last_at" doc:"End of the last workout; null without workouts"`
}

// DataCalendar marks the days in a range that have data, for the date pickers.
type DataCalendar struct {
	From     string              `json:"from"`
	To       string              `json:"to"`
	FirstDay *string             `json:"first_day" doc:"First day with daily activity or a workout, across all data"`
	LastDay  *string             `json:"last_day" doc:"Last day with daily activity or a workout, across all data"`
	Items    []query.CalendarDay `json:"items" doc:"Days in the range with daily activity or a workout; days without are left out"`
}

// InsightList is a page of insights.
type InsightList struct {
	Items     []insights.Insight `json:"items"`
	Truncated bool               `json:"truncated" doc:"More insights match; raise limit or narrow the range"`
}

type insightsInput struct {
	RangeQuery
	Category string `query:"category" enum:"activity,heart,training,recovery"`
	Polarity string `query:"polarity" enum:"positive,neutral,attention"`
	Kind     string `query:"kind" enum:"daily,weekly,monthly,long_term" doc:"Period kind"`
	Latest   bool   `query:"latest" doc:"Only the most recent period per rule"`
	Limit    int    `query:"limit" minimum:"1" maximum:"1000" default:"200"`
}

type dayInput struct {
	Day     string `path:"day" example:"2026-10-21" doc:"YYYY-MM-DD, today or yesterday"`
	BucketS int    `query:"bucket_s" minimum:"60" maximum:"3600" default:"300" doc:"Heart-rate bucket width in seconds"`
}

type activityInput struct {
	RangeQuery
	Hours bool `query:"hours" default:"true" doc:"Include steps_by_hour (24 values per day)"`
}

// RecoveryReport compares the mornings after training days with those after rest days.
type RecoveryReport struct {
	From string `json:"from" format:"date"`
	To   string `json:"to" format:"date"`
	query.Recovery
}

func (s *Server) registerData() {
	register(s, dataRead, huma.Operation{OperationID: "getOverview", Method: http.MethodGet, Path: "/overview", Tags: []string{"Dashboard"},
		Summary: "Period overview", Description: "Activity, heart, sleep and training averages for a period (default the last 7 days) with deltas vs a baseline " +
			"(the 28 days before for periods up to 28 days, else the preceding equal period), latest insights, scores and freshness.",
		Errors: []int{http.StatusBadRequest}},
		func(ctx context.Context, in *RangeQuery) (*body[*query.Overview], error) {
			t, r, err := s.rangeOf(ctx, *in, 7)
			if err != nil {
				return nil, err
			}
			o, err := t.Query.Overview(ctx, r, s.Cfg.StaleAfter)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(o), nil
		})

	register(s, dataRead, huma.Operation{OperationID: "getFreshness", Method: http.MethodGet, Path: "/freshness", Tags: []string{"Dashboard"},
		Summary: "Data freshness", Description: "When data last arrived and whether it is stale (same logic as the monitor endpoint)."},
		func(ctx context.Context, _ *struct{}) (*body[FreshnessSummary], error) {
			t, err := s.tenant(ctx)
			if err != nil {
				return nil, err
			}
			f, err := t.Query.Freshness(ctx, s.Cfg.StaleAfter)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			sleep, err := t.Query.HasSleep(ctx)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			version, err := t.Query.DataVersion(ctx)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			first, last, err := t.Query.WorkoutSpan(ctx)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(FreshnessSummary{Freshness: f, HasSleepData: sleep, DataVersion: version, WorkoutsFirstAt: first, WorkoutsLastAt: last}), nil
		})

	register(s, dataRead, huma.Operation{OperationID: "listInsights", Method: http.MethodGet, Path: "/insights", Tags: []string{"Insights"},
		Summary: "Insights", Description: "Rule-based insights, most recent periods first, attention before positive. Without a range, all stored periods.",
		Errors: []int{http.StatusBadRequest}},
		func(ctx context.Context, in *insightsInput) (*body[InsightList], error) {
			t, err := s.tenant(ctx)
			if err != nil {
				return nil, err
			}
			f := insights.Filter{Category: in.Category, Polarity: in.Polarity, PeriodKind: in.Kind, LatestOnly: in.Latest, Limit: in.Limit + 1}
			if !in.RangeQuery.empty() {
				r, err := in.RangeQuery.resolve(t.Query.Today(), 30)
				if err != nil {
					return nil, err
				}
				f.From, f.To = r.From.String(), r.To.String()
			}
			list, err := t.Query.Insights(ctx, f)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			out := InsightList{Items: list}
			if len(list) > in.Limit {
				out.Items, out.Truncated = list[:in.Limit], true
			}
			return ok(out), nil
		})

	register(s, dataRead, huma.Operation{OperationID: "getDay", Method: http.MethodGet, Path: "/days/{day}", Tags: []string{"Activity"},
		Summary: "One day in detail", Description: "24-hour timeline: daily activity with hourly steps, heart summary and HR curve, stood hours, workouts, sleep and its stages.",
		Errors: []int{http.StatusBadRequest}},
		func(ctx context.Context, in *dayInput) (*body[*query.DayDetail], error) {
			t, err := s.tenant(ctx)
			if err != nil {
				return nil, err
			}
			d, err := period.Day(in.Day, t.Query.Today())
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			dd, err := t.Query.DayDetail(ctx, d, in.BucketS)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(dd), nil
		})

	register(s, dataRead, huma.Operation{OperationID: "getCalendar", Method: http.MethodGet, Path: "/calendar", Tags: []string{"Activity"},
		Summary: "Days with data", Description: "Which days have daily activity or workouts (default the last 366 days), plus the first and last day with data.",
		Errors: []int{http.StatusBadRequest}},
		func(ctx context.Context, in *RangeQuery) (*body[DataCalendar], error) {
			t, r, err := s.rangeOf(ctx, *in, 366)
			if err != nil {
				return nil, err
			}
			list, err := t.Query.Calendar(ctx, r)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			first, last, err := t.Query.DataExtent(ctx)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(DataCalendar{From: r.From.String(), To: r.To.String(), FirstDay: first, LastDay: last, Items: list}), nil
		})

	register(s, dataRead, huma.Operation{OperationID: "listDailyActivity", Method: http.MethodGet, Path: "/activity/daily", Tags: []string{"Activity"},
		Summary: "Daily activity", Description: "Per day (default the last 30 days): steps, distance, calories, flights, exercise and stand, rings, sedentary stretch.",
		Errors: []int{http.StatusBadRequest}},
		func(ctx context.Context, in *activityInput) (*body[RangeItems[query.DailyActivity]], error) {
			t, r, err := s.rangeOf(ctx, in.RangeQuery, 30)
			if err != nil {
				return nil, err
			}
			list, err := t.Query.DailyActivity(ctx, r, in.Hours)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ranged(r, list), nil
		})

	register(s, dataRead, huma.Operation{OperationID: "listDailyHeart", Method: http.MethodGet, Path: "/heart/daily", Tags: []string{"Heart"},
		Summary: "Daily heart metrics", Description: "Per day (default the last 90 days): HR range, resting and walking HR, HRV, VO2max, SpO2, respiration, wrist temperature.",
		Errors: []int{http.StatusBadRequest}},
		func(ctx context.Context, in *RangeQuery) (*body[RangeItems[query.DailyHeart]], error) {
			t, r, err := s.rangeOf(ctx, *in, 90)
			if err != nil {
				return nil, err
			}
			list, err := t.Query.DailyHeart(ctx, r)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ranged(r, list), nil
		})

	register(s, dataRead, huma.Operation{OperationID: "listHeartEvents", Method: http.MethodGet, Path: "/heart/events", Tags: []string{"Heart"},
		Summary: "Heart notifications", Description: "High / low heart rate and irregular rhythm notifications, newest first (default the last 365 days).",
		Errors: []int{http.StatusBadRequest}},
		func(ctx context.Context, in *RangeQuery) (*body[RangeItems[query.HeartEvent]], error) {
			t, r, err := s.rangeOf(ctx, *in, 365)
			if err != nil {
				return nil, err
			}
			list, err := t.Query.HeartEvents(ctx, r)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ranged(r, list), nil
		})

	register(s, dataRead, huma.Operation{OperationID: "listSleepNights", Method: http.MethodGet, Path: "/sleep/nights", Tags: []string{"Sleep"},
		Summary: "Nightly sleep", Description: "One row per night, keyed by the wake-up day (default the last 30 days).",
		Errors: []int{http.StatusBadRequest}},
		func(ctx context.Context, in *RangeQuery) (*body[RangeItems[query.SleepNight]], error) {
			t, r, err := s.rangeOf(ctx, *in, 30)
			if err != nil {
				return nil, err
			}
			list, err := t.Query.Sleep(ctx, r)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ranged(r, list), nil
		})

	register(s, dataRead, huma.Operation{OperationID: "getSleepRecovery", Method: http.MethodGet, Path: "/sleep/recovery", Tags: []string{"Sleep"},
		Summary: "Next-day recovery", Description: "Mean resting HR and HRV on mornings after training days vs after rest days (default the last 30 days).",
		Errors: []int{http.StatusBadRequest}},
		func(ctx context.Context, in *RangeQuery) (*body[RecoveryReport], error) {
			t, r, err := s.rangeOf(ctx, *in, 30)
			if err != nil {
				return nil, err
			}
			rec, err := t.Query.Recovery(ctx, r)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(RecoveryReport{From: r.From.String(), To: r.To.String(), Recovery: *rec}), nil
		})

	register(s, dataRead, huma.Operation{OperationID: "getCoverage", Method: http.MethodGet, Path: "/coverage", Tags: []string{"Settings"},
		Summary: "Data coverage", Description: "Date range and row count per data type, data sources and database size."},
		func(ctx context.Context, _ *struct{}) (*body[*query.Coverage], error) {
			t, err := s.tenant(ctx)
			if err != nil {
				return nil, err
			}
			c, err := t.Query.Coverage(ctx)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(c), nil
		})
}
