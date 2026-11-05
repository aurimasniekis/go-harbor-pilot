// Package ratelimit provides a small keyed token-bucket limiter.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter limits events per key. It is safe for concurrent use and bounds
// memory by evicting idle keys.
type Limiter struct {
	mu      sync.Mutex
	perSec  float64
	burst   float64
	buckets map[string]*bucket
	now     func() time.Time
	maxKeys int
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New creates a limiter allowing `perMinute` sustained events with `burst`.
func New(perMinute int, burst int, now func() time.Time) *Limiter {
	if burst <= 0 {
		burst = 1
	}
	if now == nil {
		now = time.Now
	}
	return &Limiter{
		perSec: float64(perMinute) / 60, burst: float64(burst), buckets: map[string]*bucket{}, now: now, maxKeys: 10_000,
	}
}

// Allow consumes one token for key. When denied it returns the time until a
// token is available.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	return l.AllowN(key, 1)
}

// AllowN consumes n tokens.
func (l *Limiter) AllowN(key string, n float64) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxKeys {
			l.evict(now)
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	if el := now.Sub(b.last).Seconds(); el > 0 {
		b.tokens = min(l.burst, b.tokens+el*l.perSec)
		b.last = now
	}
	if b.tokens >= n {
		b.tokens -= n
		return true, 0
	}
	if l.perSec <= 0 {
		return false, time.Hour
	}
	return false, time.Duration((n - b.tokens) / l.perSec * float64(time.Second))
}

func (l *Limiter) evict(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.last) > 10*time.Minute {
			delete(l.buckets, k)
		}
	}
	if len(l.buckets) >= l.maxKeys {
		l.buckets = map[string]*bucket{}
	}
}
