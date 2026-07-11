// Package ratelimit provides a per-key token-bucket limiter. Each key
// (typically a service account) gets its own bucket that refills at a fixed
// rate up to a burst ceiling; a request consumes one token, and when the
// bucket is empty the caller learns how long until the next token.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a set of per-key token buckets, safe for concurrent use.
type Limiter struct {
	ratePerSec float64
	burst      float64
	now        func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New builds a limiter allowing ratePerSec sustained requests with a burst
// ceiling. A non-positive rate or burst yields a nil limiter (disabled).
func New(ratePerSec float64, burst int) *Limiter {
	if ratePerSec <= 0 || burst <= 0 {
		return nil
	}
	return &Limiter{
		ratePerSec: ratePerSec,
		burst:      float64(burst),
		now:        time.Now,
		buckets:    map[string]*bucket{},
	}
}

// Allow reports whether a request for key may proceed. When it may not, it
// returns the duration until the next token becomes available so callers
// can set Retry-After. A nil limiter always allows.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		// A fresh bucket starts full, then immediately spends one token.
		l.evictIfLarge(now)
		l.buckets[key] = &bucket{tokens: l.burst - 1, last: now}
		return true, 0
	}

	// Refill for elapsed time, capped at the burst ceiling.
	elapsed := now.Sub(b.last).Seconds()
	b.tokens = minFloat(l.burst, b.tokens+elapsed*l.ratePerSec)
	b.last = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	// Time until the bucket accumulates one whole token.
	deficit := 1 - b.tokens
	retry := time.Duration(deficit / l.ratePerSec * float64(time.Second))
	if retry < time.Second {
		retry = time.Second // Retry-After is whole seconds; never advertise 0
	}
	return false, retry
}

// evictIfLarge drops full (idle) buckets when the map grows large, so a
// churn of distinct keys can't leak memory. Caller holds the lock.
func (l *Limiter) evictIfLarge(now time.Time) {
	if len(l.buckets) < 4096 {
		return
	}
	for k, b := range l.buckets {
		if minFloat(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.ratePerSec) >= l.burst {
			delete(l.buckets, k)
		}
	}
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
