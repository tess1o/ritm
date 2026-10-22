// Package tenant owns each user's health-data database and the services built on it, and the
// registry that opens them on demand, wipes them and deletes them.
package tenant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"ritm/internal/auth"
	"ritm/internal/clock"
	"ritm/internal/config"
	"ritm/internal/days"
	"ritm/internal/derive"
	"ritm/internal/ingest"
	"ritm/internal/insights"
	"ritm/internal/query"
	"ritm/internal/store"
	"ritm/internal/uid"
)

// Tenant is one user's open database and services. Imports is nil outside the server.
type Tenant struct {
	User    auth.User
	DB      *store.DB
	Derive  *derive.Engine
	Query   *query.Service
	Imports *ingest.Manager
	// Zone is the user's time zone (Settings.Timezone, else RITM_TZ), shared by the services above.
	Zone *days.Zone

	cfg   *config.Config
	log   *slog.Logger
	clock clock.Clock

	memoMu sync.Mutex
	memo   map[any]any
}

// Open opens (creating and migrating if needed) u's database and builds the derived-data,
// insights and query services on it.
func Open(ctx context.Context, cfg *config.Config, log *slog.Logger, clk clock.Clock, u auth.User) (*Tenant, error) {
	db, err := store.OpenUser(ctx, cfg.DataDir, u.DBName)
	if err != nil {
		return nil, fmt.Errorf("open data of %s: %w", u.Username, err)
	}
	st, err := derive.LoadSettings(ctx, db.RO)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("read settings of %s: %w", u.Username, err)
	}
	zone := days.NewZone(st.Location(cfg.TZ))
	log = log.With("user", u.Username)
	eng := derive.NewEngine(db, zone, clk, log)
	eng.OnRecomputed(insights.NewEngine(db, zone, clk).Generate)
	return &Tenant{User: u, DB: db, Derive: eng, Query: query.New(db, zone, clk, eng), Zone: zone, cfg: cfg, log: log, clock: clk}, nil
}

// IngestOptions are the import options for this user.
func (t *Tenant) IngestOptions() ingest.Options {
	return ingest.Options{DeltaWindow: t.cfg.DeltaWindow, Loc: t.Zone.Location(), Logger: t.log, Clock: t.clock}
}

// UseSettings switches the services to the time zone of newly saved settings.
func (t *Tenant) UseSettings(st derive.Settings) {
	t.Zone.Set(st.Location(t.cfg.TZ))
}

// AdoptTimezone saves name (the browser's zone, sent at sign-in) as the user's time zone unless
// one is set already or name is not a valid zone. When that moves local days, derived data and
// insights are recomputed in the background.
func (t *Tenant) AdoptTimezone(ctx context.Context, name string) error {
	if _, err := derive.LoadTimezone(name); err != nil {
		return nil
	}
	st, err := derive.LoadSettings(ctx, t.DB.RO)
	if err != nil || st.Timezone != "" {
		return err
	}
	st.Timezone = name
	if err := derive.SaveSettings(ctx, t.DB, st); err != nil {
		return err
	}
	prev := t.Zone.Location()
	t.UseSettings(st)
	if t.Zone.Location().String() != prev.String() && t.Imports != nil {
		t.Imports.Go(func(ctx context.Context) {
			if err := t.Derive.Recompute(ctx, ""); err != nil {
				t.log.Error("recompute after time-zone change failed", "err", err)
			}
		})
	}
	return nil
}

// Run runs request-triggered background work, such as a recompute after a settings change, so
// that closing the tenant (shutdown, data reset, account deletion) cancels it and waits for it.
func (t *Tenant) Run(fn func(ctx context.Context) error) error {
	if t.Imports == nil {
		return fn(context.Background())
	}
	return t.Imports.Run(fn)
}

// Memo returns the value cached under key, building it on first use. Services built on the
// tenant (such as its MCP server) live exactly as long as the tenant.
func (t *Tenant) Memo(key any, build func() any) any {
	t.memoMu.Lock()
	defer t.memoMu.Unlock()
	if v, ok := t.memo[key]; ok {
		return v
	}
	if t.memo == nil {
		t.memo = map[any]any{}
	}
	v := build()
	t.memo[key] = v
	return v
}

// Close cancels a running import, waits for it to stop and closes the database.
func (t *Tenant) Close(ctx context.Context) error {
	if t.Imports != nil {
		t.Imports.Shutdown(ctx)
	}
	t.Query.Wait()
	return t.DB.Close()
}

// Registry caches the open tenants of the server, keyed by user id.
type Registry struct {
	cfg   *config.Config
	log   *slog.Logger
	clock clock.Clock
	auth  *auth.Service
	slots chan struct{} // server-wide import slots

	mu     sync.Mutex
	m      map[int64]*Tenant
	closed bool
}

// ErrClosed is returned by Get once the server is shutting down.
var ErrClosed = errors.New("server is shutting down")

func NewRegistry(cfg *config.Config, log *slog.Logger, clk clock.Clock, authSvc *auth.Service) *Registry {
	return &Registry{cfg: cfg, log: log, clock: clk, auth: authSvc,
		slots: make(chan struct{}, cfg.MaxConcurrentImports), m: map[int64]*Tenant{}}
}

