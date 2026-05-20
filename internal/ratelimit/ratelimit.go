// Package ratelimit implements the per-IP token bucket described in
// DESIGN.md §11.2. In the open-access v1, this is the primary defence
// against any single caller burning the upstream quota.
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// PerKey returns a Limiter wrapper that maintains an independent token bucket
// per opaque key (typically client IP). Buckets idle for evictAfter are
// reclaimed by a background janitor.
type PerKey struct {
	burst    int
	refill   rate.Limit
	evictAfter time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	limiter *rate.Limiter
	lastUse time.Time
}

func NewPerKey(burst int, refillPerSec float64, evictAfter time.Duration) *PerKey {
	return &PerKey{
		burst:      burst,
		refill:     rate.Limit(refillPerSec),
		evictAfter: evictAfter,
		buckets:    make(map[string]*bucket),
	}
}

// Allow reports whether the caller for key may proceed right now.
func (p *PerKey) Allow(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, ok := p.buckets[key]
	if !ok {
		b = &bucket{limiter: rate.NewLimiter(p.refill, p.burst)}
		p.buckets[key] = b
	}
	b.lastUse = time.Now()
	return b.limiter.Allow()
}

// RetryAfterSeconds returns a rough wait time in seconds for the caller's
// bucket. Best-effort; used to populate the rate_limited error retry hint.
func (p *PerKey) RetryAfterSeconds(key string) int {
	if p.refill <= 0 {
		return 1
	}
	return int(1.0/float64(p.refill)) + 1
}

// RunJanitor reclaims idle buckets every interval. Call once at startup; it
// returns when ctx-equivalent signal is closed (here: when stop is closed).
func (p *PerKey) RunJanitor(stop <-chan struct{}, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			p.sweep(now)
		}
	}
}

func (p *PerKey) sweep(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, b := range p.buckets {
		if now.Sub(b.lastUse) > p.evictAfter {
			delete(p.buckets, k)
		}
	}
}

// Size returns the number of live buckets (for metrics/tests).
func (p *PerKey) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.buckets)
}
