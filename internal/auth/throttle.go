package auth

import (
	"sync"
	"time"
)

// Throttle slows down password guessing. Failures are counted per key (client IP, account
// name, ...). After Free failures a key must wait an exponentially growing delay (capped at
// Max) before the next attempt is even evaluated. Counters decay after Window without
// failures. It never locks an account permanently, so it cannot be abused to lock out users
// for long.
type Throttle struct {
	Free   int
	Base   time.Duration
	Max    time.Duration
	Window time.Duration
	Limit  int // max tracked keys

	mu   sync.Mutex
	m    map[string]*throttleEntry
	now  func() time.Time
	last time.Time
}

type throttleEntry struct {
	failures int
	last     time.Time
	until    time.Time
}

func NewThrottle(free int, base, max, window time.Duration) *Throttle {
	return &Throttle{Free: free, Base: base, Max: max, Window: window, Limit: 20000, m: map[string]*throttleEntry{}, now: time.Now}
}

// SetClock replaces the time source (tests).
func (t *Throttle) SetClock(now func() time.Time) { t.now = now }

// Wait returns how long the keys must still wait (0 = attempt allowed).
func (t *Throttle) Wait(keys ...string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	var wait time.Duration
	for _, k := range keys {
		e := t.m[k]
		if e == nil {
			continue
		}
		if now.Sub(e.last) > t.Window {
			delete(t.m, k)
			continue
		}
		if d := e.until.Sub(now); d > wait {
			wait = d
		}
	}
	return wait
}

// Fail records a failed attempt for every key.
func (t *Throttle) Fail(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.gc(now)
	for _, k := range keys {
		e := t.m[k]
		if e == nil || now.Sub(e.last) > t.Window {
			if len(t.m) >= t.Limit {
				t.evict()
			}
			e = &throttleEntry{}
			t.m[k] = e
		}
		e.failures++
		e.last = now
		if over := e.failures - t.Free; over > 0 {
			d := t.Base << min(over-1, 20)
			if d > t.Max || d <= 0 {
				d = t.Max
			}
			e.until = now.Add(d)
		}
	}
}

// Reset forgets the keys (after a successful login).
func (t *Throttle) Reset(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, k := range keys {
		delete(t.m, k)
	}
}

func (t *Throttle) gc(now time.Time) {
	if now.Sub(t.last) < time.Minute {
		return
	}
	t.last = now
	for k, e := range t.m {
		if now.Sub(e.last) > t.Window {
			delete(t.m, k)
		}
	}
}

// evict drops the entries with the oldest activity (about 10%).
func (t *Throttle) evict() {
	n := len(t.m) / 10
	if n < 1 {
		n = 1
	}
	var oldest time.Time
	for _, e := range t.m {
		if oldest.IsZero() || e.last.Before(oldest) {
			oldest = e.last
		}
	}
	cut := oldest.Add(t.Window / 10)
	for k, e := range t.m {
		if n == 0 {
			break
		}
		if e.last.Before(cut) {
			delete(t.m, k)
			n--
		}
	}
	for k := range t.m {
		if n <= 0 {
			break
		}
		delete(t.m, k)
		n--
	}
}

// RateLimiter is a token bucket per key, for request rates (not passwords).
type RateLimiter struct {
	rate  float64 // tokens per second
	burst float64

	mu  sync.Mutex
	m   map[string]*bucket
	now func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func NewRateLimiter(perMinute, burst int) *RateLimiter {
	return NewRateLimiterPer(perMinute, time.Minute, burst)
}

// NewRateLimiterPer allows n events per period with the given burst.
func NewRateLimiterPer(n int, period time.Duration, burst int) *RateLimiter {
	return &RateLimiter{rate: float64(n) / period.Seconds(), burst: float64(burst), m: map[string]*bucket{}, now: time.Now}
}

// SetClock replaces the time source (tests).
func (r *RateLimiter) SetClock(now func() time.Time) { r.now = now }

func (r *RateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	b := r.m[key]
	if b == nil {
		if len(r.m) > 20000 {
			for k, v := range r.m {
				if now.Sub(v.last).Seconds()*r.rate > r.burst {
					delete(r.m, k)
				}
			}
			if len(r.m) > 20000 {
				return false
			}
		}
		b = &bucket{tokens: r.burst, last: now}
		r.m[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * r.rate
	if b.tokens > r.burst {
		b.tokens = r.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
