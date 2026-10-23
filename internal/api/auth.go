package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"ritm/internal/auth"
	"ritm/internal/httpx"
	"ritm/internal/tenant"
)

// AuthConfig tells the login screen what it can offer.
type AuthConfig struct {
	RegistrationEnabled bool `json:"registration_enabled" doc:"Visitors may create accounts (RITM_REGISTRATION_ENABLED)"`
}

// Credentials sign in or register a user.
type Credentials struct {
	Username string `json:"username" minLength:"1" maxLength:"64" example:"alice"`
	Password string `json:"password" minLength:"1" maxLength:"72"`
	Timezone string `json:"timezone,omitempty" maxLength:"64" example:"Europe/Berlin" doc:"The browser's IANA time zone; becomes the user's time zone when none is set yet"`
}

// LocalClock is the user's time zone and today, so clients show local days as the server
// computes them.
type LocalClock struct {
	Timezone string `json:"timezone" example:"Europe/Kyiv" doc:"IANA time zone of the user's local days (Settings, else RITM_TZ)"`
	Today    string `json:"today" format:"date" doc:"Today in that zone"`
}

// Session is a new browser session.
type Session struct {
	LocalClock
	User      auth.User `json:"user"`
	ExpiresAt time.Time `json:"expires_at"`
}

type sessionOutput struct {
	Status    int
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      Session
}

type cookieOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

// httpRequest gives an operation the underlying request (client IP, cookies, TLS).
type httpRequest struct {
	req *http.Request
}

func (h *httpRequest) Resolve(ctx huma.Context) []error {
	h.req, _ = humago.Unwrap(ctx)
	return nil
}

type credentialsInput struct {
	httpRequest
	Body Credentials
}

// checkCSRF applies the session CSRF rule to the public writes (sign-in, registration): Huma
// reads a body without a Content-Type as JSON, so a cross-site page could otherwise post one.
func (s *Server) checkCSRF(r *http.Request) error {
	if err := auth.CheckCSRF(r, s.Cfg.BaseURL); err != nil {
		return huma.Error403Forbidden(err.Error())
	}
	return nil
}

func (s *Server) secure(r *http.Request) bool { return s.Cfg.SecureCookies() || r.TLS != nil }

func (s *Server) clock(t *tenant.Tenant) LocalClock {
	return LocalClock{Timezone: t.Zone.Location().String(), Today: t.Zone.Today(s.Clock.Now()).String()}
}

// authProblem maps account errors to problems.
func (s *Server) authProblem(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, auth.ErrRateLimited):
		return huma.ErrorWithHeaders(huma.Error429TooManyRequests(err.Error()), http.Header{"Retry-After": {"900"}})
	case errors.Is(err, auth.ErrInvalidCredentials):
		return huma.Error401Unauthorized(err.Error())
	case errors.Is(err, auth.ErrSuspended), errors.Is(err, auth.ErrWrongPassword):
		return huma.Error403Forbidden(err.Error())
	case errors.Is(err, auth.ErrUsernameTaken):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, auth.ErrBadUsername), errors.Is(err, auth.ErrBadPassword), errors.Is(err, auth.ErrBadScope), errors.Is(err, auth.ErrKeyName):
		return huma.Error400BadRequest(err.Error())
	}
	return s.problem(ctx, err)
}

// newSession answers a sign-in. The user's tenant is opened here, and adopts the browser's time
// zone when the user has none yet.
func (s *Server) newSession(ctx context.Context, in *credentialsInput, status int, token string, u auth.User) (*sessionOutput, error) {
	t, err := s.Tenants.Get(ctx, u)
	if err != nil {
		return nil, s.problem(ctx, err)
	}
	if err := t.AdoptTimezone(ctx, in.Body.Timezone); err != nil {
		s.Log.Warn("save browser time zone", "user", u.Username, "err", err)
	}
	return &sessionOutput{Status: status, SetCookie: s.Auth.SessionCookie(token, s.secure(in.req)),
		Body: Session{LocalClock: s.clock(t), User: u, ExpiresAt: s.Clock.Now().Add(s.Auth.SessionTTL())}}, nil
}

func (s *Server) registerAuth() {
	tags := []string{"Authentication"}
	register(s, public, huma.Operation{OperationID: "getAuthConfig", Method: http.MethodGet, Path: "/auth/config", Tags: tags,
		Summary: "Login options", Description: "Whether the login screen may offer registration."},
		func(ctx context.Context, _ *struct{}) (*body[AuthConfig], error) {
			return ok(AuthConfig{RegistrationEnabled: s.Cfg.RegistrationEnabled}), nil
		})

	register(s, public, huma.Operation{OperationID: "login", Method: http.MethodPost, Path: "/auth/login", Tags: tags,
		Summary: "Sign in", Description: "Starts a browser session (cookie `ritm_session`). 10 failures per IP or username in 15 minutes lock sign-in for that window.",
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests}},
		func(ctx context.Context, in *credentialsInput) (*sessionOutput, error) {
			if err := s.checkCSRF(in.req); err != nil {
				return nil, err
			}
			token, u, err := s.Auth.Login(ctx, httpx.ClientIP(in.req), in.Body.Username, in.Body.Password)
			if err != nil {
				return nil, s.authProblem(ctx, err)
			}
			return s.newSession(ctx, in, http.StatusOK, token, u)
		})

	register(s, public, huma.Operation{OperationID: "register", Method: http.MethodPost, Path: "/auth/register", Tags: tags,
		Summary: "Create an account", DefaultStatus: http.StatusCreated,
		Description: "Creates an account and signs it in. Only when RITM_REGISTRATION_ENABLED is set, otherwise 404. Username: 3–32 of `a-z 0-9 . _ -`; password: 8–72 bytes.",
		Errors:      []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusTooManyRequests}},
		func(ctx context.Context, in *credentialsInput) (*sessionOutput, error) {
			if !s.Cfg.RegistrationEnabled {
				return nil, huma.Error404NotFound("registration is disabled")
			}
			if err := s.checkCSRF(in.req); err != nil {
				return nil, err
			}
			token, u, err := s.Auth.Register(ctx, httpx.ClientIP(in.req), in.Body.Username, in.Body.Password)
			if err != nil {
				return nil, s.authProblem(ctx, err)
			}
			s.Log.Info("user registered", "user", u.Username)
			return s.newSession(ctx, in, http.StatusCreated, token, u)
		})

	register(s, sessionOnly, huma.Operation{OperationID: "logout", Method: http.MethodPost, Path: "/auth/logout", Tags: tags,
		Summary: "Sign out", DefaultStatus: http.StatusNoContent, Description: "Ends this browser session."},
		func(ctx context.Context, in *httpRequest) (*cookieOutput, error) {
			if err := s.Auth.Logout(ctx, auth.SessionToken(in.req)); err != nil {
				return nil, s.problem(ctx, err)
			}
			return &cookieOutput{SetCookie: auth.ClearedSessionCookie(s.secure(in.req))}, nil
		})
}
