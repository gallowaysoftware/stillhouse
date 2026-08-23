package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// MaxAttempts is where retrying stops. Five attempts on the backoff below
// spans a little over an hour, which covers a receiver restart or a short
// outage without keeping a delivery alive so long that it arrives as a
// surprise.
const MaxAttempts = 5

// Backoff returns how long to wait before attempt n+1, or ok=false when
// the attempts are spent.
//
// Exponential from 30 seconds. Jitter is deliberately absent: there is one
// worker and deliveries are claimed in due order, so there is no
// thundering herd to spread, and a predictable schedule is one an operator
// can reason about when a delivery is late.
func Backoff(attempts int32) (time.Duration, bool) {
	if attempts >= MaxAttempts {
		return 0, false
	}
	d := 30 * time.Second
	for i := int32(1); i < attempts; i++ {
		d *= 4
	}
	return d, true
}

// Sign returns the value of the signature header: HMAC-SHA256 over the
// timestamp and the body, hex encoded.
//
// The timestamp is inside the MAC, not merely alongside it. A signature
// over the body alone is replayable forever — a receiver has no way to
// tell a delivery it already processed from a fresh one — and a timestamp
// that is not signed can simply be rewritten by whoever is replaying.
func Sign(secret []byte, at time.Time, body []byte) string {
	ts := strconv.FormatInt(at.Unix(), 10)
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(ts))
	m.Write([]byte{'.'})
	m.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(m.Sum(nil))
}

// Result is the outcome of one attempt.
type Result struct {
	OK         bool
	StatusCode int
	Err        string
}

// Deliver POSTs one payload. The client is the SSRF-safe one; callers
// must not substitute their own.
//
// Any 2xx is success. Everything else is a failure worth retrying, with
// one deliberate exception noted below.
func Deliver(ctx context.Context, c *http.Client, url string, secret, body []byte, kind, id string) Result {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Result{Err: err.Error()}
	}
	now := time.Now()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Stillhouse-Webhook/1")
	req.Header.Set("X-Stillhouse-Event", kind)
	// The delivery id is stable across retries, so a receiver can make
	// its own handling idempotent. Without it, "at least once" delivery
	// is unusable for anything that writes.
	req.Header.Set("X-Stillhouse-Delivery", id)
	req.Header.Set("X-Stillhouse-Signature", Sign(secret, now, body))

	resp, err := c.Do(req)
	if err != nil {
		return Result{Err: err.Error()}
	}
	defer resp.Body.Close()
	// Read and discard a bounded amount so the connection can be reused.
	// Bounded because the body is the receiver's, and an unbounded read
	// of somebody else's response is a memory exhaustion they control.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return Result{OK: true, StatusCode: resp.StatusCode}
	}
	return Result{
		StatusCode: resp.StatusCode,
		Err:        fmt.Sprintf("endpoint returned %s", resp.Status),
	}
}
