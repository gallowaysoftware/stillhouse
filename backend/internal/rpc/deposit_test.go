package rpc

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestTimestampOrNowHandlesNil — an unset protobuf message field arrives
// as nil, and must become "now" rather than the zero time.
func TestTimestampOrNowHandlesNil(t *testing.T) {
	before := time.Now()
	got := timestampOrNow(nil)
	after := time.Now()
	if !got.Valid {
		t.Fatal("result must be a valid timestamp")
	}
	if got.Time.Before(before) || got.Time.After(after) {
		t.Errorf("nil should map to now, got %v", got.Time)
	}
}

// TestTimestampOrNowOnZeroValueIsEpoch pins the trap. A non-nil but
// zero-valued Timestamp is NOT nil, so it does not become "now" — it
// becomes the Unix epoch, because that is what AsTime() returns for
// seconds=0. A caller that sends an empty timestamp object instead of
// omitting the field will date the record 1970.
//
// Callers must omit the field, not zero it.
func TestTimestampOrNowOnZeroValueIsEpoch(t *testing.T) {
	got := timestampOrNow(&timestamppb.Timestamp{})
	if !got.Valid {
		t.Fatal("result must be a valid timestamp")
	}
	if got.Time.UTC().Year() != 1970 {
		t.Errorf("a zero-valued timestamp gives %v; expected the Unix epoch, "+
			"which is the whole point of this test", got.Time.UTC())
	}
}

func TestTimestampOrNowPassesThroughRealValues(t *testing.T) {
	when := time.Date(2026, 3, 14, 15, 9, 26, 0, time.UTC)
	got := timestampOrNow(timestamppb.New(when))
	if !got.Time.Equal(when) {
		t.Errorf("got %v, want %v", got.Time, when)
	}
}

func TestRoundHelpers(t *testing.T) {
	// LAA is reported to four decimals on a B266; volumes to two.
	if got := round4(0.8399999999999999); got != 0.84 {
		t.Errorf("round4 = %v, want 0.84", got)
	}
	if got := round2(1234.5678); got != 1234.57 {
		t.Errorf("round2 = %v, want 1234.57", got)
	}
	// Negatives reach these helpers — a cask's strength drift is negative
	// in a cool, humid warehouse — and the int(x+0.5) idiom they used to
	// use rounded those the wrong way. Rounding must be symmetric about
	// zero.
	for _, v := range []float64{0.12345, 1.5, 0.99999, 2.00005, 0.00004} {
		if round4(-v) != -round4(v) {
			t.Errorf("round4 is asymmetric at %v: %v vs %v", v, round4(-v), -round4(v))
		}
		if round2(-v) != -round2(v) {
			t.Errorf("round2 is asymmetric at %v: %v vs %v", v, round2(-v), -round2(v))
		}
	}
	// The specific regressions.
	if got := round4(-1.5); got != -1.5 {
		t.Errorf("round4(-1.5) = %v, want -1.5", got)
	}
	if got := round4(-0.99999); got != -1 {
		t.Errorf("round4(-0.99999) = %v, want -1", got)
	}
	// Rounding must be stable — applying it twice changes nothing.
	for _, v := range []float64{0.12345, 999.99999, 0} {
		if round4(round4(v)) != round4(v) {
			t.Errorf("round4 is not idempotent at %v", v)
		}
	}
}
