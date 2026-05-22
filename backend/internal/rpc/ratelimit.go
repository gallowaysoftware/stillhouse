package rpc

import (
	"sync"
	"time"
)

// SlidingWindowLimiter is a per-key sliding-window counter used to throttle
// abusive callers. Suitable for single-instance self-host; replace with a
// shared store (Redis, Postgres) when running multiple replicas.
type SlidingWindowLimiter struct {
	max      int
	window   time.Duration
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func NewSlidingWindowLimiter(max int, window time.Duration) *SlidingWindowLimiter {
	return &SlidingWindowLimiter{
		max:      max,
		window:   window,
		attempts: make(map[string][]time.Time),
	}
}

// Allow returns true if the key has not exceeded the threshold. Each call
// counts as one attempt. Older attempts outside the window are forgotten.
func (l *SlidingWindowLimiter) Allow(key string) bool {
	now := time.Now()
	cutoff := now.Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()
	prev := l.attempts[key]
	kept := prev[:0]
	for _, t := range prev {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.attempts[key] = kept
		return false
	}
	kept = append(kept, now)
	l.attempts[key] = kept
	return true
}

// Forget clears a key (use after a successful login so a legitimate user
// who typo'd a few times doesn't stay locked out).
func (l *SlidingWindowLimiter) Forget(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}
