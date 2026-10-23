package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ritm/internal/auth"
	"ritm/internal/ingest"
	"ritm/internal/synth"
)

// raw sends a request with full control over headers and returns the recorder.
func (s *testServer) raw(method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	s.t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.9:4000"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.h.ServeHTTP(w, req)
	return w
}

// An ingest key uploads an export and follows its import, and can do nothing else.
func TestIngestKey(t *testing.T) {
	o := synth.Options{Start: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), Days: 3, Loc: time.UTC, Seed: 2, HRIntervalSec: 1800}
	s := newTestServer(t, true, o.Start.AddDate(0, 0, o.Days))
	alice := s.register("alice")
	key := s.createKey(alice, "Shortcut", auth.ScopeIngest)
	p := filepath.Join(t.TempDir(), "export.zip")
	if err := synth.WriteZip(p, o); err != nil {
		t.Fatal(err)
	}
	zip, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	bearer := map[string]string{"Authorization": "Bearer " + key, "X-File-Name": "export.zip", "Content-Type": "application/zip"}

	w := s.raw("POST", "/api/v1/imports", zip, bearer)
	if w.Code != http.StatusAccepted {
		t.Fatalf("upload with ingest key: %d %s", w.Code, w.Body)
	}
	var imp Import
	if err := json.Unmarshal(w.Body.Bytes(), &imp); err != nil || imp.ID == "" || imp.FileName != "export.zip" {
		t.Fatalf("upload response %s: %v", w.Body, err)
	}
	if loc := w.Header().Get("Location"); loc != "/api/v1/imports/"+imp.ID {
		t.Fatalf("Location %q", loc)
	}
	deadline := time.Now().Add(20 * time.Second)
	for imp.Status != ingest.StatusSucceeded {
		if time.Now().After(deadline) || imp.Status == ingest.StatusFailed {
			t.Fatalf("import status %s %s", imp.Status, imp.Error)
		}
		time.Sleep(50 * time.Millisecond)
		w = s.raw("GET", "/api/v1/imports/"+imp.ID, nil, bearer)
		if w.Code != http.StatusOK {
			t.Fatalf("status with ingest key: %d %s", w.Code, w.Body)
		}
		imp = Import{}
		json.Unmarshal(w.Body.Bytes(), &imp)
	}
	if imp.Stats == nil || imp.Stats.NewWorkouts == 0 {
		t.Fatalf("stats %+v", imp.Stats)
	}

	for _, path := range []string{"/api/v1/overview", "/api/v1/imports", "/api/v1/settings"} {
		if w := s.raw("GET", path, nil, bearer); w.Code != http.StatusForbidden {
			t.Errorf("ingest key on %s: %d, want 403", path, w.Code)
		}
	}
	monitorKey := s.createKey(alice, "Kuma", auth.ScopeMonitor)
	w = s.raw("POST", "/api/v1/imports", zip, map[string]string{"Authorization": "Bearer " + monitorKey})
	if w.Code != http.StatusForbidden {
		t.Fatalf("upload with monitor key: %d, want 403", w.Code)
	}
	if w := s.raw("GET", "/api/v1/monitor/freshness", nil, bearer); w.Code != http.StatusForbidden {
		t.Fatalf("ingest key on the monitor endpoint: %d, want 403", w.Code)
	}
	if w := s.raw("POST", "/api/v1/imports", zip, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous upload: %d, want 401", w.Code)
	}
}

// Errors are RFC 9457 problems; session writes need the CSRF header; ranges are validated.
// The calendar marks the imported days and reports the last one.
func TestCalendar(t *testing.T) {
	o := synth.Options{Start: time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC), Days: 5, Loc: time.UTC, Seed: 3, HRIntervalSec: 1800}
	s := newTestServer(t, true, o.Start.AddDate(0, 0, 30))
	alice := s.register("alice")
	info, _ := s.auth.UserByName(context.Background(), "alice")
	tn, err := s.tenants.Get(context.Background(), info.User)
	if err != nil {
		t.Fatal(err)
	}
	s.importSynth(tn, o)

	var cal DataCalendar
	if code, _ := s.do("GET", "/api/v1/calendar?from=2026-08-01&to=2026-08-31", alice, nil, &cal); code != http.StatusOK {
		t.Fatalf("calendar: %d", code)
	}
	if cal.From != "2026-08-01" || cal.To != "2026-08-31" || len(cal.Items) == 0 || cal.LastDay == nil || *cal.LastDay != cal.Items[len(cal.Items)-1].Day {
		t.Fatalf("calendar %+v", cal)
	}
	watch := 0
	for _, d := range cal.Items {
		if d.Watch {
			watch++
		}
	}
	if watch == 0 {
		t.Fatalf("calendar has no Watch days: %+v", cal.Items)
	}
	if code, _ := s.do("GET", "/api/v1/calendar?from=2025-08-01&to=2025-08-31", alice, nil, &cal); code != http.StatusOK || len(cal.Items) != 0 || cal.LastDay == nil {
		t.Fatalf("calendar before data: %d %+v", code, cal)
	}
}

