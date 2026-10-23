package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"ritm/internal/auth"
	"ritm/internal/clock"
	"ritm/internal/config"
	"ritm/internal/ingest"
	"ritm/internal/store"
	"ritm/internal/synth"
	"ritm/internal/tenant"
)

type testServer struct {
	t       *testing.T
	h       http.Handler
	auth    *auth.Service
	tenants *tenant.Registry
	clock   *clock.Fake
	appDB   *store.DB
}

func newTestServer(t *testing.T, registration bool, now time.Time) *testServer {
	t.Helper()
	ctx := context.Background()
	cfg := &config.Config{DataDir: t.TempDir(), TZ: time.UTC, RegistrationEnabled: registration, MaxConcurrentImports: 1,
		SessionTTL: time.Hour, DeltaWindow: 7 * 24 * time.Hour, StaleAfter: 14 * 24 * time.Hour, MaxUploadBytes: 64 << 20}
	appDB, err := store.OpenApp(ctx, cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(now)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := auth.NewService(appDB, clk, cfg.SessionTTL)
	reg := tenant.NewRegistry(cfg, log, clk, a)
	t.Cleanup(func() {
		reg.Shutdown(context.Background())
		appDB.Close()
	})
	h := New(Deps{Cfg: cfg, AppDB: appDB, Auth: a, Tenants: reg, Clock: clk, Log: log,
		SPA: fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}, Version: "test"})
	return &testServer{t: t, h: h, auth: a, tenants: reg, clock: clk, appDB: appDB}
}

// do sends a request with the CSRF header and the given session cookie and decodes JSON into out.
func (s *testServer) do(method, path, cookie string, body any, out any) (int, string) {
	s.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set(auth.CSRFHeader, "1")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
	}
	w := httptest.NewRecorder()
	s.h.ServeHTTP(w, req)
	if out != nil && w.Code < 300 {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			s.t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	var session string
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			session = c.Value
		}
	}
	return w.Code, session
}

func (s *testServer) register(name string) string {
	s.t.Helper()
	code, cookie := s.do("POST", "/api/v1/auth/register", "", map[string]string{"username": name, "password": "password1"}, nil)
	if code != http.StatusCreated || cookie == "" {
		s.t.Fatalf("register %s: status %d", name, code)
	}
	return cookie
}

func TestRegistrationDisabledByDefault(t *testing.T) {
	s := newTestServer(t, false, time.Now())
	var cfg map[string]bool
	if code, _ := s.do("GET", "/api/v1/auth/config", "", nil, &cfg); code != 200 || cfg["registration_enabled"] {
		t.Fatalf("config: %d %v", code, cfg)
	}
	if code, _ := s.do("POST", "/api/v1/auth/register", "", map[string]string{"username": "alice", "password": "password1"}, nil); code != http.StatusNotFound {
		t.Fatalf("register while disabled: %d, want 404", code)
	}
}

func TestRegisterAndDuplicate(t *testing.T) {
	s := newTestServer(t, true, time.Now())
	cookie := s.register("alice")
	var me struct{ User auth.User }
	if code, _ := s.do("GET", "/api/v1/account", cookie, nil, &me); code != 200 || me.User.Username != "alice" {
		t.Fatalf("me: %d %+v", code, me)
	}
	if code, _ := s.do("POST", "/api/v1/auth/register", "", map[string]string{"username": "Alice", "password": "password1"}, nil); code != http.StatusConflict {
		t.Fatalf("duplicate: %d, want 409", code)
	}
	if code, _ := s.do("POST", "/api/v1/auth/register", "", map[string]string{"username": "bob", "password": "short"}, nil); code != http.StatusBadRequest {
		t.Fatalf("short password: %d, want 400", code)
	}
}

