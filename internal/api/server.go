// Package api implements Ritm's versioned REST API (/api/v1, §11) with Huma, which also
// generates its OpenAPI description, and serves the embedded SPA.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"ritm/internal/auth"
	"ritm/internal/clock"
	"ritm/internal/config"
	"ritm/internal/httpx"
	"ritm/internal/mcpserver"
	"ritm/internal/query"
	"ritm/internal/store"
	"ritm/internal/tenant"
)

// BasePath is the prefix of every REST operation.
const BasePath = "/api/v1"

type Deps struct {
	Cfg     *config.Config
	AppDB   *store.DB
	Auth    *auth.Service
	Tenants *tenant.Registry
	Clock   clock.Clock
	Log     *slog.Logger
	SPA     fs.FS
	Version string
}

type Server struct {
	Deps
	mux *http.ServeMux
	mcp *mcpserver.Handler
	api huma.API // the whole API (for /healthz)
	v1  huma.API // operations under BasePath
}

func init() {
	// Lists are always sent as arrays (never null), so document them as non-nullable.
	huma.DefaultArrayNullable = false
}

// New returns the HTTP handler for the server: REST API, MCP, SPA.
func New(d Deps) http.Handler {
	s := newServer(d)
	return s.recoverer(s.logger(s.mux))
}

// OpenAPI returns the API description without opening any database (`ritm openapi`).
func OpenAPI(version string) *huma.OpenAPI {
	return newServer(Deps{Cfg: &config.Config{}, Version: version}).api.OpenAPI()
}

func newServer(d Deps) *Server {
	s := &Server{Deps: d, mux: http.NewServeMux(),
		mcp: mcpserver.New(mcpserver.Deps{Cfg: d.Cfg, Tenants: d.Tenants, Log: d.Log, Version: d.Version})}
	s.api = humago.New(s.mux, apiConfig(d.Version))
	s.api.UseMiddleware(s.authenticate)
	s.v1 = huma.NewGroup(s.api, BasePath)
	s.registerMeta()
	s.registerAuth()
	s.registerAccount()
	s.registerKeys()
	s.registerImports()
	s.registerData()
	s.registerWorkouts()
	s.registerSettings()
	s.registerMonitor()

	// Not REST: the MCP transport (§10), OAuth discovery probes, unknown API paths and the SPA.
	s.mux.Handle("/mcp", noStore(s.Auth.RequireKey(auth.ScopeMCP, s.mcp)))
	s.mux.HandleFunc("/.well-known/", func(w http.ResponseWriter, r *http.Request) { httpx.Error(w, http.StatusNotFound, "not found") })
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(huma.ErrorModel{Title: "Not Found", Status: http.StatusNotFound,
			Detail: "no such API endpoint; see " + BasePath + "/docs"})
	})
	s.mux.Handle("/", s.spa())
	return s
}

const apiDescription = `REST API of Ritm, a self-hosted Apple Health (Apple Watch) history: activity, heart,
sleep, workouts, training load and insights. The web app uses this API too.

## Conventions
- All operations are under ` + "`/api/v1`" + `. JSON field names are snake_case and carry their unit
  (` + "`_bpm`, `_ms`, `_kcal`, `_m`, `_min`, `_s`, `_h`, `_pct`" + `).
- ` + "`*_ms`" + ` fields are UTC epoch milliseconds (timeline data); ` + "`*_at`" + ` fields are RFC 3339 timestamps;
  ` + "`day`" + ` is a local calendar day ` + "`YYYY-MM-DD`" + ` in the server's time zone.
- Ranged reads take ` + "`period`" + ` (` + "`today`, `yesterday`, `last_7d`, `last_<n>d`, `this_week`, `last_week`, `this_month`,\n  `last_month`, `ytd`" + `, a day, or ` + "`FROM..TO`" + `) or ` + "`from`/`to`" + `.
- Collections are ` + "`{\"items\": [...]}`" + `; ranged ones add ` + "`from`/`to`" + `, capped ones ` + "`truncated`" + `.
- Errors are RFC 9457 problem details (` + "`application/problem+json`" + `).

## Authentication
- **Browser session**: the ` + "`ritm_session`" + ` cookie from ` + "`POST /api/v1/auth/login`" + `. State-changing
  requests must send ` + "`X-Ritm-CSRF: 1`" + `.
- **API key**: ` + "`Authorization: Bearer ritm_…`" + `, created under Settings. A key has one scope and can call
  only the operations listing that scope (` + "`x-ritm-scopes`" + `): ` + "`ingest`" + ` uploads exports and reads their status
  (e.g. from an iPhone Shortcut), ` + "`monitor`" + ` reads data freshness (Uptime Kuma), ` + "`mcp`" + ` is for the MCP server at
  ` + "`/mcp`" + ` only.`

