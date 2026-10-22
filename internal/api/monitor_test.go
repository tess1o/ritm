package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"ritm/internal/auth"
	"ritm/internal/ingest"
	"ritm/internal/monitor"
	"ritm/internal/query"
	"ritm/internal/synth"
	"ritm/internal/tenant"
)

// bearer sends GET path with an optional bearer key and decodes the JSON body.
func (s *testServer) bearer(path, key string, out any) int {
	s.t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = "203.0.113.7:4000"
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	s.h.ServeHTTP(w, req)
	if out != nil {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			s.t.Fatalf("GET %s: decode %q: %v", path, w.Body.String(), err)
		}
	}
	if path != "/healthz" && w.Code != http.StatusTooManyRequests && w.Header().Get("Cache-Control") != "no-store" {
		s.t.Errorf("GET %s: Cache-Control %q", path, w.Header().Get("Cache-Control"))
	}
	return w.Code
}

func (s *testServer) createKey(cookie, name, scope string) string {
	s.t.Helper()
	var out struct{ Secret string }
	if code, _ := s.do("POST", "/api/v1/api-keys", cookie, map[string]string{"name": name, "scope": scope}, &out); code != http.StatusCreated {
		s.t.Fatalf("create %s key: %d", scope, code)
	}
	return out.Secret
}

func (s *testServer) importSynth(tn *tenant.Tenant, o synth.Options) {
	s.t.Helper()
	ctx := context.Background()
	p := filepath.Join(s.t.TempDir(), "e.zip")
	if err := synth.WriteZip(p, o); err != nil {
		s.t.Fatal(err)
	}
	if _, err := ingest.RunRecorded(ctx, tn.DB, p, "e.zip", 0, tn.IngestOptions(), tn.Derive.AfterImport); err != nil {
		s.t.Fatal(err)
	}
}

