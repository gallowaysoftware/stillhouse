package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The signature is what makes a delivery trustworthy. If a receiver can
// be made to accept a forged or replayed body, the feature is worse than
// not having it — it launders untrusted data as ours.
func TestSignIsOverTimestampAndBody(t *testing.T) {
	secret := []byte("shhh")
	at := time.Unix(1_770_000_000, 0)
	body := []byte(`{"kind":"removal_recorded"}`)

	got := Sign(secret, at, body)
	if !strings.HasPrefix(got, "t=1770000000,v1=") {
		t.Fatalf("shape: %q", got)
	}

	// Recompute the way a receiver would.
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(strconv.FormatInt(at.Unix(), 10)))
	m.Write([]byte{'.'})
	m.Write(body)
	want := "t=1770000000,v1=" + hex.EncodeToString(m.Sum(nil))
	if got != want {
		t.Errorf("signature does not match the documented construction:\n got %s\nwant %s", got, want)
	}

	// The timestamp must be INSIDE the MAC. If it were merely prefixed,
	// changing it would leave the digest valid and a captured delivery
	// could be replayed with a fresh timestamp forever.
	other := Sign(secret, at.Add(time.Second), body)
	if strings.SplitN(got, "v1=", 2)[1] == strings.SplitN(other, "v1=", 2)[1] {
		t.Error("the digest does not change with the timestamp — captured deliveries are replayable")
	}

	// And a different secret must not produce the same digest.
	if Sign([]byte("other"), at, body) == got {
		t.Error("the digest does not depend on the secret")
	}
	// Nor a different body.
	if Sign(secret, at, []byte(`{"kind":"loss_recorded"}`)) == got {
		t.Error("the digest does not depend on the body")
	}
}

// Retrying forever turns a receiver's outage into ours, and a delivery
// that arrives two days late arrives as a surprise. The schedule is
// pinned so a change to it is a decision rather than a drift.
func TestBackoffStopsAtMaxAttempts(t *testing.T) {
	var total time.Duration
	for n := int32(1); ; n++ {
		d, ok := Backoff(n)
		if !ok {
			if n != MaxAttempts {
				t.Errorf("gave up after %d attempts, want %d", n, MaxAttempts)
			}
			break
		}
		if d <= 0 {
			t.Fatalf("attempt %d: non-positive backoff %v", n, d)
		}
		total += d
		if n > 20 {
			t.Fatal("Backoff never gave up")
		}
	}
	if total < 30*time.Minute || total > 3*time.Hour {
		t.Errorf("total retry span %v — outside the hour-ish window the comment claims", total)
	}
}

// The backoff must actually grow. An earlier draft of the worker asked
// for Backoff(1) on every retry, so five "exponential" attempts were five
// attempts thirty seconds apart — a retry storm wearing a backoff's name,
// and one that would have looked correct in any test that only checked
// "does it retry".
func TestBackoffGrows(t *testing.T) {
	var prev time.Duration
	for n := int32(1); n < MaxAttempts; n++ {
		d, ok := Backoff(n)
		if !ok {
			t.Fatalf("attempt %d: gave up early", n)
		}
		if n > 1 && d <= prev {
			t.Errorf("attempt %d waits %v, not longer than the previous %v", n, d, prev)
		}
		prev = d
	}
	// And the first wait is the documented 30 seconds, so a receiver
	// restarting is not hammered.
	if d, _ := Backoff(1); d != 30*time.Second {
		t.Errorf("first retry waits %v, want 30s", d)
	}
}
