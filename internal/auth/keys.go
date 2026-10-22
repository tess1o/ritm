package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"ritm/internal/store"
)

const (
	ScopeMCP     = "mcp"     // the MCP server at /mcp
	ScopeMonitor = "monitor" // GET /api/v1/monitor/freshness (Uptime Kuma)
	ScopeIngest  = "ingest"  // upload exports and read their import status (e.g. iPhone Shortcuts)

	keyPrefix = "ritm_"
)

var (
	ErrInvalidKey = errors.New("invalid or revoked API key")
	ErrWrongScope = errors.New("API key scope does not allow this endpoint")
	ErrBadScope   = errors.New("scope must be 'mcp', 'monitor' or 'ingest'")
	ErrKeyName    = errors.New("key name must be 1–80 characters")
)

type APIKey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scope      string     `json:"scope" enum:"mcp,monitor,ingest"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`

	User User `json:"-"` // owner; set by VerifyKey
}

// Scopes lists the valid API key scopes.
var Scopes = []string{ScopeMCP, ScopeMonitor, ScopeIngest}

func ValidScope(s string) bool { return slices.Contains(Scopes, s) }

// CreateKey generates a key "ritm_" + base62(32 random bytes). The plaintext is returned once;
// only its SHA-256 hash and an 8-character prefix are stored.
func (s *Service) CreateKey(ctx context.Context, userID int64, name, scope string) (string, APIKey, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return "", APIKey{}, ErrKeyName
	}
	if !ValidScope(scope) {
		return "", APIKey{}, ErrBadScope
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", APIKey{}, fmt.Errorf("random: %w", err)
	}
	plain := keyPrefix + base62(b)
	now := s.clock.Now()
	k := APIKey{Name: name, Prefix: plain[:len(keyPrefix)+8], Scope: scope, CreatedAt: now}
	res, err := s.db.W.ExecContext(ctx, "INSERT INTO api_keys (user_id, name, prefix, scope, key_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		userID, k.Name, k.Prefix, k.Scope, hashToken(plain), now.UnixMilli())
	if err != nil {
		return "", APIKey{}, err
	}
	k.ID, _ = res.LastInsertId()
	return plain, k, nil
}

func (s *Service) ListKeys(ctx context.Context, userID int64) ([]APIKey, error) {
	rows, err := s.db.RO.QueryContext(ctx, `SELECT id, name, prefix, scope, created_at, last_used_at, revoked_at FROM api_keys
		WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []APIKey{}
	for rows.Next() {
		var (
			k             APIKey
			created       int64
			used, revoked sql.NullInt64
		)
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.Scope, &created, &used, &revoked); err != nil {
			return nil, err
		}
		k.CreatedAt = time.UnixMilli(created)
		k.LastUsedAt = store.MSTime(used)
		k.RevokedAt = store.MSTime(revoked)
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (s *Service) RevokeKey(ctx context.Context, userID, id int64) error {
	res, err := s.db.W.ExecContext(ctx, "UPDATE api_keys SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL",
		s.clock.Now().UnixMilli(), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// VerifyKey resolves a bearer token to an active key of a non-suspended user and records its use
// (throttled to once a minute).
func (s *Service) VerifyKey(ctx context.Context, token string) (APIKey, error) {
	if !strings.HasPrefix(token, keyPrefix) {
		return APIKey{}, ErrInvalidKey
	}
	var (
		k       APIKey
		created int64
		used    sql.NullInt64
	)
	err := s.db.RO.QueryRowContext(ctx, `SELECT k.id, k.name, k.prefix, k.scope, k.created_at, k.last_used_at, u.id, u.username, u.db_name
		FROM api_keys k JOIN users u ON u.id = k.user_id
		WHERE k.key_hash = ? AND k.revoked_at IS NULL AND u.suspended_at IS NULL`,
		hashToken(token)).Scan(&k.ID, &k.Name, &k.Prefix, &k.Scope, &created, &used, &k.User.ID, &k.User.Username, &k.User.DBName)
	if errors.Is(err, sql.ErrNoRows) {
		return APIKey{}, ErrInvalidKey
	}
	if err != nil {
		return APIKey{}, err
	}
	k.CreatedAt = time.UnixMilli(created)
	k.LastUsedAt = store.MSTime(used)
	now := s.clock.Now()
	if s.keyUsed.due(k.ID, now) {
		_, _ = s.db.W.ExecContext(ctx, "UPDATE api_keys SET last_used_at = ? WHERE id = ?", now.UnixMilli(), k.ID)
	}
	return k, nil
}

type lastUsedThrottle struct {
	mu   sync.Mutex
	last map[int64]time.Time
}

func (t *lastUsedThrottle) due(id int64, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if prev, ok := t.last[id]; ok && now.Sub(prev) < time.Minute {
		return false
	}
	t.last[id] = now
	return true
}

const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func base62(b []byte) string {
	n := new(big.Int).SetBytes(b)
	base := big.NewInt(62)
	mod := new(big.Int)
	var out []byte
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		out = append(out, base62Alphabet[mod.Int64()])
	}
	// Pad to a fixed length (43 chars covers 256 bits) so every key looks the same.
	for len(out) < 43 {
		out = append(out, '0')
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}
