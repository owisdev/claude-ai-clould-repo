// Package ratelimit provides per-key token-bucket rate limiting.
package ratelimit

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limiter keeps one token bucket per key (API client now, user later).
// Safe for concurrent use.
type Limiter struct {
	rps   rate.Limit
	burst int
	idle  time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// New allows rps requests per second per key with bursts of up to burst.
// Buckets unused for idle are dropped by Cleanup.
func New(rps float64, burst int, idle time.Duration) *Limiter {
	return &Limiter{
		rps:     rate.Limit(rps),
		burst:   burst,
		idle:    idle,
		buckets: make(map[string]*bucket),
	}
}

// Allow reports whether a request for key may proceed. If not, it returns
// how long the caller should wait before retrying.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := time.Now()

	l.mu.Lock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{limiter: rate.NewLimiter(l.rps, l.burst)}
		l.buckets[key] = b
	}
	b.lastSeen = now
	l.mu.Unlock()

	r := b.limiter.ReserveN(now, 1)
	if !r.OK() {
		return false, time.Second
	}
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return false, d
	}
	return true, 0
}

// Cleanup removes idle buckets every interval until ctx is done.
func (l *Limiter) Cleanup(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			l.mu.Lock()
			for k, b := range l.buckets {
				if now.Sub(b.lastSeen) > l.idle {
					delete(l.buckets, k)
				}
			}
			l.mu.Unlock()
		}
	}
}
