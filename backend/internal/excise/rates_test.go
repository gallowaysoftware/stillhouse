package excise

import (
	"errors"
	"math"
	"testing"
	"time"
)

// inBand is a date the seeded table can answer for. Every arithmetic test
// below uses it, so extending the table never silently changes what they
// assert.
var inBand = time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)

func TestOwed(t *testing.T) {
	b, err := RateOn(inBand)
	if err != nil {
		t.Fatalf("RateOn(%v): %v", inBand, err)
	}

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
			wantRate:  b.PerLAAOver7Pct,
			wantTotal: 0.75 * 0.40 * b.PerLAAOver7Pct, // 0.30 × 14.117 = 4.2351
		},
		{
			name:    "1000 L × 70% (bulk tax for some imaginary clearance)",
			volumeL: 1000, abvPct: 70,
			wantRate:  b.PerLAAOver7Pct,
			wantTotal: 700 * b.PerLAAOver7Pct,
		},
		{
			name:    "exactly at threshold (7%) → low-strength per-litre rate",
			volumeL: 100, abvPct: 7,
			wantRate:  0,
			wantTotal: 100 * b.PerLitreAtOrUnder7,
		},
		{
			name:    "just above threshold (7.01%) → high-strength per-LAA rate",
			volumeL: 100, abvPct: 7.01,
			wantRate:  b.PerLAAOver7Pct,
			wantTotal: 100 * 0.0701 * b.PerLAAOver7Pct,
		},
		{
			name:    "low-strength ABV well under threshold",
			volumeL: 250, abvPct: 5,
			wantRate:  0,
			wantTotal: 250 * b.PerLitreAtOrUnder7,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rate, total, err := Owed(inBand, tc.volumeL, tc.abvPct)
			if err != nil {
				t.Fatalf("Owed: %v", err)
			}
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
// expresses its expectation in terms of the band it looked up, so the
// package would report full coverage while asserting nothing about the
// numbers. Setting the 2026 rate to 141.17 would otherwise pass the entire
// suite. These are the figures CRA publishes; they change once a year on
// 1 April, and changing them should require deliberately editing this test.
//
// Source: EDN104, adjusted rates of excise duty on spirits and wine
// effective April 1, 2026.
func TestRatesArePinnedToPublishedFigures(t *testing.T) {
	b, err := RateOn(inBand)
	if err != nil {
		t.Fatalf("RateOn: %v", err)
	}
	if b.Source != "EDN104" {
		t.Errorf("source: got %q, want EDN104", b.Source)
	}
	if b.PerLAAOver7Pct != 14.117 {
		t.Errorf("spirits over 7%% ABV: %v per LAA, want 14.117 (EDN104, effective 2026-04-01)",
			b.PerLAAOver7Pct)
	}
	if b.PerLitreAtOrUnder7 != 0.358 {
		t.Errorf("spirits at or under 7%% ABV: %v per litre, want 0.358 (EDN104, effective 2026-04-01)",
			b.PerLitreAtOrUnder7)
	}
	if AbvThresholdPct != 7.0 {
		t.Errorf("band threshold: %v%% ABV, want 7.0", AbvThresholdPct)
	}
	// A worked example an auditor could check by hand: one 750 mL bottle
	// at 40% is 0.3 LAA, so $4.2351 of duty.
	if _, got, err := Owed(inBand, 0.75, 40); err != nil || math.Abs(got-4.2351) > 1e-9 {
		t.Errorf("duty on a 750 mL bottle at 40%%: $%v (err %v), want $4.2351", got, err)
	}
	// And one at 5% is charged per litre of product, not per LAA:
	// 0.355 L × $0.358.
	if _, got, err := Owed(inBand, 0.355, 5); err != nil || math.Abs(got-0.12709) > 1e-9 {
		t.Errorf("duty on a 355 mL cooler at 5%%: $%v (err %v), want $0.12709", got, err)
	}
}

// The whole point of A2: a date outside the table refuses instead of
// quietly answering with the nearest rate it happens to hold. Duty
// computed at today's rate against last year's quantities is wrong on a
// filed return and nothing about it looks wrong.
func TestRateOnRefusesOutsideTheTable(t *testing.T) {
	from, to := Coverage()

	for _, tc := range []struct {
		name string
		on   time.Time
	}{
		{"the day before the earliest band", from.AddDate(0, 0, -1)},
		{"a year before the earliest band", from.AddDate(-1, 0, 0)},
		{"the day the last band stops being known", to},
		{"well past the last band", to.AddDate(5, 0, 0)},
		{"the zero time", time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := RateOn(tc.on); err == nil {
				t.Fatalf("RateOn(%s) returned a rate; it must refuse", tc.on.Format("2006-01-02"))
			}
			var ure *UnknownRateError
			if _, err := RateOn(tc.on); !errors.As(err, &ure) {
				t.Errorf("error is %T, want *UnknownRateError", err)
			}
			if _, _, err := Owed(tc.on, 100, 40); err == nil {
				t.Error("Owed returned a figure for a date with no rate on file")
			}
		})
	}
}

