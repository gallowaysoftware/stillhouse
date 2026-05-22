package rpc

import (
	"testing"
	"time"
)

func TestSlidingWindowLimiter(t *testing.T) {
	l := NewSlidingWindowLimiter(3, 100*time.Millisecond)

	for i := 0; i < 3; i++ {
		if !l.Allow("k") {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	if l.Allow("k") {
		t.Errorf("4th attempt should be denied")
	}

	// Different key isolates.
	if !l.Allow("k2") {
		t.Errorf("different key should be allowed")
	}

	// After window passes, attempts are forgotten.
	time.Sleep(110 * time.Millisecond)
	if !l.Allow("k") {
		t.Errorf("after window expiry, attempts should reset")
	}

	// Forget clears immediately.
	for i := 0; i < 3; i++ {
		l.Allow("k3")
	}
	if l.Allow("k3") {
		t.Fatalf("k3 should be over limit before Forget")
	}
	l.Forget("k3")
	if !l.Allow("k3") {
		t.Errorf("after Forget, k3 should be allowed again")
	}
}
