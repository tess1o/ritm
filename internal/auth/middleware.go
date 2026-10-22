package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ritm/internal/httpx"
)

const (
	SessionCookie = "ritm_session"
	CSRFHeader    = "X-Ritm-CSRF"
)

type ctxKey int

const (
	userKey ctxKey = iota
	apiKeyKey
)

func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userKey).(User)
	return u, ok
}

func APIKeyFrom(ctx context.Context) (APIKey, bool) {
	k, ok := ctx.Value(apiKeyKey).(APIKey)
	return k, ok
}

// SessionCookie is the session cookie for token (AUTH-1).
func (s *Service) SessionCookie(token string, secure bool) http.Cookie {
	return http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  s.clock.Now().Add(s.sessionTTL),
		MaxAge:   int(s.sessionTTL / time.Second),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// ClearedSessionCookie removes the session cookie from the browser.
func ClearedSessionCookie(secure bool) http.Cookie {
	return http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode}
}

func SessionToken(r *http.Request) string {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// AuthenticateKey resolves a bearer token to an active key. An IP that sends 20 invalid keys
// within 15 minutes gets ErrRateLimited until the window passes.
func (s *Service) AuthenticateKey(ctx context.Context, ip, token string) (APIKey, error) {
	if !s.keyLimiter.Allow(ip) {
		return APIKey{}, ErrRateLimited
	}
	k, err := s.VerifyKey(ctx, token)
	if errors.Is(err, ErrInvalidKey) {
		s.keyLimiter.Fail(ip)
	}
	return k, err
}

// WithAPIKey returns ctx carrying the authenticated key (see APIKeyFrom).
func WithAPIKey(ctx context.Context, k APIKey) context.Context {
	return context.WithValue(ctx, apiKeyKey, k)
}

// WithUser returns ctx carrying the signed-in user (see UserFrom).
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// RequireKey authenticates "Authorization: Bearer <key>" and checks the scope:
// missing/invalid key → 401, other scope → 403 (MON-4). Used for /mcp; REST operations declare
// their scopes in internal/api.
func (s *Service) RequireKey(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := BearerToken(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="ritm"`)
			httpx.Error(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		k, err := s.AuthenticateKey(r.Context(), httpx.ClientIP(r), token)
		switch {
		case errors.Is(err, ErrRateLimited):
			w.Header().Set("Retry-After", "900")
			httpx.Error(w, http.StatusTooManyRequests, "too many invalid API keys, try again later")
			return
		case errors.Is(err, ErrInvalidKey):
			w.Header().Set("WWW-Authenticate", `Bearer realm="ritm", error="invalid_token"`)
			httpx.Error(w, http.StatusUnauthorized, ErrInvalidKey.Error())
			return
		case err != nil:
			httpx.Error(w, http.StatusInternalServerError, "key lookup failed")
			return
		}
		if k.Scope != scope {
			httpx.Error(w, http.StatusForbidden, ErrWrongScope.Error())
			return
		}
		next.ServeHTTP(w, r.WithContext(WithAPIKey(r.Context(), k)))
	})
}

func BearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) <= len(p) || !strings.EqualFold(h[:len(p)], p) {
		return "", false
	}
	return strings.TrimSpace(h[len(p):]), true
}

// ErrCSRF is returned by CheckCSRF for a state-changing request that lacks the CSRF header or
// comes from another origin.
var ErrCSRF = errors.New("missing CSRF header or cross-origin request")

// CheckCSRF requires the X-Ritm-CSRF header on state-changing requests and, when an Origin header
// is present, that it matches the request host or the configured base URL (AUTH-3).
func CheckCSRF(r *http.Request, baseURL string) error {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}
	if r.Header.Get(CSRFHeader) != "1" {
		return ErrCSRF
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		var baseHost string
		if u, err := url.Parse(baseURL); err == nil {
			baseHost = u.Host
		}
		u, err := url.Parse(origin)
		if err != nil || (u.Host != r.Host && (baseHost == "" || u.Host != baseHost)) {
			return ErrCSRF
		}
	}
	return nil
}