func apiConfig(version string) huma.Config {
	cfg := huma.DefaultConfig("Ritm API", version)
	cfg.Info.Description = apiDescription
	cfg.OpenAPIPath = BasePath + "/openapi"
	cfg.DocsPath = BasePath + "/docs"
	cfg.SchemasPath = BasePath + "/schemas"
	cfg.CreateHooks = nil // no `$schema` fields or Link headers in responses
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		securitySession: {Type: "apiKey", In: "cookie", Name: auth.SessionCookie,
			Description: "Browser session from POST /api/v1/auth/login. Writes also need the header `X-Ritm-CSRF: 1`."},
		securityKey: {Type: "http", Scheme: "bearer", BearerFormat: "ritm_…",
			Description: "API key with a scope (`ingest`, `monitor`, `mcp`); see each operation's `x-ritm-scopes`."},
	}
	return cfg
}

const (
	securitySession = "session"
	securityKey     = "apiKey"
)

// access says who may call an operation: anyone, a signed-in browser session, and/or API keys
// with one of the listed scopes.
type access struct {
	session bool
	scopes  []string
}

var (
	public      = access{}
	sessionOnly = access{session: true}
)

func sessionOrKey(scopes ...string) access { return access{session: true, scopes: scopes} }
func keyOnly(scopes ...string) access      { return access{scopes: scopes} }

const accessKey = "ritm.access"

// describe documents and records op's access rules; the authenticate middleware enforces them.
func describe(op *huma.Operation, a access) {
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[accessKey] = a
	if a.session {
		op.Security = append(op.Security, map[string][]string{securitySession: {}})
	}
	if len(a.scopes) > 0 {
		op.Security = append(op.Security, map[string][]string{securityKey: {}})
		if op.Extensions == nil {
			op.Extensions = map[string]any{}
		}
		op.Extensions["x-ritm-scopes"] = a.scopes
		op.Description = strings.TrimSpace(op.Description + "\n\nAPI keys with scope `" + strings.Join(a.scopes, "`, `") + "` may call this operation.")
	}
	if a.session || len(a.scopes) > 0 {
		op.Errors = append(op.Errors, http.StatusUnauthorized, http.StatusForbidden)
	}
}

// register adds a JSON operation under BasePath with its access rules.
func register[I, O any](s *Server, a access, op huma.Operation, h func(context.Context, *I) (*O, error)) {
	describe(&op, a)
	huma.Register(s.v1, op, h)
}

// authenticate enforces each operation's access rules and puts the caller into the context:
// auth.UserFrom for every authenticated request, auth.APIKeyFrom for API keys.
func (s *Server) authenticate(ctx huma.Context, next func(huma.Context)) {
	ctx.SetHeader("Cache-Control", "no-store")
	a, _ := ctx.Operation().Metadata[accessKey].(access)
	if !a.session && len(a.scopes) == 0 {
		next(ctx)
		return
	}
	r, _ := humago.Unwrap(ctx)
	if token, ok := auth.BearerToken(r); ok {
		k, err := s.Auth.AuthenticateKey(ctx.Context(), httpx.ClientIP(r), token)
		switch {
		case errors.Is(err, auth.ErrRateLimited):
			ctx.SetHeader("Retry-After", "900")
			huma.WriteErr(s.api, ctx, http.StatusTooManyRequests, "too many invalid API keys, try again later")
			return
		case errors.Is(err, auth.ErrInvalidKey):
			ctx.SetHeader("WWW-Authenticate", `Bearer realm="ritm", error="invalid_token"`)
			huma.WriteErr(s.api, ctx, http.StatusUnauthorized, auth.ErrInvalidKey.Error())
			return
		case err != nil:
			s.Log.Error("key lookup failed", "err", err)
			huma.WriteErr(s.api, ctx, http.StatusInternalServerError, "key lookup failed")
			return
		}
		if !slices.Contains(a.scopes, k.Scope) {
			huma.WriteErr(s.api, ctx, http.StatusForbidden, auth.ErrWrongScope.Error())
			return
		}
		next(huma.WithContext(ctx, auth.WithUser(auth.WithAPIKey(ctx.Context(), k), k.User)))
		return
	}
	if !a.session {
		ctx.SetHeader("WWW-Authenticate", `Bearer realm="ritm"`)
		huma.WriteErr(s.api, ctx, http.StatusUnauthorized, "missing bearer token")
		return
	}
	u, err := s.Auth.SessionUser(ctx.Context(), auth.SessionToken(r))
	if errors.Is(err, auth.ErrNoSession) {
		huma.WriteErr(s.api, ctx, http.StatusUnauthorized, "authentication required")
		return
	} else if err != nil {
		s.Log.Error("session lookup failed", "err", err)
		huma.WriteErr(s.api, ctx, http.StatusInternalServerError, "session lookup failed")
		return
	}
	if err := auth.CheckCSRF(r, s.Cfg.BaseURL); err != nil {
		huma.WriteErr(s.api, ctx, http.StatusForbidden, err.Error())
		return
	}
	next(huma.WithContext(ctx, auth.WithUser(ctx.Context(), u)))
}

