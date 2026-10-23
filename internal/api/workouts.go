package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"ritm/internal/days"
	"ritm/internal/query"
	"ritm/internal/tenant"
)

// WorkoutList is a page of workouts, newest first.
type WorkoutList struct {
	From       string                 `json:"from" format:"date" doc:"First local day of the range"`
	To         string                 `json:"to" format:"date" doc:"Last local day of the range"`
	Items      []query.WorkoutSummary `json:"items"`
	Truncated  bool                   `json:"truncated" doc:"More workouts match; fetch them with next_cursor"`
	NextCursor *string                `json:"next_cursor" doc:"Pass as cursor for the next page; null on the last page"`
}

// RouteTrack is one workout's GPS track in compact form.
type RouteTrack struct {
	WorkoutID int64             `json:"workout_id"`
	Key       string            `json:"key" doc:"The workout's stable key (see the workout list)"`
	Bounds    query.RouteBounds `json:"bounds"`
	Track     [][3]float64      `json:"track" doc:"Downsampled points as [lat, lon, t_ms] (t_ms: UTC epoch milliseconds)"`
}

// RouteBatch is a page of GPS tracks, newest workout first.
type RouteBatch struct {
	From       string       `json:"from" format:"date"`
	To         string       `json:"to" format:"date"`
	Items      []RouteTrack `json:"items"`
	Truncated  bool         `json:"truncated" doc:"More workouts with a route match; fetch them with next_cursor"`
	NextCursor *string      `json:"next_cursor" doc:"Pass as cursor for the next page; null on the last page"`
}

// WorkoutQuery selects workouts for the list and the batch-route endpoints (exported: Huma reads
// the parameters of embedded structs only when they are exported).
type WorkoutQuery struct {
	RangeQuery
	Start    string  `query:"start" format:"date-time" example:"2026-10-01T00:00:00Z" doc:"With end: workouts that overlap this exact window (RFC 3339) instead of local days. Not combined with period/from/to"`
	End      string  `query:"end" format:"date-time" example:"2026-10-08T00:00:00Z" doc:"End of the window (exclusive)"`
	Type     string  `query:"type" example:"HKWorkoutActivityTypeFunctionalStrengthTraining" doc:"Only this HealthKit workout type"`
	Category string  `query:"category" enum:"walk,run,hike,cycle,swim,row,paddle,sail,ski,strength,cardio,other" doc:"Only this kind of activity"`
	Near     string  `query:"near" example:"50.4501,30.5234" doc:"lat,lon: only workouts whose GPS route starts within radius_m of this point"`
	RadiusM  float64 `query:"radius_m" minimum:"1" maximum:"100000" default:"200" doc:"Radius for near, in metres"`
	BBox     string  `query:"bbox" example:"30.2,50.3,30.8,50.6" doc:"minLon,minLat,maxLon,maxLat: only workouts whose GPS route bounds intersect this box (minLon > maxLon crosses the antimeridian). Not combined with near"`
	Cursor   string  `query:"cursor" doc:"next_cursor of the previous page"`
}

// workoutFilter resolves q for the caller; without a range the last defaultDays local days are used.
func (s *Server) workoutFilter(ctx context.Context, q WorkoutQuery, defaultDays int) (*tenant.Tenant, query.WorkoutFilter, error) {
	f := query.WorkoutFilter{ActivityType: q.Type, Category: q.Category}
	t, err := s.tenant(ctx)
	if err != nil {
		return nil, f, err
	}
	bad := func(msg string) (*tenant.Tenant, query.WorkoutFilter, error) {
		return nil, f, huma.Error400BadRequest(msg)
	}
	if q.Start != "" || q.End != "" {
		if !q.RangeQuery.empty() {
			return bad("start/end cannot be combined with period, from or to")
		}
		start, err1 := time.Parse(time.RFC3339, q.Start)
		end, err2 := time.Parse(time.RFC3339, q.End)
		if err1 != nil || err2 != nil || !end.After(start) {
			return bad("start and end must both be RFC 3339 times, with end after start")
		}
		f.StartMS, f.EndMS = start.UnixMilli(), end.UnixMilli()
		loc := t.Zone.Location()
		from, _ := days.Parse(start.In(loc).Format(time.DateOnly))
		to, _ := days.Parse(end.Add(-time.Millisecond).In(loc).Format(time.DateOnly))
		f.Range = days.Range{From: from, To: to}
	} else if f.Range, err = q.resolve(t.Query.Today(), defaultDays); err != nil {
		return nil, f, err
	}
	if q.Near != "" && q.BBox != "" {
		return bad("near and bbox cannot be combined")
	}
	if q.Near != "" {
		v, ok := floats(q.Near, 2)
		if !ok || v[0] < -90 || v[0] > 90 || v[1] < -180 || v[1] > 180 {
			return bad("near must be lat,lon")
		}
		f.Near = &query.Near{Lat: v[0], Lon: v[1], RadiusM: q.RadiusM}
	}
	if q.BBox != "" {
		v, ok := floats(q.BBox, 4)
		if !ok || v[1] > v[3] || v[1] < -90 || v[3] > 90 || v[0] < -180 || v[0] > 180 || v[2] < -180 || v[2] > 180 {
			return bad("bbox must be minLon,minLat,maxLon,maxLat")
		}
		f.BBox = &query.RouteBounds{MinLon: v[0], MinLat: v[1], MaxLon: v[2], MaxLat: v[3]}
	}
	if q.Cursor != "" {
		c, err := query.ParseCursor(q.Cursor)
		if err != nil {
			return bad(err.Error())
		}
		f.After = &c
	}
	return t, f, nil
}

