package units

import (
	"math"
	"testing"
)

func TestKg(t *testing.T) {
	for _, tc := range []struct {
		q    float64
		uom  string
		want float64
		ok   bool
	}{
		{25, "kg", 25, true}, {25, " Kg ", 25, true}, {750, "g", 0.75, true}, {1, "t", 1000, true},
		{55.1155655, "lb", 25, true}, {5, "L", 0, false}, {5, "", 0, false}, {5, "each", 0, false},
	} {
		got, ok := Kg(tc.q, tc.uom)
		if ok != tc.ok || math.Abs(got-tc.want) > 1e-6 {
			t.Errorf("Kg(%v, %q) = %v, %v; want %v, %v", tc.q, tc.uom, got, ok, tc.want, tc.ok)
		}
	}
}
