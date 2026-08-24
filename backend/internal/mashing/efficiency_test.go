package mashing

import "testing"

// ConversionPercent is the arithmetic the mash bench and the cross-tenant
// benchmarks both go through. A second copy of it anywhere is how a
// cohort stops being comparable, so this pins the one.
func TestConversionPercent(t *testing.T) {
	// 100 kg of extract available, wash of 1000 L at SG 1.050.
	got, ok := ConversionPercent(100, 1.050, 1000)
	if !ok {
		t.Fatal("refused a complete set of inputs")
	}
	// Pinned to a number worked out by hand rather than to the function
	// under test. An earlier version of this asserted against
	// ExtractInSolutionKg, which made half of it tautological: dropping
	// the SG factor from the wort-mass step changed the answer by five
	// kilogrammes and nothing failed.
	//
	//   Plato(1.050) = 259 − 259/1.050          = 12.3333…
	//   wort mass    = 1000 L × 1.050           = 1050 kg
	//   extract      = 1050 × 12.3333… / 100    = 129.50 kg
	//   percent      = 129.50 / 100 × 100       = 129.50 %
	if got < 129.49 || got > 129.51 {
		t.Errorf("got %v%%, want 129.50%% — see the working above", got)
	}
	// And the two entry points agree, since a benchmark cohort is only
	// comparable while they do.
	if e := ExtractInSolutionKg(1.050, 1000); e < 129.49 || e > 129.51 {
		t.Errorf("ExtractInSolutionKg: got %v kg, want 129.50", e)
	}

	// Missing inputs refuse rather than returning zero: a mash with no
	// gravity reading and one that converted nothing are different
	// claims, and a benchmark built from the second when the first is
	// true would drag a cohort down.
	for _, tc := range []struct{ avail, og, vol float64 }{
		{0, 1.050, 1000},
		{100, 1.0, 1000},
		{100, 0, 1000},
		{100, 1.050, 0},
	} {
		if v, ok := ConversionPercent(tc.avail, tc.og, tc.vol); ok {
			t.Errorf("ConversionPercent(%v,%v,%v) = %v, want a refusal", tc.avail, tc.og, tc.vol, v)
		}
	}
}