func TestMonitorFreshness(t *testing.T) {
	ctx := context.Background()
	const path = "/api/v1/monitor/freshness"
	o := synth.Options{Start: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), Days: 14, Loc: time.UTC, Seed: 1, HRIntervalSec: 1800, WorkoutHRIntervalSec: 60}
	end := o.Start.AddDate(0, 0, o.Days)
	s := newTestServer(t, true, end)
	clk := s.clock
	alice, bob := s.register("alice"), s.register("bob")
	monKey, mcpKey := s.createKey(alice, "Kuma", auth.ScopeMonitor), s.createKey(alice, "Claude", auth.ScopeMCP)
	bobKey := s.createKey(bob, "Kuma", auth.ScopeMonitor)

	// MON-4: auth and scope.
	if code := s.bearer(path, "", nil); code != http.StatusUnauthorized {
		t.Fatalf("no key: %d", code)
	}
	if code := s.bearer(path, "ritm_nope", nil); code != http.StatusUnauthorized {
		t.Fatalf("bad key: %d", code)
	}
	if code := s.bearer(path, mcpKey, nil); code != http.StatusForbidden {
		t.Fatalf("mcp key: %d", code)
	}
	if code, _ := s.do("GET", path, alice, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("session cookie instead of key: %d", code)
	}

	// Nothing imported yet.
	var rep monitor.FreshnessReport
	if code := s.bearer(path, monKey, &rep); code != http.StatusServiceUnavailable || rep.Status != monitor.StatusNoData {
		t.Fatalf("empty: %d %+v", code, rep)
	}

	info, _ := s.auth.UserByName(ctx, "alice")
	tn, err := s.tenants.Get(ctx, info.User)
	if err != nil {
		t.Fatal(err)
	}
	s.importSynth(tn, o)

	rep = monitor.FreshnessReport{}
	if code := s.bearer(path, monKey, &rep); code != http.StatusOK || rep.Status != monitor.StatusOK || rep.StaleAfterDays != 14 || rep.LastDataAt == nil {
		t.Fatalf("fresh: %d %+v", code, rep)
	}
	firstLast := *rep.LastDataAt

	// Each user's monitor key sees only their own database.
	if code := s.bearer(path, bobKey, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("bob after alice's import: %d", code)
	}

	// MON-9: the poll is recorded for the Settings page, and the key's last use too.
	tn.Query.Wait()
	var fr query.Freshness
	s.do("GET", "/api/v1/freshness", alice, nil, &fr)
	if fr.LastCheckedAt == nil || !fr.LastCheckedAt.Equal(end) {
		t.Fatalf("last_checked_at = %v, want %v", fr.LastCheckedAt, end)
	}
	var keys struct{ Items []auth.APIKey }
	s.do("GET", "/api/v1/api-keys", alice, nil, &keys)
	for _, k := range keys.Items {
		if k.Scope == auth.ScopeMonitor && k.LastUsedAt == nil {
			t.Fatal("monitor key last_used_at not set")
		}
	}

	// Red after the threshold (MON-2), with a per-request override (MON-5).
	clk.Advance(15 * 24 * time.Hour)
	rep = monitor.FreshnessReport{}
	if code := s.bearer(path, monKey, &rep); code != http.StatusServiceUnavailable || rep.Status != monitor.StatusStale || rep.AgeDays == nil || *rep.AgeDays < 15 {
		t.Fatalf("stale: %d %+v", code, rep)
	}
	if code := s.bearer(path+"?max_age=30d", monKey, &rep); code != http.StatusOK || rep.StaleAfterDays != 30 {
		t.Fatalf("max_age=30d: %d %+v", code, rep)
	}
	if code := s.bearer(path+"?max_age=nonsense", monKey, nil); code != http.StatusBadRequest {
		t.Fatalf("bad max_age: %d", code)
	}

	// The Settings threshold applies when no max_age is given. (The session expired with the jump.)
	_, alice = s.do("POST", "/api/v1/auth/login", "", map[string]string{"username": "alice", "password": "password1"}, nil)
	var st map[string]any
	if code, _ := s.do("GET", "/api/v1/settings", alice, nil, &st); code != http.StatusOK {
		t.Fatalf("get settings: %d", code)
	}
	settings := st["settings"].(map[string]any)
	settings["stale_after_hours"] = 20 * 24
	if code, _ := s.do("PUT", "/api/v1/settings", alice, settings, nil); code != http.StatusOK {
		t.Fatalf("save settings: %d", code)
	}
	if code := s.bearer(path, monKey, &rep); code != http.StatusOK || rep.StaleAfterDays != 20 {
		t.Fatalf("settings threshold: %d %+v", code, rep)
	}
	settings["stale_after_hours"] = nil
	s.do("PUT", "/api/v1/settings", alice, settings, nil)

	// MON-1: re-uploading an older export does not reset the clock.
	older := o
	older.Start = o.Start.AddDate(0, 0, -30)
	s.importSynth(tn, older)
	if code := s.bearer(path, monKey, &rep); code != http.StatusServiceUnavailable || *rep.LastDataAt != firstLast {
		t.Fatalf("after older import: %d %+v (want last_data_at %s)", code, rep, firstLast)
	}

	// Green again after a newer upload.
	newer := o
	newer.Start = o.Start.AddDate(0, 0, 14)
	s.importSynth(tn, newer)
	if code := s.bearer(path, monKey, &rep); code != http.StatusOK || rep.Status != monitor.StatusOK {
		t.Fatalf("after newer import: %d %+v", code, rep)
	}

	// Revoked keys stop working immediately (KEY-3).
	for _, k := range keys.Items {
		if k.Scope == auth.ScopeMonitor {
			s.do("DELETE", "/api/v1/api-keys/"+strconv.FormatInt(k.ID, 10), alice, nil, nil)
		}
	}
	if code := s.bearer(path, monKey, nil); code != http.StatusUnauthorized {
		t.Fatalf("revoked key: %d", code)
	}
}

func TestMonitorKeyRateLimit(t *testing.T) {
	s := newTestServer(t, true, time.Now())
	key := s.createKey(s.register("alice"), "Kuma", auth.ScopeMonitor)
	for i := 0; i < 19; i++ {
		if code := s.bearer("/api/v1/monitor/freshness", "ritm_guess", nil); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	if code := s.bearer("/api/v1/monitor/freshness", "ritm_guess", nil); code != http.StatusUnauthorized {
		t.Fatalf("20th attempt: %d", code)
	}
	if code := s.bearer("/api/v1/monitor/freshness", key, nil); code != http.StatusTooManyRequests {
		t.Fatalf("after 20 failures: %d, want 429", code)
	}
	s.clock.Advance(16 * time.Minute)
	if code := s.bearer("/api/v1/monitor/freshness", key, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("after the window: %d", code)
	}
}

func TestHealthz(t *testing.T) {
	s := newTestServer(t, false, time.Now())
	var body map[string]string
	if code := s.bearer("/healthz", "", &body); code != http.StatusOK || body["status"] != "ok" || body["version"] != "test" {
		t.Fatalf("healthz: %d %v", code, body)
	}
	s.appDB.Close()
	if code := s.bearer("/healthz", "", &body); code != http.StatusServiceUnavailable || body["status"] != "error" {
		t.Fatalf("healthz with closed DB: %d %v", code, body)
	}
}