func (r *Registry) tmpRoot() string { return filepath.Join(r.cfg.DataDir, "tmp") }

// OpenAll clears leftover upload files and opens every user's database at startup, so pending
// migrations fail fast and imports interrupted by a restart are marked failed.
func (r *Registry) OpenAll(ctx context.Context) error {
	if err := os.RemoveAll(r.tmpRoot()); err != nil {
		return err
	}
	users, err := r.auth.ListUsers(ctx)
	if err != nil {
		return err
	}
	for _, u := range users {
		if _, err := r.Get(ctx, u.User); err != nil {
			return err
		}
	}
	return nil
}

// Get returns u's tenant, opening it on first use. A cached tenant whose database name no longer
// matches (the user's data was reset) is replaced.
func (r *Registry) Get(ctx context.Context, u auth.User) (*Tenant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrClosed
	}
	if t, ok := r.m[u.ID]; ok {
		if t.User.DBName == u.DBName {
			return t, nil
		}
		delete(r.m, u.ID)
		go r.close(t)
	}
	t, err := Open(ctx, r.cfg, r.log, r.clock, u)
	if err != nil {
		return nil, err
	}
	t.Imports = ingest.NewManager(t.DB, t.log, filepath.Join(r.tmpRoot(), u.DBName), t.IngestOptions, t.Derive.AfterImport, r.slots)
	if err := t.Imports.Recover(ctx); err != nil {
		t.DB.Close()
		return nil, fmt.Errorf("recover imports of %s: %w", u.Username, err)
	}
	// Finish a recompute that failed or was interrupted (e.g. by a restart during an import).
	if pending, err := t.Derive.HasPending(ctx); err != nil {
		t.DB.Close()
		return nil, err
	} else if pending {
		t.Imports.Go(func(ctx context.Context) {
			if err := t.Derive.AfterImport(ctx, ""); err != nil {
				t.log.Error("pending recompute failed", "err", err)
			}
		})
	}
	r.m[u.ID] = t
	return t, nil
}

// drop evicts and closes the user's tenant, if open.
func (r *Registry) drop(userID int64) {
	r.mu.Lock()
	t, ok := r.m[userID]
	delete(r.m, userID)
	r.mu.Unlock()
	if ok {
		r.close(t)
	}
}

func (r *Registry) close(t *Tenant) {
	if err := t.Close(context.Background()); err != nil {
		r.log.Warn("close tenant", "user", t.User.Username, "err", err)
	}
}

// ResetData replaces the user's database with an empty one under a new name and deletes the old
// file. It returns the updated user.
func (r *Registry) ResetData(ctx context.Context, u auth.User) (auth.User, error) {
	next := u
	next.DBName = uid.New()
	db, err := store.OpenUser(ctx, r.cfg.DataDir, next.DBName)
	if err != nil {
		return u, err
	}
	db.Close()
	if err := r.auth.SetDBName(ctx, u.ID, next.DBName); err != nil {
		store.RemoveFiles(store.UserPath(r.cfg.DataDir, next.DBName))
		return u, err
	}
	r.drop(u.ID)
	r.removeData(u)
	return next, nil
}

// DeleteUser deletes the account and the user's data. The database name is never reused, so a
// file left behind by a failed removal cannot surface again.
func (r *Registry) DeleteUser(ctx context.Context, u auth.User) error {
	r.drop(u.ID)
	if err := r.auth.DeleteUser(ctx, u.ID); err != nil {
		return err
	}
	r.removeData(u)
	return nil
}

func (r *Registry) removeData(u auth.User) {
	err := errors.Join(store.RemoveFiles(store.UserPath(r.cfg.DataDir, u.DBName)),
		os.RemoveAll(filepath.Join(r.tmpRoot(), u.DBName)))
	if err != nil {
		r.log.Warn("remove user data", "user", u.Username, "err", err)
	}
}

// StopImports refuses new tenants and cancels running imports (they are marked failed, NFR-7),
// waiting until they stop. Call it before the HTTP server shuts down, so progress streams end.
func (r *Registry) StopImports(ctx context.Context) {
	r.mu.Lock()
	r.closed = true
	all := make([]*Tenant, 0, len(r.m))
	for _, t := range r.m {
		all = append(all, t)
	}
	r.mu.Unlock()
	var wg sync.WaitGroup
	for _, t := range all {
		wg.Go(func() { t.Imports.Shutdown(ctx) })
	}
	wg.Wait()
}

// Shutdown stops imports and closes every open database. Call it after the HTTP server has
// stopped, so no request is still using a database.
func (r *Registry) Shutdown(ctx context.Context) {
	r.StopImports(ctx)
	r.mu.Lock()
	all := r.m
	r.m = map[int64]*Tenant{}
	r.mu.Unlock()
	var wg sync.WaitGroup
	for _, t := range all {
		wg.Go(func() {
			t.Query.Wait()
			t.DB.Close()
		})
	}
	wg.Wait()
}
