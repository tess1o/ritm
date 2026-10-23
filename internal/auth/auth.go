// Package auth implements user accounts, browser sessions, login rate limiting and API keys.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"ritm/internal/clock"
	"ritm/internal/store"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrRateLimited        = errors.New("too many failed attempts, try again later")
	ErrNoSession          = errors.New("no valid session")
	ErrSuspended          = errors.New("account suspended")
)

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	DBName   string `json:"-"` // file name of the user's health-data database
}

type Service struct {
	db            *store.DB
	clock         clock.Clock
	sessionTTL    time.Duration
	limiter       *RateLimiter // failed logins and password re-checks, per IP
	userLimit     *RateLimiter // failed logins per username, which a spoofed IP cannot evade
	regLimiter    *RateLimiter // every registration attempt
	keyLimiter    *RateLimiter // tokens that match no API key, per IP
	staleKeyLimit *RateLimiter // revoked keys and keys of suspended users, per key

	keyUsed lastUsedThrottle
}

func NewService(db *store.DB, clk clock.Clock, sessionTTL time.Duration) *Service {
	return &Service{
		db:            db,
		clock:         clk,
		sessionTTL:    sessionTTL,
		limiter:       NewRateLimiter(clk, 10, 15*time.Minute),
		userLimit:     NewRateLimiter(clk, 10, 15*time.Minute),
		regLimiter:    NewRateLimiter(clk, 5, time.Hour),
		keyLimiter:    NewRateLimiter(clk, 20, 15*time.Minute),
		staleKeyLimit: NewRateLimiter(clk, 20, 15*time.Minute),
		keyUsed:       lastUsedThrottle{last: map[int64]time.Time{}},
	}
}

// Login verifies credentials and returns a new session token. A suspended account with the
// correct password gets ErrSuspended; anything else wrong is ErrInvalidCredentials.
func (s *Service) Login(ctx context.Context, ip, username, password string) (string, User, error) {
	name := normalizeUsername(username)
	if !s.limiter.Take(ip) {
		return "", User{}, ErrRateLimited
	}
	if !s.userLimit.Take(name) {
		s.limiter.Refund(ip)
		return "", User{}, ErrRateLimited
	}
	var (
		u         User
		hash      string
		suspended sql.NullInt64
	)
	err := s.db.W.QueryRowContext(ctx, "SELECT id, username, db_name, password_hash, suspended_at FROM users WHERE username = ?",
		name).Scan(&u.ID, &u.Username, &u.DBName, &hash, &suspended)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.limiter.Refund(ip)
		s.userLimit.Refund(name)
		return "", User{}, err
	}
	if hash == "" {
		// Burn comparable time so a missing user is indistinguishable.
		hash = dummyHash()
	}
	passOK := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	if !passOK || u.ID == 0 {
		return "", User{}, ErrInvalidCredentials
	}
	// Only this attempt is handed back: signing into one account must not clear the IP's earlier
	// failures against others.
	s.limiter.Refund(ip)
	s.userLimit.Reset(name)
	if suspended.Valid {
		return "", User{}, ErrSuspended
	}
	token, err := s.newSession(ctx, u.ID)
	if err != nil {
		return "", User{}, err
	}
	return token, u, nil
}

func (s *Service) newSession(ctx context.Context, userID int64) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	now := s.clock.Now()
	_, err = s.db.W.ExecContext(ctx, "INSERT INTO sessions (token_hash, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)",
		hashToken(token), userID, now.Add(s.sessionTTL).UnixMilli(), now.UnixMilli())
	if err != nil {
		return "", err
	}
	// Opportunistic cleanup of expired sessions.
	_, _ = s.db.W.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < ?", now.UnixMilli())
	return token, nil
}

func (s *Service) SessionUser(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrNoSession
	}
	var u User
	err := s.db.RO.QueryRowContext(ctx, `SELECT u.id, u.username, u.db_name FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ? AND u.suspended_at IS NULL`, hashToken(token), s.clock.Now().UnixMilli()).
		Scan(&u.ID, &u.Username, &u.DBName)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNoSession
	}
	return u, err
}

func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.db.W.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", hashToken(token))
	return err
}

func (s *Service) SessionTTL() time.Duration { return s.sessionTTL }

var dummyHash = sync.OnceValue(func() string {
	h, _ := bcrypt.GenerateFromPassword([]byte("ritm-dummy-password"), bcrypt.DefaultCost)
	return string(h)
})

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
