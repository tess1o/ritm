package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"ritm/internal/auth"
	"ritm/internal/synth"
)

type bearerTransport struct{ key string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.key)
	return http.DefaultTransport.RoundTrip(r)
}

func mcpClient(t *testing.T, url, key string) *mcp.ClientSession {
	t.Helper()
	tr := &mcp.StreamableClientTransport{Endpoint: url + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{key}},
		DisableStandaloneSSE: true}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(context.Background(), tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// call runs a tool and decodes its JSON text; it fails the test on tool errors unless wantErr.
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, wantErr bool) map[string]any {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError != wantErr {
		t.Fatalf("%s(%v): IsError=%v: %s", name, args, res.IsError, text)
	}
	if wantErr {
		return map[string]any{"error": text}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%s: decode %q: %v", name, text, err)
	}
	return out
}

func TestMCP(t *testing.T) {
	ctx := context.Background()
	o := synth.Options{Start: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), Days: 42, Loc: time.UTC, Seed: 1, Sleep: true,
		HRIntervalSec: 900, WorkoutHRIntervalSec: 30, Walks: true}
	s := newTestServer(t, true, o.Start.AddDate(0, 0, o.Days).Add(10*time.Hour))
	alice, bob := s.register("alice"), s.register("bob")
	aliceKey, bobKey := s.createKey(alice, "Claude", auth.ScopeRead), s.createKey(bob, "Claude", auth.ScopeRead)
	monKey := s.createKey(alice, "Kuma", auth.ScopeMonitor)
	info, _ := s.auth.UserByName(ctx, "alice")
	tn, err := s.tenants.Get(ctx, info.User)
	if err != nil {
		t.Fatal(err)
	}
	s.importSynth(tn, o)

	srv := httptest.NewServer(s.h)
	defer srv.Close()

	// Auth: bearer key with the read scope.
	initBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}`)
	for key, want := range map[string]int{"": http.StatusUnauthorized, "ritm_wrong": http.StatusUnauthorized, monKey: http.StatusForbidden} {
		req, _ := http.NewRequest("POST", srv.URL+"/mcp", bytes.NewReader(initBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("key %q: %d, want %d", key, resp.StatusCode, want)
		}
	}

	cs := mcpClient(t, srv.URL, aliceKey)
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
		if tl.Annotations == nil || !tl.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read-only", tl.Name)
		}
	}
	slices.Sort(names)
	want := []string{"compare_periods", "get_daily_activity", "get_day_detail", "get_heart_metrics", "get_insights", "get_overview",
		"get_settings", "get_sleep", "get_training_load", "get_workout", "list_workouts", "query_sql"}
	if !slices.Equal(names, want) {
		t.Fatalf("tools %v", names)
	}

	ov := call(t, cs, "get_overview", map[string]any{"period": "last_7d"}, false)
	if _, ok := ov["steps_per_day"]; !ok {
		t.Fatalf("overview: %v", ov)
	}
	da := call(t, cs, "get_daily_activity", map[string]any{"period": "2026-01-05..2026-01-18"}, false)
	if rows := da["rows"].([]any); len(rows) != 14 || da["granularity"] != "day" {
		t.Fatalf("daily activity: %d rows, %v", len(rows), da["granularity"])
	}
	wk := call(t, cs, "get_daily_activity", map[string]any{"from": "2026-01-05", "to": "2026-02-15", "granularity": "week"}, false)
	if rows := wk["rows"].([]any); len(rows) != 6 || rows[0].(map[string]any)["days"].(float64) != 7 {
		t.Fatalf("weekly activity: %v", wk["rows"])
	}
	capped := call(t, cs, "get_daily_activity", map[string]any{"from": "2026-01-05", "to": "2026-02-15", "granularity": "day", "limit": 5}, false)
	if len(capped["rows"].([]any)) != 5 || capped["truncated"] != true {
		t.Fatalf("limit: %v", capped)
	}
	hm := call(t, cs, "get_heart_metrics", map[string]any{"metric": "resting_hr", "period": "last_30d", "rolling": 7}, false)
	if sum := hm["summary"].(map[string]any); sum["n"].(float64) == 0 {
		t.Fatalf("heart metrics: %v", hm)
	}
	if !strings.Contains(mustJSON(hm["rows"]), "resting_hr_bpm_rolling_7d") {
		t.Fatalf("no rolling mean: %v", hm["rows"])
	}
	call(t, cs, "get_heart_metrics", map[string]any{"metric": "blood_pressure"}, true)
	call(t, cs, "get_sleep", map[string]any{"period": "last_14d"}, false)

	wl := call(t, cs, "list_workouts", map[string]any{"period": "last_90d", "activity_type": "FunctionalStrengthTraining"}, false)
	ws := wl["workouts"].([]any)
	if len(ws) == 0 {
		t.Fatal("no workouts")
	}
	w0 := ws[0].(map[string]any)
	if _, ok := w0["start_ms"]; ok || w0["start"] == nil {
		t.Fatalf("times are not localised: %v", w0)
	}
	wd := call(t, cs, "get_workout", map[string]any{"id": w0["id"]}, false)
	if len(wd["hr_series"].([]any)) == 0 {
		t.Fatalf("workout: %v", wd)
	}
	call(t, cs, "get_workout", map[string]any{"id": 999999}, true)
	call(t, cs, "get_day_detail", map[string]any{"day": w0["day"]}, false)
	if tl := call(t, cs, "get_training_load", map[string]any{"period": "last_30d"}, false); tl["consistency"] == nil {
		t.Fatalf("training load: %v", tl)
	}
	cmp := call(t, cs, "compare_periods", map[string]any{"a": "2026-02-02..2026-02-15", "b": "2026-01-05..2026-01-18"}, false)
	steps := cmp["activity"].(map[string]any)["steps"].(map[string]any)
	if steps["a"] == nil || steps["b"] == nil || steps["delta"] == nil {
		t.Fatalf("compare: %v", steps)
	}
	// Sessions count the main type (strength) only; the Saturday walks are in all_workouts.
	tr := cmp["training"].(map[string]any)
	if tr["sessions"].(map[string]any)["a"] != 6.0 || tr["all_workouts"].(map[string]any)["a"] != 8.0 {
		t.Fatalf("compare training: %v", tr)
	}

	// Routes: walks have a GPS route, exposed in full over REST and as a summary over MCP.
	walks := call(t, cs, "list_workouts", map[string]any{"period": "last_90d", "activity_type": "Walking"}, false)["workouts"].([]any)
	walk := walks[0].(map[string]any)
	if len(walks) != 6 || walk["has_route"] != true {
		t.Fatalf("walks: %d, first %v", len(walks), walk)
	}
	wid := int64(walk["id"].(float64))
	var rt struct {
		DistanceM float64 `json:"distance_m"`
		Track     []struct {
			Lat, Lon float64
		} `json:"track"`
	}
	if code, body := s.do("GET", "/api/v1/workouts/"+strconv.FormatInt(wid, 10)+"/route?points=100", alice, nil, &rt); code != http.StatusOK {
		t.Fatalf("route: %d %s", code, body)
	}
	if len(rt.Track) < 90 || len(rt.Track) > 101 || rt.DistanceM < 3000 || rt.DistanceM > 4000 {
		t.Fatalf("route: %d points, %.0f m", len(rt.Track), rt.DistanceM)
	}
	if code, _ := s.do("GET", "/api/v1/workouts/"+strconv.FormatInt(int64(w0["id"].(float64)), 10)+"/route", alice, nil, nil); code != http.StatusNotFound {
		t.Fatalf("strength workout route: %d, want 404", code)
	}
	wr := call(t, cs, "get_workout", map[string]any{"id": wid}, false)
	if r, ok := wr["route"].(map[string]any); !ok || r["distance_m"] == nil || r["track"] != nil {
		t.Fatalf("mcp route summary: %v", wr["route"])
	}
	call(t, cs, "get_settings", map[string]any{}, false)
	call(t, cs, "get_insights", map[string]any{"category": "activity"}, false)
	call(t, cs, "get_insights", map[string]any{"category": "mood"}, true)

	// query_sql is off until the user allows it in Settings.
	if e := call(t, cs, "query_sql", map[string]any{"sql": "SELECT 1"}, true); !strings.Contains(e["error"].(string), "Allow AI assistants to run SQL queries") {
		t.Fatalf("disabled sql: %v", e)
	}
	var st struct {
		Settings map[string]any `json:"settings"`
	}
	if code, body := s.do("GET", "/api/v1/settings", alice, nil, &st); code != http.StatusOK {
		t.Fatalf("get settings: %d %s", code, body)
	}
	st.Settings["allow_sql"] = true
	if code, body := s.do("PUT", "/api/v1/settings", alice, st.Settings, nil); code != http.StatusOK {
		t.Fatalf("put settings: %d %s", code, body)
	}
	q := call(t, cs, "query_sql", map[string]any{"sql": "SELECT count(*) AS n FROM v_workouts"}, false)
	if q["row_count"].(float64) != 1 {
		t.Fatalf("sql: %v", q)
	}
	big := call(t, cs, "query_sql", map[string]any{"sql": "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c LIMIT 600) SELECT x FROM c"}, false)
	if big["row_count"].(float64) != 500 || big["truncated"] != true {
		t.Fatalf("row cap: %v %v", big["row_count"], big["truncated"])
	}
	for _, bad := range []string{
		"DELETE FROM records",
		"PRAGMA table_info(records)",
		"ATTACH DATABASE 'other.db' AS o",
		"SELECT 1; DELETE FROM records",
		"WITH x AS (SELECT 1) DELETE FROM records",
		"select * from records where 1=1 /* ; */ ; drop table records",
	} {
		call(t, cs, "query_sql", map[string]any{"sql": bad}, true)
	}

	// Resources and prompts.
	res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "ritm://schema"})
	if err != nil || !strings.Contains(res.Contents[0].Text, "## v_daily") || !strings.Contains(res.Contents[0].Text, "resting_hr_bpm") {
		t.Fatalf("schema: %v", err)
	}
	res, err = cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "ritm://data-coverage"})
	if err != nil || !strings.Contains(res.Contents[0].Text, "HKQuantityTypeIdentifierHeartRate") {
		t.Fatalf("coverage: %v", err)
	}
	pr, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "training_review", Arguments: map[string]string{"weeks": "4"}})
	if err != nil || !strings.Contains(pr.Messages[0].Content.(*mcp.TextContent).Text, "last_28d") {
		t.Fatalf("prompt: %v", err)
	}

	// Each key reads only its owner's database.
	bobCS := mcpClient(t, srv.URL, bobKey)
	if bw := call(t, bobCS, "list_workouts", map[string]any{"period": "last_90d"}, false); len(bw["workouts"].([]any)) != 0 {
		t.Fatal("bob sees alice's workouts")
	}

	// The setup page's catalog lists the same tools, in the spec's order.
	var info2 struct {
		Catalog struct {
			Tools []struct{ Name string } `json:"tools"`
		} `json:"catalog"`
	}
	if code, _ := s.do("GET", "/api/v1/mcp/catalog", alice, nil, &info2); code != http.StatusOK || len(info2.Catalog.Tools) != 12 ||
		info2.Catalog.Tools[0].Name != "get_overview" || info2.Catalog.Tools[11].Name != "query_sql" {
		t.Fatalf("catalog: %d %+v", code, info2)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
