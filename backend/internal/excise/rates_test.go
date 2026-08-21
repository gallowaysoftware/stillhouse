package excise

import (
	"math"
	"testing"
	"time"
)

func TestOwed(t *testing.T) {
	when := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		volumeL   float64
		abvPct    float64
		wantRate  float64 // ratePerLAA returned (0 for ≤7%)
		wantTotal float64
	}{
		{
			name:    "750 mL × 40% = 0.3 L LAA at full rate",
			volumeL: 0.75, abvPct: 40,
			wantRate:  DutyRatePerLAAOver7Pct,
			wantTotal: 0.75 * 0.40 * DutyRatePerLAAOver7Pct, // 0.30 × 14.117 = 4.2351
		},
		{
			name:    "1000 L × 70% (bulk tax for some imaginary clearance)",
			volumeL: 1000, abvPct: 70,
			wantRate:  DutyRatePerLAAOver7Pct,
			wantTotal: 700 * DutyRatePerLAAOver7Pct,
		},
		{
			name:    "exactly at threshold (7%) → low-strength per-litre rate",
			volumeL: 100, abvPct: 7,
			wantRate:  0,
			wantTotal: 100 * DutyRatePerLAtOrUnder7,
		},
		{
			name:    "just above threshold (7.01%) → high-strength per-LAA rate",
			volumeL: 100, abvPct: 7.01,
			wantRate:  DutyRatePerLAAOver7Pct,
			wantTotal: 100 * 0.0701 * DutyRatePerLAAOver7Pct,
		},
		{
			name:    "low-strength ABV well under threshold",
			volumeL: 250, abvPct: 5,
			wantRate:  0,
			wantTotal: 250 * DutyRatePerLAtOrUnder7,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rate, total := Owed(when, tc.volumeL, tc.abvPct)
			if rate != tc.wantRate {
				t.Errorf("rate: got %v want %v", rate, tc.wantRate)
			}
			if !near(total, tc.wantTotal, 1e-6) {
				t.Errorf("total: got %v want %v", total, tc.wantTotal)
			}
		})
	}
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// TestRatesArePinnedToPublishedFigures: every other test in this package
// expresses its expectation in terms of the constants, so the package
// reported 100% coverage while asserting nothing about the numbers. Setting
// DutyRatePerLAAOver7Pct to 141.17 passed the entire suite. These are the
// figures CRA publishes; they change once a year on 1 April, and changing
// them should require deliberately editing this test.
//
// Source: EDN104, adjusted rates of excise duty on spirits and wine
// effective April 1, 2026.
func TestRatesArePinnedToPublishedFigures(t *testing.T) {
	if DutyRatePerLAAOver7Pct != 14.117 {
		t.Errorf("spirits over 7%% ABV: %v per LAA, want 14.117 (EDN104, effective 2026-04-01)",
			DutyRatePerLAAOver7Pct)
	}
	if DutyRatePerLAtOrUnder7 != 0.358 {
		t.Errorf("spirits at or under 7%% ABV: %v per litre, want 0.358 (EDN104, effective 2026-04-01)",
			DutyRatePerLAtOrUnder7)
	}
	if AbvThresholdPct != 7.0 {
		t.Errorf("band threshold: %v%% ABV, want 7.0", AbvThresholdPct)
	}
	// A worked example an auditor could check by hand: one 750 mL bottle
	// at 40% is 0.3 LAA, so $4.2351 of duty.
	if _, got := Owed(time.Time{}, 0.75, 40); math.Abs(got-4.2351) > 1e-9 {
		t.Errorf("duty on a 750 mL bottle at 40%%: $%v, want $4.2351", got)
	}
	// And one at 5% is charged per litre of product, not per LAA:
	// 0.355 L x $0.358.
	if _, got := Owed(time.Time{}, 0.355, 5); math.Abs(got-0.12709) > 1e-9 {
		t.Errorf("duty on a 355 mL cooler at 5%%: $%v, want $0.12709", got)
	}
}