// A cross-site page cannot sign visitors in or create accounts: the public writes need the CSRF
// header too, even without a Content-Type.
func TestPublicWritesNeedCSRF(t *testing.T) {
	s := newTestServer(t, true, time.Now())
	s.register("alice")
	body := []byte(`{"username":"bob","password":"password1"}`)
	for _, path := range []string{"/api/v1/auth/login", "/api/v1/auth/register"} {
		if w := s.raw("POST", path, body, nil); w.Code != http.StatusForbidden {
			t.Fatalf("%s without CSRF header: %d, want 403", path, w.Code)
		}
		if w := s.raw("POST", path, body, map[string]string{auth.CSRFHeader: "1", "Origin": "https://evil.example"}); w.Code != http.StatusForbidden {
			t.Fatalf("%s from another origin: %d, want 403", path, w.Code)
		}
	}
	if _, err := s.auth.UserByName(context.Background(), "bob"); err == nil {
		t.Fatal("bob was created")
	}
}

func TestUsersCannotSeeEachOthersData(t *testing.T) {
	ctx := context.Background()
	o := synth.Options{Start: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), Days: 14, Loc: time.UTC, Seed: 1, HRIntervalSec: 1800, WorkoutHRIntervalSec: 30}
	s := newTestServer(t, true, o.Start.AddDate(0, 0, o.Days).Add(-time.Hour))
	alice, bob := s.register("alice"), s.register("bob")

	// Import directly into alice's database.
	info, err := s.auth.UserByName(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	tn, err := s.tenants.Get(ctx, info.User)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "e.zip")
	if err := synth.WriteZip(p, o); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.RunRecorded(ctx, tn.DB, p, "e.zip", 0, tn.IngestOptions(), tn.Derive.AfterImport); err != nil {
		t.Fatal(err)
	}

	type workouts struct {
		Workouts []struct{ ID int64 } `json:"items"`
	}
	type imports struct {
		Imports []ingest.ImportRow `json:"items"`
	}
	const rng = "?from=2026-01-05&to=2026-01-18"
	var aw, bw workouts
	var ai, bi imports
	s.do("GET", "/api/v1/workouts"+rng, alice, nil, &aw)
	s.do("GET", "/api/v1/imports", alice, nil, &ai)
	if len(aw.Workouts) == 0 || len(ai.Imports) != 1 {
		t.Fatalf("alice: %d workouts, %d imports", len(aw.Workouts), len(ai.Imports))
	}
	s.do("GET", "/api/v1/workouts"+rng, bob, nil, &bw)
	s.do("GET", "/api/v1/imports", bob, nil, &bi)
	if len(bw.Workouts) != 0 || len(bi.Imports) != 0 {
		t.Fatalf("bob sees alice's data: %d workouts, %d imports", len(bw.Workouts), len(bi.Imports))
	}
	if code, _ := s.do("GET", fmt.Sprintf("/api/v1/workouts/%d", aw.Workouts[0].ID), bob, nil, nil); code != http.StatusNotFound {
		t.Fatalf("bob reads alice's workout: %d", code)
	}
	if code, _ := s.do("GET", "/api/v1/imports/"+ai.Imports[0].ID, bob, nil, nil); code != http.StatusNotFound {
		t.Fatalf("bob reads alice's import: %d", code)
	}

	// Reset keeps the account but drops the data.
	if code, _ := s.do("POST", "/api/v1/account/reset", alice, map[string]string{"password": "wrong-password"}, nil); code != http.StatusForbidden {
		t.Fatalf("reset with wrong password: %d", code)
	}
	if code, _ := s.do("POST", "/api/v1/account/reset", alice, map[string]string{"password": "password1"}, nil); code != http.StatusNoContent {
		t.Fatalf("reset: %d", code)
	}
	ai = imports{}
	if code, _ := s.do("GET", "/api/v1/imports", alice, nil, &ai); code != 200 || len(ai.Imports) != 0 {
		t.Fatalf("after reset: %d, %d imports", code, len(ai.Imports))
	}
}

