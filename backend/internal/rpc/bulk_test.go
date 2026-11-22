package rpc

import (
	"math"
	"testing"

	"connectrpc.com/connect"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

func floatNear(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestBalanceAdd(t *testing.T) {
	for _, tc := range []struct {
		name               string
		have               balance
		volume, abv        float64
		wantABV, wantTotal float64
	}{
		{"into an empty vessel", balance{}, 250, 63, 63, 157.5},
		{"same strength", balance{100, 70}, 50, 70, 70, 105},
		{"by alcohol, not the average of the strengths", balance{900, 360}, 100, 90, 45, 450},
		{"stronger into weaker", balance{500, 200}, 100, 94.5, 49.083333, 294.5},
		{"water into spirit", balance{100, 60}, 50, 0, 40, 60},
	} {
		got := tc.have.add(tc.volume, tc.volume*tc.abv/100)
		if !floatNear(got.laa, tc.wantTotal, 1e-9) || !floatNear(got.abv().Float64, tc.wantABV, 1e-6) {
			t.Errorf("%s: %+v at %v, want %v LAA at %v", tc.name, got, got.abv().Float64, tc.wantTotal, tc.wantABV)
		}
	}
}

func TestBalanceTake(t *testing.T) {
	for _, tc := range []struct {
		name         string
		have         balance
		volume, laa  float64
		want         balance
		wantStranded float64
		refused      bool
	}{
		{"part", balance{100, 60}, 40, 24, balance{60, 36}, 0, false},
		{"all", balance{100, 60}, 100, 60, balance{}, 0, false},
		{"float noise around all", balance{100, 60}, 100 + 1e-9, 60 - 1e-9, balance{}, 0, false},
		{"all the litres, not all the alcohol", balance{100, 60}, 100, 59, balance{}, 1, false},
		{"more litres than it holds", balance{100, 60}, 120, 48, balance{}, 0, true},
		{"more alcohol than it holds", balance{100, 60}, 80, 61, balance{}, 0, true},
		{"leaving more alcohol than litres", balance{100, 60}, 50, 0, balance{}, 0, true},
	} {
		got, stranded, err := tc.have.take("T", tc.volume, tc.laa)
		if (err != nil) != tc.refused {
			t.Errorf("%s: err %v, want refused %v", tc.name, err, tc.refused)
			continue
		}
		if err != nil {
			if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if !floatNear(got.volumeL, tc.want.volumeL, 1e-6) || !floatNear(got.laa, tc.want.laa, 1e-6) ||
			!floatNear(stranded, tc.wantStranded, 1e-6) {
			t.Errorf("%s: %+v stranded %v, want %+v stranded %v", tc.name, got, stranded, tc.want, tc.wantStranded)
		}
	}
}

// The screen shows 60.67 for 60.6667, and a draw that empties the vessel at
// the strength shown takes everything.
func TestDrawnLAATakesEverythingAtTheStrengthShown(t *testing.T) {
	c := sqlcgen.BulkContainer{CurrentVolumeL: 150, CurrentLaa: 91}
	if got := drawnLAA(c, 150, 60.67); got != 91 {
		t.Errorf("emptied at the strength shown: %v, want 91", got)
	}
	if got := drawnLAA(c, 100, 60.67); !floatNear(got, 60.67, 1e-9) {
		t.Errorf("part draw: %v, want the gauge's 60.67", got)
	}
	if got := drawnLAA(c, 150, 59); !floatNear(got, 88.5, 1e-9) {
		t.Errorf("emptied at a different gauge: %v, want the gauge's 88.5", got)
	}
}

func TestLossRowIsAlwaysWritable(t *testing.T) {
	for _, tc := range []struct{ lost, held, laa float64 }{
		{5, 200, 3}, {0, 200, 2}, {-0.5, 200, 3.7}, {1, 200, 2.5}, {0.1, 200, 1},
	} {
		vol, abv := lossRow(tc.lost, tc.held, tc.laa)
		if vol <= 0 || abv < 0 || abv > 100 || !floatNear(vol*abv/100, tc.laa, 1e-9) {
			t.Errorf("lossRow(%v, %v, %v) = %v L at %v", tc.lost, tc.held, tc.laa, vol, abv)
		}
	}
}
