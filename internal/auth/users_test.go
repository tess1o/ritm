package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ritm/internal/clock"
	"ritm/internal/store"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	db, err := store.OpenApp(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewService(db, clock.NewFake(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)), time.Hour)
}

func mustCreate(t *testing.T, s *Service, name string) User {
	t.Helper()
	u, err := s.CreateUser(context.Background(), name, "password1")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestEnsureUserCreatesOnceAndNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	if created, err := s.EnsureUser(ctx, "Admin", "password1"); err != nil || !created {
		t.Fatalf("first EnsureUser: created=%v err=%v", created, err)
	}
	if created, err := s.EnsureUser(ctx, "admin", "different-pw"); err != nil || created {
		t.Fatalf("second EnsureUser: created=%v err=%v", created, err)
	}
	if _, _, err := s.Login(ctx, "ip", "admin", "password1"); err != nil {
		t.Fatalf("original password must still work: %v", err)
	}
	if _, _, err := s.Login(ctx, "ip", "admin", "different-pw"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("env password must not overwrite: %v", err)
	}
}

func TestCreateUserValidation(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	mustCreate(t, s, "alice")
	cases := []struct {
		name, pw string
		want     error
	}{
		{"ALICE", "password1", ErrUsernameTaken},
		{"al", "password1", ErrBadUsername},
		{"bob smith", "password1", ErrBadUsername},
		{"bob", "short", ErrBadPassword},
	}
	for _, c := range cases {
		if _, err := s.CreateUser(ctx, c.name, c.pw); !errors.Is(err, c.want) {
			t.Errorf("CreateUser(%q): %v, want %v", c.name, err, c.want)
		}
	}
	a, b := mustCreate(t, s, "bob"), mustCreate(t, s, "carol")
	if a.DBName == b.DBName || len(a.DBName) != 36 {
		t.Fatalf("db names must be distinct UUIDs: %q %q", a.DBName, b.DBName)
	}
}

func TestSuspendBlocksLoginSessionsAndKeys(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	u := mustCreate(t, s, "alice")
	token, _, err := s.Login(ctx, "ip", "alice", "password1")
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := s.CreateKey(ctx, u.ID, "k", ScopeRead)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSuspended(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("session after suspend: %v", err)
	}
	if _, err := s.VerifyKey(ctx, key); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("key after suspend: %v", err)
	}
	if _, _, err := s.Login(ctx, "ip", "alice", "password1"); !errors.Is(err, ErrSuspended) {
		t.Fatalf("login with right password: %v, want ErrSuspended", err)
	}
	if _, _, err := s.Login(ctx, "ip", "alice", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("login with wrong password must not reveal suspension: %v", err)
	}
	if err := s.SetSuspended(ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	if k, err := s.VerifyKey(ctx, key); err != nil || k.User.ID != u.ID {
		t.Fatalf("key after unsuspend: %+v %v", k, err)
	}
}

func TestSetPasswordKeepsOnlyCurrentSession(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	u := mustCreate(t, s, "alice")
	t1, _, _ := s.Login(ctx, "ip", "alice", "password1")
	t2, _, _ := s.Login(ctx, "ip", "alice", "password1")
	if err := s.CheckPassword(ctx, "ip", u.ID, "nope-nope"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("CheckPassword wrong: %v", err)
	}
	if err := s.SetPassword(ctx, u.ID, "password2", t1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, t1); err != nil {
		t.Fatalf("current session must survive: %v", err)
	}
	if _, err := s.SessionUser(ctx, t2); !errors.Is(err, ErrNoSession) {
		t.Fatalf("other session must end: %v", err)
	}
	if _, _, err := s.Login(ctx, "ip", "alice", "password2"); err != nil {
		t.Fatalf("login with new password: %v", err)
	}
}

func TestKeysAreScopedToTheirUser(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	a, b := mustCreate(t, s, "alice"), mustCreate(t, s, "bob")
	_, k, err := s.CreateKey(ctx, a.ID, "k", ScopeMonitor)
	if err != nil {
		t.Fatal(err)
	}
	if keys, _ := s.ListKeys(ctx, b.ID); len(keys) != 0 {
		t.Fatalf("bob sees %d keys", len(keys))
	}
	if err := s.RevokeKey(ctx, b.ID, k.ID); err == nil {
		t.Fatal("bob revoked alice's key")
	}
	if err := s.RevokeKey(ctx, a.ID, k.ID); err != nil {
		t.Fatalf("alice revokes own key: %v", err)
	}
}

func TestDeleteUserCascades(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	u := mustCreate(t, s, "alice")
	token, _, _ := s.Login(ctx, "ip", "alice", "password1")
	key, _, _ := s.CreateKey(ctx, u.ID, "k", ScopeRead)
	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("session after delete: %v", err)
	}
	if _, err := s.VerifyKey(ctx, key); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("key after delete: %v", err)
	}
	if _, err := s.UserByName(ctx, "alice"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("user after delete: %v", err)
	}
}

// Rotating client IPs (e.g. spoofed X-Forwarded-For) does not evade the per-username limit.
func TestLoginLimitPerUsername(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	mustCreate(t, s, "alice")
	for i := range 10 {
		ip := fmt.Sprintf("10.0.0.%d", i+1)
		if _, _, err := s.Login(ctx, ip, "alice", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	if _, _, err := s.Login(ctx, "10.0.1.1", "Alice", "password1"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("after 10 failures from different IPs: %v, want ErrRateLimited", err)
	}
	s.clock.(*clock.Fake).Advance(16 * time.Minute)
	if _, _, err := s.Login(ctx, "10.0.1.1", "alice", "password1"); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

func TestRateLimiterIsBounded(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	l := NewRateLimiter(clk, 3, time.Minute)
	for i := range maxLimiterKeys + 500 {
		l.Fail(fmt.Sprint(i))
	}
	if n := len(l.failures); n > maxLimiterKeys {
		t.Fatalf("%d keys, want at most %d", n, maxLimiterKeys)
	}
}

// Parallel attempts cannot all pass the check before any failure is recorded.
func TestLoginLimitUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	mustCreate(t, s, "alice")
	var wg sync.WaitGroup
	var invalid atomic.Int32
	for i := range 30 {
		wg.Go(func() {
			_, _, err := s.Login(ctx, "10.0.0.1", fmt.Sprintf("user%d", i), "wrong-password")
			if errors.Is(err, ErrInvalidCredentials) {
				invalid.Add(1)
			}
		})
	}
	wg.Wait()
	if n := invalid.Load(); n != 10 {
		t.Fatalf("%d password checks from one IP, want 10", n)
	}
}

// Signing into one account does not clear the IP's failures against other accounts.
func TestLoginSuccessKeepsIPFailures(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	mustCreate(t, s, "alice")
	for i := range 9 {
		s.Login(ctx, "10.0.0.1", fmt.Sprintf("user%d", i), "wrong-password")
	}
	if _, _, err := s.Login(ctx, "10.0.0.1", "alice", "password1"); err != nil {
		t.Fatalf("correct password: %v", err)
	}
	if _, _, err := s.Login(ctx, "10.0.0.1", "bob", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("10th failure: %v", err)
	}
	if _, _, err := s.Login(ctx, "10.0.0.1", "alice", "password1"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("after 10 failures: %v, want ErrRateLimited", err)
	}
}
