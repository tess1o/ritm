package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"ritm/internal/auth"
	"ritm/internal/query"
	"ritm/internal/synth"
)

type workoutPage struct {
	Items      []query.WorkoutSummary
	Truncated  bool
	NextCursor *string `json:"next_cursor"`
}

// The workout list for API clients: exact windows, cursor pages, categories, route location and
// geo filters, and the batch of tracks.
func TestWorkoutQueries(t *testing.T) {
	ctx := context.Background()
	o := synth.Options{Start: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), Days: 28, Loc: time.UTC, Seed: 1,
		HRIntervalSec: 1800, WorkoutHRIntervalSec: 60, Walks: true}
	s := newTestServer(t, true, o.Start.AddDate(0, 0, o.Days).Add(10*time.Hour))
	key := s.createKey(s.register("alice"), "GeoPulse", auth.ScopeRead)
	info, _ := s.auth.UserByName(ctx, "alice")
	tn, err := s.tenants.Get(ctx, info.User)
	if err != nil {
		t.Fatal(err)
	}
	s.importSynth(tn, o)
	list := func(q string) workoutPage {
		t.Helper()
		var p workoutPage
		if code := s.bearer("/api/v1/workouts?"+q, key, &p); code != http.StatusOK {
			t.Fatalf("GET /workouts?%s: %d", q, code)
		}
		return p
	}

	all := list("period=last_30d")
	var walks []query.WorkoutSummary
	for _, w := range all.Items {
		if w.Key != w.ActivityType+"|"+itoa(w.StartMS)+"|"+itoa(w.EndMS) {
			t.Fatalf("key %q", w.Key)
		}
		if w.ActivityType == "HKWorkoutActivityTypeWalking" {
			walks = append(walks, w)
			if w.Category != "walk" || !w.DistanceBased || w.Route == nil || w.Route.Points != synth.WalkPoints ||
				w.Route.Start.Lat != 50.45 || w.Route.Bounds.MaxLat <= w.Route.Start.Lat {
				t.Fatalf("walk: %+v route %+v", w, w.Route)
			}
		} else if w.Category != "strength" || w.Route != nil {
			t.Fatalf("gym workout: category %s, route %+v", w.Category, w.Route)
		}
	}
	if len(walks) != 4 || len(all.Items) <= len(walks) {
		t.Fatalf("%d workouts, %d walks", len(all.Items), len(walks))
	}

	// Cursor pages cover the list exactly once, in order.
	var paged []int64
	q := "period=last_30d&limit=3"
	for range 20 {
		p := list(q)
		for _, w := range p.Items {
			paged = append(paged, w.ID)
		}
		if p.NextCursor == nil {
			break
		}
		q = "period=last_30d&limit=3&cursor=" + *p.NextCursor
	}
	var ids []int64
	for _, w := range all.Items {
		ids = append(ids, w.ID)
	}
	if !slices.Equal(paged, ids) {
		t.Fatalf("pages %v, want %v", paged, ids)
	}

	// An exact window matches the workouts overlapping it.
	w := walks[1]
	win := func(from, to int64) string {
		return "start=" + url.QueryEscape(time.UnixMilli(from).UTC().Format(time.RFC3339)) +
			"&end=" + url.QueryEscape(time.UnixMilli(to).UTC().Format(time.RFC3339))
	}
	if p := list(win(w.EndMS-60_000, w.EndMS+3_600_000)); len(p.Items) != 1 || p.Items[0].ID != w.ID {
		t.Fatalf("window over the walk's end: %v", p.Items)
	}
	if p := list(win(w.EndMS+1000, w.EndMS+60_000)); len(p.Items) != 0 {
		t.Fatalf("window after the walk: %v", p.Items)
	}

	for q, want := range map[string]int{
		"period=last_30d&category=walk":                         4,
		"period=last_30d&category=other":                        0,
		"period=last_30d&near=50.45,30.52&radius_m=100":         4,
		"period=last_30d&near=50.46,30.52&radius_m=500":         0, // 1.1 km north of the start
		"period=last_30d&bbox=30.5,50.44,30.6,50.5":             4,
		"period=last_30d&bbox=31,50.44,32,50.5":                 0,
		"period=last_30d&bbox=170,50.44,30.6,50.5":              4, // crosses the antimeridian
		"period=last_30d&near=50.45,30.52&radius_m=100&limit=1": 1,
	} {
		if p := list(q); len(p.Items) != want {
			t.Errorf("%s: %d workouts, want %d", q, len(p.Items), want)
		}
	}
	for _, q := range []string{"start=2026-01-05T00:00:00Z", "start=2026-01-05T00:00:00Z&end=2026-01-06T00:00:00Z&period=last_7d",
		"near=50,30&bbox=1,2,3,4", "near=95,30", "bbox=1,5,3,4", "cursor=nope"} {
		if code := s.bearer("/api/v1/workouts?"+q, key, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", q, code)
		}
	}

	// Batch tracks: walks only, compact points, paged like the list.
	var batch struct {
		Items []struct {
			WorkoutID int64 `json:"workout_id"`
			Key       string
			Track     [][3]float64
		}
		NextCursor *string `json:"next_cursor"`
	}
	if code := s.bearer("/api/v1/workouts/routes?period=last_30d&points=20&limit=3", key, &batch); code != http.StatusOK {
		t.Fatalf("routes: %d", code)
	}
	if len(batch.Items) != 3 || batch.NextCursor == nil || batch.Items[0].WorkoutID != walks[0].ID || batch.Items[0].Key != walks[0].Key {
		t.Fatalf("routes page: %+v", batch)
	}
	if tr := batch.Items[0].Track; len(tr) < 20 || len(tr) > 22 || tr[0][0] != 50.45 || int64(tr[0][2]) < walks[0].StartMS-60_000 {
		t.Fatalf("track: %d points, first %v", len(tr), tr[0])
	}
}

