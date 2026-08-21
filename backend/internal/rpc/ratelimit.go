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
	// lastSweep bounds how often the whole map is walked.
	lastSweep time.Time
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
	l.sweepLocked(now, cutoff)
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

// maxTrackedKeys bounds the attempt map.
//
// Allow only ever pruned the key it was handed, so every distinct key was
// remembered for the process lifetime. A caller who can vary the key —
// which, before clientIP stopped trusting forwarded headers, meant anyone
// who could set X-Forwarded-For — could grow the map without limit. A
// rate limiter that can be turned into a memory exhaustion primitive is
// worse than no rate limiter.
const maxTrackedKeys = 10000

// sweepLocked drops keys whose attempts have all aged out. It runs at most
// once per window, and unconditionally once the map is over the cap, so
// the common path stays O(1).
func (l *SlidingWindowLimiter) sweepLocked(now, cutoff time.Time) {
	if len(l.attempts) < maxTrackedKeys && now.Sub(l.lastSweep) < l.window {
		return
	}
	l.lastSweep = now
	for k, ts := range l.attempts {
		live := false
		for _, t := range ts {
			if t.After(cutoff) {
				live = true
				break
			}
		}
		if !live {
			delete(l.attempts, k)
		}
	}
	// Everything is live and we are still over the cap: the limiter is
	// under genuine load rather than being farmed. Drop the map rather
	// than grow without bound — the cost is that some in-flight attackers
	// get a fresh budget, which beats running out of memory.
	if len(l.attempts) >= maxTrackedKeys {
		l.attempts = make(map[string][]time.Time, maxTrackedKeys)
	}
}
