package ingest

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"ritm/internal/store"
)

// ErrBusy is returned when the user already has an import uploading, queued or running.
var ErrBusy = errors.New("an import is already running")

// Progress is a live snapshot of an import, streamed over SSE (PRG-1).
type Progress struct {
	ID       string       `json:"id"`
	Status   string       `json:"status" enum:"queued,running,succeeded,failed"`
	Phase    string       `json:"phase" enum:"queued,parse,recompute,done"`
	Consumed int64        `json:"consumed_bytes"`
	Total    int64        `json:"total_bytes"`
	Percent  float64      `json:"percent"`
	Stats    *ImportStats `json:"stats,omitempty"`
	Error    string       `json:"error,omitempty"`
}

// Manager runs one user's background work: at most one import at a time (ING-2) and recomputes
// started by requests, which Shutdown cancels and waits for. Managers of different users share
// slots, which caps concurrent imports server-wide; an upload waits for a slot as "queued".
type Manager struct {
	db       *store.DB
	log      *slog.Logger
	tmpDir   string
	baseOpts func() Options // read per import, so a changed time zone applies to the next one
	post     PostImportFunc
	slots    chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	reserved bool
	current  *Progress
	subs     map[string]map[chan Progress]struct{}
}

// NewManager creates a manager that stages uploads in tmpDir. slots is shared by all managers;
// nil means no server-wide limit.
func NewManager(db *store.DB, log *slog.Logger, tmpDir string, base func() Options, post PostImportFunc, slots chan struct{}) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{db: db, log: log, tmpDir: tmpDir, baseOpts: base, post: post, slots: slots,
		ctx: ctx, cancel: cancel, subs: map[string]map[chan Progress]struct{}{}}
}

func (m *Manager) now() time.Time {
	o := m.baseOpts()
	return o.now()
}

// Recover marks interrupted imports failed and removes leftover temp files (ING-3).
func (m *Manager) Recover(ctx context.Context) error {
	n, err := RecoverInterrupted(ctx, m.db, m.now())
	if err != nil {
		return err
	}
	if n > 0 {
		m.log.Warn("marked interrupted imports as failed", "count", n)
	}
	if err := os.RemoveAll(m.tmpDir); err != nil {
		return err
	}
	return os.MkdirAll(m.tmpDir, 0o750)
}

func (m *Manager) TmpDir() string { return m.tmpDir }

// Reserve claims this user's import slot before an upload is streamed. Call Release if the upload
// fails before Start.
func (m *Manager) Reserve() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reserved {
		return ErrBusy
	}
	m.reserved = true
	return nil
}

func (m *Manager) Release() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reserved = false
}

// acquireSlot waits for a server-wide import slot; it returns a release func.
func (m *Manager) acquireSlot() (func(), error) {
	if m.slots == nil {
		return func() {}, nil
	}
	select {
	case m.slots <- struct{}{}:
		return func() { <-m.slots }, nil
	case <-m.ctx.Done():
		return nil, errors.New("server is shutting down")
	}
}