func TestProblemsAndCSRF(t *testing.T) {
	s := newTestServer(t, true, time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	alice := s.register("alice")
	cookie := map[string]string{"Cookie": auth.SessionCookie + "=" + alice, "Content-Type": "application/json"}
	problem := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		var p struct {
			Status int
			Detail string
		}
		if w.Code != status || w.Header().Get("Content-Type") != "application/problem+json" ||
			json.Unmarshal(w.Body.Bytes(), &p) != nil || p.Status != status || p.Detail == "" {
			t.Fatalf("want a %d problem, got %d %q %s", status, w.Code, w.Header().Get("Content-Type"), w.Body)
		}
	}
	problem(s.raw("POST", "/api/v1/api-keys", []byte(`{"name":"x","scope":"read"}`), cookie), http.StatusForbidden)
	problem(s.raw("GET", "/api/v1/activity/daily?period=fortnight", nil, cookie), http.StatusBadRequest)
	problem(s.raw("GET", "/api/v1/overview", nil, nil), http.StatusUnauthorized)
	problem(s.raw("GET", "/api/v1/nope", nil, cookie), http.StatusNotFound)
	cookie[auth.CSRFHeader] = "1"
	problem(s.raw("POST", "/api/v1/api-keys", []byte(`{"name":"x","scope":"admin"}`), cookie), http.StatusUnprocessableEntity)
	if w := s.raw("POST", "/api/v1/api-keys", []byte(`{"name":"x","scope":"ingest"}`), cookie); w.Code != http.StatusCreated {
		t.Fatalf("create ingest key: %d %s", w.Code, w.Body)
	}
}

// docs/openapi.yaml is the committed output of `ritm openapi`; regenerate it with `make openapi`.
func TestOpenAPIUpToDate(t *testing.T) {
	want, err := OpenAPI("dev").YAML()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("docs/openapi.yaml is out of date; run `make openapi`")
	}
}

// A read key reads every kind of health data over REST, and can change nothing.
func TestReadKey(t *testing.T) {
	ctx := context.Background()
	o := synth.Options{Start: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), Days: 14, Loc: time.UTC, Seed: 1, Sleep: true,
		HRIntervalSec: 1800, WorkoutHRIntervalSec: 60}
	s := newTestServer(t, true, o.Start.AddDate(0, 0, o.Days).Add(10*time.Hour))
	alice := s.register("alice")
	readKey := s.createKey(alice, "Grafana", auth.ScopeRead)
	ingestKey := s.createKey(alice, "Shortcut", auth.ScopeIngest)
	monKey := s.createKey(alice, "Kuma", auth.ScopeMonitor)
	info, _ := s.auth.UserByName(ctx, "alice")
	tn, err := s.tenants.Get(ctx, info.User)
	if err != nil {
		t.Fatal(err)
	}
	s.importSynth(tn, o)

	var workouts struct{ Items []struct{ ID int64 } }
	if code := s.bearer("/api/v1/workouts?period=last_30d", readKey, &workouts); code != http.StatusOK || len(workouts.Items) == 0 {
		t.Fatalf("workouts: %d, %d items", code, len(workouts.Items))
	}
	wid := fmt.Sprint(workouts.Items[0].ID)
	reads := []string{
		"/api/v1/overview?period=last_7d", "/api/v1/freshness", "/api/v1/insights", "/api/v1/days/2026-01-10",
		"/api/v1/calendar?period=last_30d", "/api/v1/activity/daily?period=last_30d", "/api/v1/heart/daily?period=last_30d",
		"/api/v1/heart/events?period=last_30d", "/api/v1/sleep/nights?period=last_30d", "/api/v1/sleep/recovery",
		"/api/v1/coverage", "/api/v1/workout-types", "/api/v1/workouts/" + wid, "/api/v1/workouts/" + wid + "/heart-rate",
		"/api/v1/training/load?period=last_30d", "/api/v1/training/weekly?period=last_30d",
	}
	for _, p := range reads {
		if code := s.bearer(p, readKey, nil); code != http.StatusOK {
			t.Errorf("read key GET %s: %d", p, code)
		}
		for name, k := range map[string]string{"ingest": ingestKey, "monitor": monKey} {
			if code := s.bearer(p, k, nil); code != http.StatusForbidden {
				t.Errorf("%s key GET %s: %d, want 403", name, p, code)
			}
		}
	}
	for _, p := range []string{"/api/v1/settings", "/api/v1/api-keys", "/api/v1/imports", "/api/v1/account", "/api/v1/mcp/catalog"} {
		if code := s.bearer(p, readKey, nil); code != http.StatusForbidden {
			t.Errorf("read key GET %s: %d, want 403", p, code)
		}
	}
	bearer := map[string]string{"Authorization": "Bearer " + readKey, "Content-Type": "application/json"}
	writes := []struct{ method, path, body string }{
		{"PUT", "/api/v1/workouts/" + wid + "/annotation", `{"rpe":5,"notes":"x"}`},
		{"PUT", "/api/v1/settings", `{}`},
		{"POST", "/api/v1/api-keys", `{"name":"x","scope":"read"}`},
		{"POST", "/api/v1/imports", `x`},
	}
	for _, w := range writes {
		if r := s.raw(w.method, w.path, []byte(w.body), bearer); r.Code != http.StatusForbidden {
			t.Errorf("read key %s %s: %d, want 403", w.method, w.path, r.Code)
		}
	}
}