// floats parses n comma-separated numbers.
func floats(s string, n int) ([]float64, bool) {
	parts := strings.Split(s, ",")
	if len(parts) != n {
		return nil, false
	}
	out := make([]float64, n)
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

// nextCursor is the cursor after the last item of a truncated page.
func nextCursor(list []query.WorkoutSummary, truncated bool) *string {
	if !truncated || len(list) == 0 {
		return nil
	}
	c := query.CursorAfter(list[len(list)-1]).String()
	return &c
}

// Annotation is the user's RPE and notes for a workout.
type Annotation struct {
	RPE   *int   `json:"rpe" minimum:"1" maximum:"10" doc:"Rating of perceived exertion; null clears it"`
	Notes string `json:"notes" maxLength:"4000"`
}

type workoutsInput struct {
	WorkoutQuery
	Limit int `query:"limit" minimum:"1" maximum:"2000" default:"200"`
}

type routesInput struct {
	WorkoutQuery
	Points int `query:"points" minimum:"2" maximum:"2000" default:"150" doc:"Approximate number of points per track"`
	Limit  int `query:"limit" minimum:"1" maximum:"200" default:"50" doc:"Tracks per page"`
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
	register(s, dataRead, huma.Operation{OperationID: "listWorkouts", Method: http.MethodGet, Path: "/workouts", Tags: tags,
		Summary: "Workouts", Description: "Workouts with key metrics and route location, newest first (default the last 90 days). " +
			"Select by local days (period/from/to) or an exact window (start/end), and filter by type, category, or where the route is " +
			"(near or bbox). Long lists come in pages: pass next_cursor as cursor.", Errors: []int{http.StatusBadRequest}},
		func(ctx context.Context, in *workoutsInput) (*body[WorkoutList], error) {
			t, f, err := s.workoutFilter(ctx, in.WorkoutQuery, 90)
			if err != nil {
				return nil, err
			}
			list, truncated, err := t.Query.Workouts(ctx, f, in.Limit)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(WorkoutList{From: f.Range.From.String(), To: f.Range.To.String(), Items: list, Truncated: truncated,
				NextCursor: nextCursor(list, truncated)}), nil
		})

	register(s, dataRead, huma.Operation{OperationID: "listWorkoutRoutes", Method: http.MethodGet, Path: "/workouts/routes", Tags: tags,
		Summary: "GPS tracks of many workouts", Description: "Downsampled tracks of the workouts with a GPS route, newest first, as compact " +
			"[lat, lon, t_ms] arrays (for drawing many routes on a map). Takes the same filters as the workout list. " +
			"For pace and elevation use the single-workout route.", Errors: []int{http.StatusBadRequest}},
		func(ctx context.Context, in *routesInput) (*body[RouteBatch], error) {
			t, f, err := s.workoutFilter(ctx, in.WorkoutQuery, 90)
			if err != nil {
				return nil, err
			}
			f.RouteOnly = true
			list, truncated, err := t.Query.Workouts(ctx, f, in.Limit)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			ids := make([]int64, len(list))
			for i, w := range list {
				ids[i] = w.ID
			}
			tracks, err := t.Query.Tracks(ctx, ids, in.Points)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			out := RouteBatch{From: f.Range.From.String(), To: f.Range.To.String(), Items: []RouteTrack{}, Truncated: truncated,
				NextCursor: nextCursor(list, truncated)}
			for _, w := range list {
				rt := tracks[w.ID]
				if rt == nil {
					continue
				}
				tr := RouteTrack{WorkoutID: w.ID, Key: w.Key, Bounds: rt.Bounds, Track: make([][3]float64, len(rt.Track))}
				for i, p := range rt.Track {
					tr.Track[i] = [3]float64{p.Lat, p.Lon, float64(p.TMS)}
				}
				out.Items = append(out.Items, tr)
			}
			return ok(out), nil
		})

	register(s, dataRead, huma.Operation{OperationID: "listWorkoutTypes", Method: http.MethodGet, Path: "/workout-types", Tags: tags,
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

	register(s, dataRead, huma.Operation{OperationID: "getWorkout", Method: http.MethodGet, Path: "/workouts/{id}", Tags: tags,
		Summary: "Workout detail", Description: "Metrics, time in HR zones, events and pauses, Apple statistics, RPE and notes, and the previous workout of the same type.",
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

	register(s, dataRead, huma.Operation{OperationID: "getWorkoutHeartRate", Method: http.MethodGet, Path: "/workouts/{id}/heart-rate", Tags: tags,
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

	register(s, dataRead, huma.Operation{OperationID: "getWorkoutRoute", Method: http.MethodGet, Path: "/workouts/{id}/route", Tags: tags,
		Summary: "Workout GPS route", Description: "The downsampled GPS track with pace, distance, climb and bounds. 404 when the workout has no route.",
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
	register(s, dataRead, huma.Operation{OperationID: "getTrainingLoad", Method: http.MethodGet, Path: "/training/load", Tags: tags,
		Summary: "Training load", Description: "Daily load with fitness (CTL), fatigue (ATL), form (TSB) and ACWR (default the last 91 days).",
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

	register(s, dataRead, huma.Operation{OperationID: "getTrainingWeekly", Method: http.MethodGet, Path: "/training/weekly", Tags: tags,
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
