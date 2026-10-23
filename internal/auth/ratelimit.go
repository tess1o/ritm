package auth

import (
	"sync"
	"time"

	"ritm/internal/clock"
)

// maxLimiterKeys bounds a limiter's memory: keys can come from request data (spoofable
// X-Forwarded-For values, usernames), so expired entries are swept and, past the cap, dropped.
const maxLimiterKeys = 10_000

// RateLimiter blocks a key (an IP or a username) after max failures inside window.
type RateLimiter struct {
	mu        sync.Mutex
	clock     clock.Clock
	max       int
	window    time.Duration
	failures  map[string][]time.Time
	lastSweep time.Time
}

func NewRateLimiter(clk clock.Clock, max int, window time.Duration) *RateLimiter {
	return &RateLimiter{clock: clk, max: max, window: window, failures: map[string][]time.Time{}}
}

func (l *RateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key)) < l.max
}

func (l *RateLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.record(key)
}

// Take checks and records an attempt in one step, so parallel requests cannot all pass the
// check before any of them fails. An attempt that turns out fine is handed back with Refund.
func (l *RateLimiter) Take(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.prune(key)) >= l.max {
		return false
	}
	l.record(key)
	return true
}

// Refund takes back one attempt recorded by Take.
func (l *RateLimiter) Refund(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if f := l.prune(key); len(f) > 0 {
		if len(f) == 1 {
			delete(l.failures, key)
		} else {
			l.failures[key] = f[:len(f)-1]
		}
	}
}

func (l *RateLimiter) record(key string) {
	now := l.clock.Now()
	if _, ok := l.failures[key]; !ok && (len(l.failures) >= maxLimiterKeys || now.Sub(l.lastSweep) > l.window) {
		l.sweep(now)
	}
	l.failures[key] = append(l.prune(key), now)
}

func (l *RateLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

// sweep drops expired entries and, if the map is still full, arbitrary ones.
func (l *RateLimiter) sweep(now time.Time) {
	l.lastSweep = now
	for k := range l.failures {
		l.prune(k)
	}
	for k := range l.failures {
		if len(l.failures) < maxLimiterKeys {
			break
		}
		delete(l.failures, k)
	}
}

func (l *RateLimiter) prune(key string) []time.Time {
	cutoff := l.clock.Now().Add(-l.window)
	f := l.failures[key]
	i := 0
	for i < len(f) && f[i].Before(cutoff) {
		i++
	}
	f = f[i:]
	if len(f) == 0 {
		delete(l.failures, key)
		return nil
	}
	l.failures[key] = f
	return f
}