// The boundaries themselves: EffectiveFrom is inclusive and KnownUntil is
// exclusive, so 1 April is the new band's first day and never the old
// band's last.
func TestBandBoundariesAreHalfOpen(t *testing.T) {
	for _, b := range bands {
		if got, err := RateOn(b.EffectiveFrom); err != nil {
			t.Errorf("%s: its own EffectiveFrom (%s) has no rate: %v",
				b.Source, b.EffectiveFrom.Format("2006-01-02"), err)
		} else if got.Source != b.Source {
			t.Errorf("%s EffectiveFrom resolved to %s", b.Source, got.Source)
		}
		if got, err := RateOn(b.KnownUntil.AddDate(0, 0, -1)); err != nil {
			t.Errorf("%s: its own last day has no rate: %v", b.Source, err)
		} else if got.Source != b.Source {
			t.Errorf("%s last day resolved to %s", b.Source, got.Source)
		}
	}
	// A time of day, and a non-UTC zone, must not change which band a
	// removal lands in: a shipment recorded at 20:00 in Vancouver on the
	// first of April is dutied at April's rate.
	first := bands[0].EffectiveFrom
	vancouver := time.FixedZone("PDT", -7*3600)
	local := time.Date(first.Year(), first.Month(), first.Day(), 20, 0, 0, 0, vancouver)
	if got, err := RateOn(local); err != nil || got.Source != bands[0].Source {
		t.Errorf("evening on the first day of a band resolved to %v (err %v), want %s",
			got.Source, err, bands[0].Source)
	}
}

// The table's own shape. A gap between bands is a date that silently has
// no rate; an overlap is two rates for one day, and which one wins depends
// on iteration order.
func TestBandTableIsContiguousAndOrdered(t *testing.T) {
	if len(bands) == 0 {
		t.Fatal("no rate bands on file")
	}
	for i, b := range bands {
		if !b.EffectiveFrom.Before(b.KnownUntil) {
			t.Errorf("band %d (%s): EffectiveFrom %s is not before KnownUntil %s",
				i, b.Source, b.EffectiveFrom.Format("2006-01-02"), b.KnownUntil.Format("2006-01-02"))
		}
		if b.Source == "" {
			t.Errorf("band %d has no source notice — every rate must say where it came from", i)
		}
		if b.PerLAAOver7Pct <= 0 || b.PerLitreAtOrUnder7 <= 0 {
			t.Errorf("band %d (%s): a rate is zero or negative", i, b.Source)
		}
		if i == 0 {
			continue
		}
		prev := bands[i-1]
		if !prev.KnownUntil.Equal(b.EffectiveFrom) {
			t.Errorf("band %d (%s) starts %s but %s stops being known at %s — %s",
				i, b.Source, b.EffectiveFrom.Format("2006-01-02"),
				prev.Source, prev.KnownUntil.Format("2006-01-02"),
				"bands must abut exactly: a gap is a date with no rate, an overlap is two")
		}
	}
}