// user is the authenticated caller; only valid in operations that require authentication.
func user(ctx context.Context) auth.User {
	u, _ := auth.UserFrom(ctx)
	return u
}

// tenant opens (or reuses) the caller's database and services.
func (s *Server) tenant(ctx context.Context) (*tenant.Tenant, error) {
	t, err := s.Tenants.Get(ctx, user(ctx))
	if err != nil {
		return nil, s.problem(ctx, err)
	}
	return t, nil
}

// problem maps an internal error to an RFC 9457 problem; unexpected errors are logged.
func (s *Server) problem(ctx context.Context, err error) error {
	var se huma.StatusError
	switch {
	case errors.As(err, &se):
		return err
	case errors.Is(err, query.ErrNotFound):
		return huma.Error404NotFound("not found")
	case errors.Is(err, tenant.ErrClosed):
		return huma.Error503ServiceUnavailable(err.Error())
	case errors.Is(err, context.Canceled):
		return huma.Error503ServiceUnavailable("request canceled")
	}
	s.Log.Error("request failed", "err", err)
	return huma.Error500InternalServerError("internal error")
}

// Items is a collection.
type Items[T any] struct {
	Items []T `json:"items"`
}

// RangeItems is a collection of days (or events) in a day range.
type RangeItems[T any] struct {
	From  string `json:"from" format:"date" doc:"First day of the range"`
	To    string `json:"to" format:"date" doc:"Last day of the range"`
	Items []T    `json:"items"`
}

// body wraps a response body for Huma.
type body[T any] struct {
	Body T
}

func ok[T any](v T) *body[T] { return &body[T]{Body: v} }

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// contentSecurityPolicy allows only the app's own scripts and connections. Styles may be inline
// (Vue and ECharts set style attributes); map tiles come from OpenStreetMap (RouteMap.vue).
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob: https://tile.openstreetmap.org; connect-src 'self'; font-src 'self'; " +
	"worker-src 'self'; manifest-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

// docsSecurityPolicy is for the API reference page, which loads Stoplight Elements from unpkg.
const docsSecurityPolicy = "default-src 'self'; script-src 'self' https://unpkg.com; style-src 'self' 'unsafe-inline' https://unpkg.com; " +
	"img-src 'self' data: https:; connect-src 'self'; font-src 'self' data: https://unpkg.com; frame-ancestors 'none'"

func (s *Server) logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		if r.URL.Path == BasePath+"/docs" {
			w.Header().Set("Content-Security-Policy", docsSecurityPolicy)
		} else {
			w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		}
		next.ServeHTTP(sw, r)
		level := slog.LevelDebug
		if sw.status >= 500 {
			level = slog.LevelError
		} else if r.Method != http.MethodGet && r.Method != http.MethodHead {
			level = slog.LevelInfo
		}
		s.Log.Log(r.Context(), level, "http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"duration_ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.Log.Error("panic", "path", r.URL.Path, "err", v, "stack", string(debug.Stack()))
				httpx.Error(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