func TestChangePasswordAndDeleteAccount(t *testing.T) {
	s := newTestServer(t, true, time.Now())
	cookie := s.register("alice")
	if code, _ := s.do("PUT", "/api/v1/account/password", cookie, map[string]string{"current_password": "nope-nope", "new_password": "password2"}, nil); code != http.StatusForbidden {
		t.Fatalf("wrong current password: %d", code)
	}
	if code, _ := s.do("PUT", "/api/v1/account/password", cookie, map[string]string{"current_password": "password1", "new_password": "password2"}, nil); code != http.StatusNoContent {
		t.Fatalf("change password: %d", code)
	}
	if code, _ := s.do("GET", "/api/v1/account", cookie, nil, nil); code != 200 {
		t.Fatalf("current session must survive a password change: %d", code)
	}
	if code, _ := s.do("DELETE", "/api/v1/account", cookie, map[string]string{"password": "password2"}, nil); code != http.StatusNoContent {
		t.Fatalf("delete account: %d", code)
	}
	if code, _ := s.do("GET", "/api/v1/account", cookie, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("session after delete: %d", code)
	}
	if code, _ := s.do("POST", "/api/v1/auth/login", "", map[string]string{"username": "alice", "password": "password2"}, nil); code != http.StatusUnauthorized {
		t.Fatalf("login after delete: %d", code)
	}
}

func TestBrowserTimezone(t *testing.T) {
	s := newTestServer(t, true, time.Date(2026, 10, 23, 22, 30, 0, 0, time.UTC))
	login := func(name, zone string, status int) (string, map[string]any) {
		t.Helper()
		var out map[string]any
		path := "/api/v1/auth/login"
		if status == http.StatusCreated {
			path = "/api/v1/auth/register"
		}
		code, cookie := s.do("POST", path, "", map[string]string{"username": name, "password": "password1", "timezone": zone}, &out)
		if code != status {
			t.Fatalf("%s %s: status %d, want %d", path, name, code, status)
		}
		return cookie, out
	}
	clockOf := func(m map[string]any) string { return fmt.Sprint(m["timezone"], " ", m["today"]) }

	// The first sign-in adopts the browser's zone; later ones keep it.
	_, sess := login("alice", "Asia/Tokyo", http.StatusCreated)
	if got := clockOf(sess); got != "Asia/Tokyo 2026-10-24" {
		t.Fatalf("register clock = %s", got)
	}
	cookie, sess := login("alice", "America/New_York", http.StatusOK)
	if got := clockOf(sess); got != "Asia/Tokyo 2026-10-24" {
		t.Fatalf("login clock = %s, want the saved zone", got)
	}

	// Settings change it, and the services follow.
	var st map[string]any
	if code, _ := s.do("GET", "/api/v1/settings", cookie, nil, &st); code != 200 {
		t.Fatalf("get settings: %d", code)
	}
	settings := st["settings"].(map[string]any)
	if settings["timezone"] != "Asia/Tokyo" {
		t.Fatalf("settings timezone = %v", settings["timezone"])
	}
	settings["timezone"] = "Mars/Olympus"
	if code, _ := s.do("PUT", "/api/v1/settings", cookie, settings, nil); code != http.StatusBadRequest {
		t.Fatalf("invalid zone: %d, want 400", code)
	}
	settings["timezone"] = "America/New_York"
	if code, _ := s.do("PUT", "/api/v1/settings", cookie, settings, nil); code != 200 {
		t.Fatalf("put settings: %d", code)
	}
	var acct map[string]any
	if code, _ := s.do("GET", "/api/v1/account", cookie, nil, &acct); code != 200 || clockOf(acct) != "America/New_York 2026-10-23" {
		t.Fatalf("account clock = %d %s", code, clockOf(acct))
	}

	// Without a valid browser zone the user stays on RITM_TZ, and a later sign-in can still set one.
	_, sess = login("bob", "Nowhere/Land", http.StatusCreated)
	if got := clockOf(sess); got != "UTC 2026-10-23" {
		t.Fatalf("bob clock = %s", got)
	}
	_, sess = login("bob", "Asia/Tokyo", http.StatusOK)
	if got := clockOf(sess); got != "Asia/Tokyo 2026-10-24" {
		t.Fatalf("bob clock after login = %s", got)
	}
}
