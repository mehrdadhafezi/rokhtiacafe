// Package ratelimit provides in-memory, per-key token buckets (docs/SECURITY.md §5).
// Limits reset on restart, which is acceptable for this single-instance app.
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limiter allows `burst` events per `window` for each key, refilling evenly.
type Limiter struct {
	mu      sync.Mutex
	every   rate.Limit
	burst   int
	idleTTL time.Duration
	keys    map[string]*entry
	calls   int
	now     func() time.Time
}

type entry struct {
	lim  *rate.Limiter
	seen time.Time
}

// New returns a limiter allowing burst events per window and key.
func New(burst int, window time.Duration) *Limiter {
	return &Limiter{
		every:   rate.Every(window / time.Duration(burst)),
		burst:   burst,
		idleTTL: 2 * window,
		keys:    make(map[string]*entry),
		now:     time.Now,
	}
}

// Allow consumes one token for key. When denied it returns how long until the next token.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.calls++
	if l.calls%256 == 0 {
		for k, e := range l.keys {
			if now.Sub(e.seen) > l.idleTTL {
				delete(l.keys, k)
			}
		}
	}
	e, ok := l.keys[key]
	if !ok {
		e = &entry{lim: rate.NewLimiter(l.every, l.burst)}
		l.keys[key] = e
	}
	e.seen = now
	res := e.lim.ReserveN(now, 1)
	if delay := res.DelayFrom(now); delay > 0 {
		res.CancelAt(now)
		return false, delay
	}
	return true, 0
}