// Start launches the import of a file already written to tmpPath; the file is deleted when done.
// The user's slot must have been reserved. The import is queued until a server-wide slot is free.
func (m *Manager) Start(id, tmpPath, fileName string, size int64, full bool) error {
	if err := CreateImport(m.ctx, m.db, id, StatusQueued, fileName, size, full, m.now()); err != nil {
		os.Remove(tmpPath)
		m.Release()
		return err
	}
	m.mu.Lock()
	m.current = &Progress{ID: id, Status: StatusQueued, Phase: "queued"}
	m.mu.Unlock()
	m.publish()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer m.Release()
		defer os.Remove(tmpPath)
		opts := m.baseOpts()
		opts.ImportID = id
		opts.Full = full
		started := time.Now()
		var st *ImportStats
		release, err := m.acquireSlot()
		if err == nil {
			defer release()
			err = MarkRunning(m.ctx, m.db, id, opts.now())
		}
		if err == nil {
			m.update(func(p *Progress) { p.Status, p.Phase = StatusRunning, "parse" })
			opts.Progress = func(consumed, total int64) {
				m.update(func(p *Progress) {
					p.Consumed, p.Total = consumed, total
					if total > 0 {
						p.Percent = min(100, float64(consumed)*100/float64(total))
					}
				})
			}
			post := func(ctx context.Context, day string) error {
				m.update(func(p *Progress) { p.Phase = "recompute"; p.Percent = 100 })
				if m.post == nil {
					return nil
				}
				return m.post(ctx, day)
			}
			st, err = RunRecorded(m.ctx, m.db, tmpPath, fileName, size, opts, post)
		} else if ferr := FinishImport(m.ctx, m.db, id, nil, err, opts.now()); ferr != nil {
			err = errors.Join(err, ferr)
		}
		m.update(func(p *Progress) {
			p.Phase = "done"
			p.Stats = st
			if err != nil {
				p.Status = StatusFailed
				p.Error = err.Error()
			} else {
				p.Status = StatusSucceeded
				p.Percent = 100
			}
		})
		if err != nil {
			m.log.Error("import failed", "id", id, "err", err)
		} else {
			m.log.Info("import finished", "id", id, "duration", time.Since(started).Round(time.Millisecond),
				"new_workouts", st.NewWorkouts, "days_reaggregated", st.DaysReaggregated)
		}
		m.closeSubs(id)
	}()
	return nil
}

func (m *Manager) update(fn func(*Progress)) {
	m.mu.Lock()
	if m.current != nil {
		fn(m.current)
	}
	m.mu.Unlock()
	m.publish()
}

func (m *Manager) publish() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return
	}
	snap := *m.current
	for ch := range m.subs[snap.ID] {
		select {
		case ch <- snap:
		default: // slow subscriber: drop this update, a newer one follows
		}
	}
}

func (m *Manager) closeSubs(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for ch := range m.subs[id] {
		close(ch)
	}
	delete(m.subs, id)
}

// Live returns the in-memory progress of the most recent import if it matches id.
func (m *Manager) Live(id string) (Progress, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil || m.current.ID != id {
		return Progress{}, false
	}
	return *m.current, true
}

// Subscribe streams progress updates for a running import. The channel is closed when the import
// ends; ok is false if the import is not running.
func (m *Manager) Subscribe(id string) (ch <-chan Progress, unsubscribe func(), ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil || m.current.ID != id || m.current.Phase == "done" {
		return nil, func() {}, false
	}
	c := make(chan Progress, 8)
	if m.subs[id] == nil {
		m.subs[id] = map[chan Progress]struct{}{}
	}
	m.subs[id][c] = struct{}{}
	c <- *m.current
	return c, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if _, ok := m.subs[id][c]; ok {
			delete(m.subs[id], c)
			close(c)
		}
	}, true
}

// track registers background work unless Shutdown has begun.
func (m *Manager) track() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	m.wg.Add(1)
	return true
}

// Go runs fn in the background with a context that Shutdown cancels; Shutdown waits for it.
func (m *Manager) Go(fn func(ctx context.Context)) {
	if !m.track() {
		return
	}
	go func() {
		defer m.wg.Done()
		fn(m.ctx)
	}()
}

// Run runs fn on the caller's goroutine with a context that Shutdown cancels (not the request's,
// so a client that goes away does not stop it); Shutdown waits for it.
func (m *Manager) Run(fn func(ctx context.Context) error) error {
	if !m.track() {
		return errors.New("server is shutting down")
	}
	defer m.wg.Done()
	return fn(m.ctx)
}

// Shutdown cancels a running import (it is marked failed, NFR-7) and other background work, and
// waits for them to stop.
func (m *Manager) Shutdown(ctx context.Context) {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.cancel()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
