package rpc

import (
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func abv(v float64) pgtype.Float8 { return pgtype.Float8{Float64: v, Valid: true} }
func noABV() pgtype.Float8        { return pgtype.Float8{Valid: false} }

// TestApplyDepositConservesAlcohol is the invariant every movement in the
// ledger rests on, and the same class of property stage 109's P0 broke:
// depositing spirit into a vessel must neither create nor destroy
// absolute alcohol.
func TestApplyDepositConservesAlcohol(t *testing.T) {
	cases := []struct {
		name           string
		curVol         float64
		curABV         pgtype.Float8
		addVol, addABV float64
	}{
		{"into an empty vessel", 0, noABV(), 100, 63},
		{"same strength", 100, abv(63), 100, 63},
		{"stronger into weaker", 500, abv(40), 100, 94.5},
		{"weaker into stronger", 100, abv(94.5), 500, 40},
		{"water into spirit", 100, abv(60), 50, 0},
		{"tiny addition", 1000, abv(62), 0.5, 62},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotVol, gotABV, gotLAA := applyDeposit(tc.curVol, tc.curABV, tc.addVol, tc.addABV)

			wantVol := tc.curVol + tc.addVol
			if tc.curVol <= 0 {
				wantVol = tc.addVol
			}
			if math.Abs(gotVol-wantVol) > 1e-9 {
				t.Errorf("volume = %v, want %v", gotVol, wantVol)
			}

			// Alcohol in must equal alcohol out.
			startLAA := 0.0
			if tc.curVol > 0 && tc.curABV.Valid {
				startLAA = tc.curVol * tc.curABV.Float64 / 100
			}
			wantLAA := startLAA + tc.addVol*tc.addABV/100
			if math.Abs(gotLAA-wantLAA) > 1e-9 {
				t.Errorf("LAA = %.10f, want %.10f — alcohol was created or destroyed", gotLAA, wantLAA)
			}

			// And the blended strength must be consistent with them.
			if gotABV.Valid && gotVol > 0 {
				if math.Abs(gotVol*gotABV.Float64/100-gotLAA) > 1e-9 {
					t.Errorf("volume × strength (%.10f) disagrees with LAA (%.10f)",
						gotVol*gotABV.Float64/100, gotLAA)
				}
			}
		})
	}
}

// TestApplyDepositBlendsByAlcoholNotAverage — the blended strength is the
// alcohol-weighted mean, not the arithmetic mean of the two strengths.
// Equal volumes happen to coincide; unequal volumes are the real test.
func TestApplyDepositBlendsByAlcoholNotAverage(t *testing.T) {
	// 900 L at 40 % blended with 100 L at 90 %.
	_, got, _ := applyDeposit(900, abv(40), 100, 90)
	if !got.Valid {
		t.Fatal("blended strength should be set")
	}
	want := (900*40 + 100*90) / 1000.0 // 45 %
	if math.Abs(got.Float64-want) > 1e-9 {
		t.Errorf("blended strength = %.6f, want %.6f", got.Float64, want)
	}
	naiveAverage := (40 + 90) / 2.0
	if math.Abs(got.Float64-naiveAverage) < 1e-9 {
		t.Error("blend used the arithmetic mean of the strengths, ignoring volume")
	}
}

// TestApplyDepositTreatsUnknownStrengthAsZero documents a sharp edge. A
// vessel holding liquid with no recorded strength contributes no alcohol
// to the blend, so the result is understated rather than refused.
//
// Not currently reachable — a container only gets a volume through a
// deposit or a regauge, and both set a strength — but it is the kind of
// thing a future write path could walk into, so the behaviour is pinned.
func TestApplyDepositTreatsUnknownStrengthAsZero(t *testing.T) {
	_, gotABV, gotLAA := applyDeposit(100, noABV(), 100, 60)
	// 100 L of unknown + 100 L at 60 % is treated as 100 L of 0 %.
	if math.Abs(gotABV.Float64-30) > 1e-9 {
		t.Errorf("blended strength = %v, want 30 (the unknown half counted as water)", gotABV.Float64)
	}
	if math.Abs(gotLAA-60) > 1e-9 {
		t.Errorf("LAA = %v, want 60", gotLAA)
	}
}

func TestApplyDepositIntoEmptyTakesTheDepositStrength(t *testing.T) {
	vol, got, laa := applyDeposit(0, noABV(), 250, 63)
	if vol != 250 {
		t.Errorf("volume = %v, want 250", vol)
	}
	if !got.Valid || got.Float64 != 63 {
		t.Errorf("strength = %+v, want 63", got)
	}
	if math.Abs(laa-157.5) > 1e-9 {
		t.Errorf("LAA = %v, want 157.5", laa)
	}
}

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
