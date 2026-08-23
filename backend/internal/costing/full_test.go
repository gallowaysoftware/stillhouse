package costing

import "testing"

// round2 is used on every money figure that leaves this package, so its
// behaviour at the half is worth pinning: banker's rounding and
// round-half-away differ by a cent, and a cent per bottle across a year
// is a number somebody notices.
func TestRound2(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want float64
	}{
		{0, 0},
		{1.234, 1.23},
		{1.235, 1.24},
		// 1.005 is stored as 1.00499999999999989, so it rounds down. That
		// is the input's property, not this function's, and pinning it
		// here stops somebody "fixing" it with an epsilon.
		{1.005, 1.00},
		{-1.235, -1.24},
		{-1.234, -1.23},
		{12345.678, 12345.68},
	} {
		if got := round2(tc.in); got != tc.want {
			t.Errorf("round2(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// A component that could not be computed must say so rather than
// reporting zero. The distinction is the whole point of the type: a cost
// of sales missing its overhead and a cost of sales with no overhead are
// different statements, and only one of them is a full cost.
func TestComponentAbsenceIsNotZero(t *testing.T) {
	var c Component
	if c.Available {
		t.Error("a zero Component must not read as available")
	}
	full := FullResult{
		Materials: Result{TotalCAD: 100, BottleCount: 200},
		Labour:    Component{Missing: "no labour rate is set"},
		Overhead:  Component{Missing: "no overhead basis is set"},
		TotalCAD:  100,
	}
	if full.Complete {
		t.Error("a cost missing two of its three components claimed to be complete")
	}
	if got, want := full.PerBottleCAD(), 0.5; got != want {
		t.Errorf("per bottle = %v, want %v — the figure is still worth showing", got, want)
	}
}

func TestPerBottleWithNoBottles(t *testing.T) {
	// A run that bottled nothing divides by zero if this is wrong, and a
	// NaN in a cost of sales line is worse than a gap.
	full := FullResult{Materials: Result{TotalCAD: 100}, TotalCAD: 100}
	if got := full.PerBottleCAD(); got != 0 {
		t.Errorf("per bottle = %v, want 0", got)
	}
}
