package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"ritm/internal/query"
)

// WorkoutList is a page of workouts, newest first.
type WorkoutList struct {
	From      string                 `json:"from" format:"date"`
	To        string                 `json:"to" format:"date"`
	Items     []query.WorkoutSummary `json:"items"`
	Truncated bool                   `json:"truncated" doc:"More workouts match; raise limit or narrow the range"`
}

// Annotation is the user's RPE and notes for a workout (§7.5).
type Annotation struct {
	RPE   *int   `json:"rpe" minimum:"1" maximum:"10" doc:"Rating of perceived exertion; null clears it"`
	Notes string `json:"notes" maxLength:"4000"`
}

type workoutsInput struct {
	RangeQuery
	Type  string `query:"type" example:"HKWorkoutActivityTypeFunctionalStrengthTraining" doc:"Only this HealthKit workout type"`
	Limit int    `query:"limit" minimum:"1" maximum:"2000" default:"200"`
}

type workoutSeriesInput struct {
	IDPath
	Points int `query:"points" minimum:"10" maximum:"2000" default:"300" doc:"Approximate number of points"`
}

type routeInput struct {
	IDPath
	Points int `query:"points" minimum:"2" maximum:"2000" default:"500" doc:"Approximate number of track points"`
}

type annotationInput struct {
	IDPath
	Body Annotation
}

func (s *Server) registerWorkouts() {
	tags := []string{"Workouts"}
	register(s, sessionOnly, huma.Operation{OperationID: "listWorkouts", Method: http.MethodGet, Path: "/workouts", Tags: tags,
		Summary: "Workouts", Description: "Workouts with key metrics, newest first (default the last 90 days).", Errors: []int{http.StatusBadRequest}},
		func(ctx context.Context, in *workoutsInput) (*body[WorkoutList], error) {
			t, r, err := s.rangeOf(ctx, in.RangeQuery, 90)
			if err != nil {
				return nil, err
			}
			list, truncated, err := t.Query.Workouts(ctx, query.WorkoutFilter{Range: r, ActivityType: in.Type}, in.Limit)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(WorkoutList{From: r.From.String(), To: r.To.String(), Items: list, Truncated: truncated}), nil
		})

	register(s, sessionOnly, huma.Operation{OperationID: "listWorkoutTypes", Method: http.MethodGet, Path: "/workout-types", Tags: tags,
		Summary: "Workout types", Description: "Activity types present in the data with their counts, most frequent first."},
		func(ctx context.Context, _ *struct{}) (*body[Items[query.WorkoutType]], error) {
			t, err := s.tenant(ctx)
			if err != nil {
				return nil, err
			}
			list, err := t.Query.WorkoutTypes(ctx)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(Items[query.WorkoutType]{Items: list}), nil
		})

	register(s, sessionOnly, huma.Operation{OperationID: "getWorkout", Method: http.MethodGet, Path: "/workouts/{id}", Tags: tags,
		Summary: "Workout detail", Description: "Metrics, time in HR zones, events and pauses, Apple statistics, RPE and notes, and the previous workout of the same type (§7.5).",
		Errors: []int{http.StatusNotFound}},
		func(ctx context.Context, in *IDPath) (*body[*query.WorkoutDetail], error) {
			t, err := s.tenant(ctx)
			if err != nil {
				return nil, err
			}
			d, err := t.Query.Workout(ctx, in.ID)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(d), nil
		})

	register(s, sessionOnly, huma.Operation{OperationID: "getWorkoutHeartRate", Method: http.MethodGet, Path: "/workouts/{id}/heart-rate", Tags: tags,
		Summary: "Workout heart rate", Description: "The HR curve from the start to 2 minutes after the end, bucketed to about `points` points.",
		Errors: []int{http.StatusNotFound}},
		func(ctx context.Context, in *workoutSeriesInput) (*body[Items[query.HRPoint]], error) {
			t, err := s.tenant(ctx)
			if err != nil {
				return nil, err
			}
			hr, err := t.Query.WorkoutHR(ctx, in.ID, in.Points)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(Items[query.HRPoint]{Items: hr}), nil
		})

	register(s, sessionOnly, huma.Operation{OperationID: "getWorkoutRoute", Method: http.MethodGet, Path: "/workouts/{id}/route", Tags: tags,
		Summary: "Workout GPS route", Description: "The downsampled GPS track with pace, distance, climb and bounds (PAR-9). 404 when the workout has no route.",
		Errors: []int{http.StatusNotFound}},
		func(ctx context.Context, in *routeInput) (*body[*query.Route], error) {
			t, err := s.tenant(ctx)
			if err != nil {
				return nil, err
			}
			rt, err := t.Query.Route(ctx, in.ID, in.Points)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(rt), nil
		})

	register(s, sessionOnly, huma.Operation{OperationID: "setWorkoutAnnotation", Method: http.MethodPut, Path: "/workouts/{id}/annotation", Tags: tags,
		Summary: "Set RPE and notes", Description: "Stores RPE (1–10) and notes; they survive re-imports. RPE feeds the sRPE load, so derived data is recomputed from the workout's day.",
		Errors: []int{http.StatusNotFound}},
		func(ctx context.Context, in *annotationInput) (*body[*query.WorkoutDetail], error) {
			t, err := s.tenant(ctx)
			if err != nil {
				return nil, err
			}
			if err := t.Query.SetAnnotation(ctx, in.ID, in.Body.RPE, in.Body.Notes); err != nil {
				return nil, s.problem(ctx, err)
			}
			d, err := t.Query.Workout(ctx, in.ID)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			if err := t.Run(func(ctx context.Context) error { return t.Derive.Recompute(ctx, d.Day) }); err != nil {
				return nil, s.problem(ctx, err)
			}
			if d, err = t.Query.Workout(ctx, in.ID); err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(d), nil
		})

	tags = []string{"Training"}
	register(s, sessionOnly, huma.Operation{OperationID: "getTrainingLoad", Method: http.MethodGet, Path: "/training/load", Tags: tags,
		Summary: "Training load", Description: "Daily load with fitness (CTL), fatigue (ATL), form (TSB) and ACWR (default the last 91 days, §7.6).",
		Errors: []int{http.StatusBadRequest}},
		func(ctx context.Context, in *RangeQuery) (*body[*query.TrainingLoad], error) {
			t, r, err := s.rangeOf(ctx, *in, 91)
			if err != nil {
				return nil, err
			}
			l, err := t.Query.TrainingLoad(ctx, r)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(l), nil
		})

	register(s, sessionOnly, huma.Operation{OperationID: "getTrainingWeekly", Method: http.MethodGet, Path: "/training/weekly", Tags: tags,
		Summary: "Training consistency", Description: "Main-type sessions per ISO week vs the target, streaks, zones per week, session HR, monthly volume and totals (default the last 91 days).",
		Errors: []int{http.StatusBadRequest}},
		func(ctx context.Context, in *RangeQuery) (*body[*query.Weekly], error) {
			t, r, err := s.rangeOf(ctx, *in, 91)
			if err != nil {
				return nil, err
			}
			w, err := t.Query.TrainingWeekly(ctx, r)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(w), nil
		})
}