// Data reads carry an ETag that changes with the data; a matching If-None-Match gets 304.
func TestETagRevalidation(t *testing.T) {
	ctx := context.Background()
	o := synth.Options{Start: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), Days: 7, Loc: time.UTC, Seed: 1,
		HRIntervalSec: 1800, WorkoutHRIntervalSec: 60}
	s := newTestServer(t, true, o.Start.AddDate(0, 0, o.Days).Add(10*time.Hour))
	alice := s.register("alice")
	key := s.createKey(alice, "GeoPulse", auth.ScopeRead)
	info, _ := s.auth.UserByName(ctx, "alice")
	tn, err := s.tenants.Get(ctx, info.User)
	if err != nil {
		t.Fatal(err)
	}
	s.importSynth(tn, o)
	get := func(path, etag string) (int, string, []byte) {
		h := map[string]string{"Authorization": "Bearer " + key}
		if etag != "" {
			h["If-None-Match"] = etag
		}
		w := s.raw("GET", path, nil, h)
		return w.Code, w.Header().Get("ETag"), w.Body.Bytes()
	}
	const path = "/api/v1/workouts?period=last_7d"
	code, tag, body := get(path, "")
	if code != http.StatusOK || !strings.HasPrefix(tag, `W/"`) {
		t.Fatalf("first GET: %d, ETag %q", code, tag)
	}
	if code, _, b := get(path, tag); code != http.StatusNotModified || len(b) != 0 {
		t.Fatalf("revalidate: %d, %d bytes", code, len(b))
	}
	if _, other, _ := get("/api/v1/workouts?period=last_14d", ""); other == tag {
		t.Fatal("another URL got the same ETag")
	}
	var page workoutPage
	json.Unmarshal(body, &page)

	var before map[string]any
	s.bearer("/api/v1/freshness", key, &before)
	rpe := 6
	if code, _ := s.do("PUT", "/api/v1/workouts/"+itoa(page.Items[0].ID)+"/annotation", alice, Annotation{RPE: &rpe}, nil); code != http.StatusOK {
		t.Fatalf("annotate: %d", code)
	}
	code, tag2, _ := get(path, tag)
	if code != http.StatusOK || tag2 == tag {
		t.Fatalf("after a change: %d, ETag %q (was %q)", code, tag2, tag)
	}
	var after map[string]any
	s.bearer("/api/v1/freshness", key, &after)
	if after["data_version"].(float64) <= before["data_version"].(float64) || after["workouts_first_at"] == nil {
		t.Fatalf("freshness before %v, after %v", before, after)
	}
	if code, tag, _ := get("/api/v1/freshness", ""); code != http.StatusOK || tag != "" {
		t.Fatalf("freshness: %d, ETag %q (it depends on the clock)", code, tag)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
