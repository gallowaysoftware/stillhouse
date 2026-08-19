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
