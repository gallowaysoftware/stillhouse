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

func TestDrawFrom(t *testing.T) {
	vessel := func(vol, laa float64) sqlcgen.BulkContainer {
		return sqlcgen.BulkContainer{Name: "T", CurrentVolumeL: vol, CurrentLaa: laa, CurrentAbvPct: balance{vol, laa}.abv()}
	}
	for _, tc := range []struct {
		name                string
		have                sqlcgen.BulkContainer
		volume, abv         float64
		wantVol, wantLAA    float64
		restVol, correction float64
	}{
		{"part, at book strength", vessel(200, 125), 50, 62.5, 50, 31.25, 150, 0},
		{"part, gauge a little off", vessel(1000, 600), 100, 61, 100, 61, 900, 0},
		{"all, at the strength shown for 60.6667", vessel(150, 91), 150, 60.67, 150, 91, 0, 0},
		{"all, at the litres shown for 211.33125", vessel(211.33125, 130), 211.33, 61.51, 211.33125, 130, 0, 0},
		{"all, at litres shown rounded up", vessel(176.16348, 100), 176.1635, 56.77, 176.16348, 100, 0, 0},
		{"all, gauged above book", vessel(200, 125), 200, 63, 200, 126, 0, 1},
		{"all, gauged below book", vessel(200, 125), 200, 62, 200, 124, 0, -1},
		{"all but a litre, below book", vessel(200, 125), 199, 62, 199, 123.38, 1, -1},
		{"all but a litre, above book", vessel(200, 125), 199, 63.5, 199, 126.365, 1, 2},
		{"most of it, a long way off", vessel(200, 125), 190, 61, 190, 115.9, 10, -3},
	} {
		d, err := drawFrom(tc.have, tc.volume, tc.abv)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if !floatNear(d.volumeL, tc.wantVol, 1e-9) || !floatNear(d.laa, tc.wantLAA, 1e-9) ||
			!floatNear(d.rest.volumeL, tc.restVol, 1e-9) || !floatNear(d.correction, tc.correction, 1e-9) {
			t.Errorf("%s: took %v L and %v LAA, left %v L, corrected %v; want %v, %v, %v, %v", tc.name,
				d.volumeL, d.laa, d.rest.volumeL, d.correction, tc.wantVol, tc.wantLAA, tc.restVol, tc.correction)
		}
		if !floatNear(tc.have.CurrentLaa+d.correction-d.laa, d.rest.laa, 1e-9) {
			t.Errorf("%s: %v + %v - %v is not the %v left", tc.name, tc.have.CurrentLaa, d.correction, d.laa, d.rest.laa)
		}
		if d.rest.volumeL > 0 && !floatNear(d.rest.abv().Float64, tc.have.CurrentAbvPct.Float64, fillStrengthToleranceABV) {
			t.Errorf("%s: the heel is booked at %v", tc.name, d.rest.abv().Float64)
		}
	}
	for name, try := range map[string][2]float64{
		"more than it holds": {200.1, 62.5}, "a strength it does not have": {100, 65}, "no strength": {100, 0},
	} {
		if _, err := drawFrom(vessel(200, 125), try[0], try[1]); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestGaugedLAA(t *testing.T) {
	c := sqlcgen.BulkContainer{Name: "C", CurrentVolumeL: 200, CurrentLaa: 120, CurrentAbvPct: balance{200, 120}.abv()}
	for _, tc := range []struct {
		volume, abv, laa, gain float64
		refused                bool
	}{
		{200, 60, 120, 0, false}, {200, 60.004, 120, 0, false}, {195, 59, 115.05, 0, false},
		{200, 60.1, 120.2, 0.2, false}, {200.5, 60, 120.3, 0.3, false}, {200, 63, 0, 0, true},
	} {
		laa, gain, err := gaugedLAA(c, tc.volume, tc.abv)
		if (err != nil) != tc.refused || (err == nil && (!floatNear(laa, tc.laa, 1e-9) || !floatNear(gain, tc.gain, 1e-9))) {
			t.Errorf("%v L at %v: %v, %v, %v", tc.volume, tc.abv, laa, gain, err)
		}
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
