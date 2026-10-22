package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"ritm/internal/store"
	"ritm/internal/uid"
)

var (
	ErrUsernameTaken = errors.New("username is already taken")
	ErrUserNotFound  = errors.New("user not found")
	ErrBadUsername   = errors.New("username must be 3–32 characters: lowercase letters, digits, '.', '_' or '-', starting with a letter or digit")
	ErrBadPassword   = errors.New("password must be 8–72 bytes long")
	ErrWrongPassword = errors.New("current password is incorrect")
)

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,31}$`)

func normalizeUsername(u string) string { return strings.ToLower(strings.TrimSpace(u)) }

// ValidateUsername returns the normalised (trimmed, lowercased) username or ErrBadUsername.
func ValidateUsername(u string) (string, error) {
	u = normalizeUsername(u)
	if !usernameRe.MatchString(u) {
		return "", ErrBadUsername
	}
	return u, nil
}

// ValidatePassword enforces bcrypt's 72-byte limit and a minimum length.
func ValidatePassword(p string) error {
	if len(p) < 8 || len(p) > 72 {
		return ErrBadPassword
	}
	return nil
}

// UserInfo is a user row as listed by the CLI.
type UserInfo struct {
	User
	CreatedAt   time.Time
	SuspendedAt *time.Time
}

// CreateUser validates the credentials and inserts a user with a fresh database name.
func (s *Service) CreateUser(ctx context.Context, username, password string) (User, error) {
	name, err := ValidateUsername(username)
	if err != nil {
		return User{}, err
	}
	if err := ValidatePassword(password); err != nil {
		return User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	u := User{Username: name, DBName: uid.New()}
	now := s.clock.Now().UnixMilli()
	res, err := s.db.W.ExecContext(ctx, `INSERT INTO users (username, password_hash, db_name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`, u.Username, string(hash), u.DBName, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: users.username") {
			return User{}, ErrUsernameTaken
		}
		return User{}, err
	}
	u.ID, _ = res.LastInsertId()
	return u, nil
}

// EnsureUser creates the user if no user with that name exists; an existing user is left untouched.
func (s *Service) EnsureUser(ctx context.Context, username, password string) (created bool, err error) {
	if _, err := s.UserByName(ctx, username); err == nil {
		return false, nil
	} else if !errors.Is(err, ErrUserNotFound) {
		return false, err
	}
	if _, err := s.CreateUser(ctx, username, password); err != nil {
		return false, err
	}
	return true, nil
}

// Register creates an account on behalf of a visitor and starts a session. Every attempt counts
// against the per-IP registration limit.
func (s *Service) Register(ctx context.Context, ip, username, password string) (string, User, error) {
	if !s.regLimiter.Allow(ip) {
		return "", User{}, ErrRateLimited
	}
	s.regLimiter.Fail(ip)
	u, err := s.CreateUser(ctx, username, password)
	if err != nil {
		return "", User{}, err
	}
	token, err := s.newSession(ctx, u.ID)
	if err != nil {
		return "", User{}, err
	}
	return token, u, nil
}

// CheckPassword re-authenticates a signed-in user before a sensitive action. Failures count
// against the login rate limit of ip.
func (s *Service) CheckPassword(ctx context.Context, ip string, userID int64, password string) error {
	if !s.limiter.Allow(ip) {
		return ErrRateLimited
	}
	var hash string
	if err := s.db.W.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id = ?", userID).Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		s.limiter.Fail(ip)
		return ErrWrongPassword
	}
	return nil
}

// SetPassword replaces the user's password and ends all their sessions except keepToken's
// (pass "" to end every session).
func (s *Service) SetPassword(ctx context.Context, userID int64, password, keepToken string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return store.InTx(ctx, s.db.W, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?",
			string(hash), s.clock.Now().UnixMilli(), userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrUserNotFound
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?", userID, hashToken(keepToken))
		return err
	})
}

// SetSuspended suspends or reactivates a user. Suspending ends all their sessions; their API keys
// stop working while suspended.
func (s *Service) SetSuspended(ctx context.Context, userID int64, suspended bool) error {
	var at any
	if suspended {
		at = s.clock.Now().UnixMilli()
	}
	return store.InTx(ctx, s.db.W, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE users SET suspended_at = ?, updated_at = ? WHERE id = ?", at, s.clock.Now().UnixMilli(), userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrUserNotFound
		}
		if suspended {
			_, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", userID)
		}
		return err
	})
}

// DeleteUser removes the account; sessions and API keys cascade. The caller removes the data file.
func (s *Service) DeleteUser(ctx context.Context, userID int64) error {
	res, err := s.db.W.ExecContext(ctx, "DELETE FROM users WHERE id = ?", userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SetDBName points the user at a different health-data database (used to wipe their data).
func (s *Service) SetDBName(ctx context.Context, userID int64, dbName string) error {
	res, err := s.db.W.ExecContext(ctx, "UPDATE users SET db_name = ?, updated_at = ? WHERE id = ?", dbName, s.clock.Now().UnixMilli(), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (s *Service) UserByName(ctx context.Context, username string) (UserInfo, error) {
	list, err := s.listUsers(ctx, "WHERE username = ?", normalizeUsername(username))
	if err != nil {
		return UserInfo{}, err
	}
	if len(list) == 0 {
		return UserInfo{}, ErrUserNotFound
	}
	return list[0], nil
}

func (s *Service) ListUsers(ctx context.Context) ([]UserInfo, error) {
	return s.listUsers(ctx, "ORDER BY id")
}

func (s *Service) listUsers(ctx context.Context, tail string, args ...any) ([]UserInfo, error) {
	rows, err := s.db.W.QueryContext(ctx, "SELECT id, username, db_name, created_at, suspended_at FROM users "+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserInfo
	for rows.Next() {
		var (
			u         UserInfo
			created   int64
			suspended sql.NullInt64
		)
		if err := rows.Scan(&u.ID, &u.Username, &u.DBName, &created, &suspended); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		u.CreatedAt = time.UnixMilli(created)
		u.SuspendedAt = store.MSTime(suspended)
		out = append(out, u)
	}
	return out, rows.Err()
}
