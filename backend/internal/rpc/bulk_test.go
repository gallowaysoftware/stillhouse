package rpc

import (
	"math"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestApplyDeposit(t *testing.T) {
	tests := []struct {
		name        string
		curVol      float64
		curABVValid bool
		curABV      float64
		addVol      float64
		addABV      float64
		wantVol     float64
		wantABV     float64
		wantABVOK   bool
		wantLAA     float64
	}{
		{
			name:    "deposit into empty container",
			curVol:  0,
			addVol:  100, addABV: 70,
			wantVol: 100, wantABV: 70, wantABVOK: true, wantLAA: 70,
		},
		{
			name:        "deposit at same ABV grows volume",
			curVol:      100, curABVValid: true, curABV: 70,
			addVol: 50, addABV: 70,
			wantVol: 150, wantABV: 70, wantABVOK: true, wantLAA: 105,
		},
		{
			name:        "deposit at different ABV produces weighted average",
			curVol:      100, curABVValid: true, curABV: 70,
			addVol: 50, addABV: 60,
			// (100*70 + 50*60) / 150 = 66.66666...
			wantVol: 150, wantABV: 66.66666666666667, wantABVOK: true, wantLAA: 100,
		},
		{
			name:        "deposit small volume at high ABV barely shifts mix",
			curVol:      1000, curABVValid: true, curABV: 40,
			addVol: 1, addABV: 100,
			// (1000*40 + 1*100)/1001 ≈ 40.05994
			// LAA = 1001 × 40.05994/100 ≈ 401 (was 400 before deposit, +1 LAA added)
			wantVol: 1001, wantABV: 40.05994, wantABVOK: true, wantLAA: 401,
		},
		{
			name:    "first fill at fractional ABV",
			curVol:  0,
			addVol:  53, addABV: 62.5,
			// 53 × 0.625 = 33.125
			wantVol: 53, wantABV: 62.5, wantABVOK: true, wantLAA: 33.125,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			curABV := pgtype.Float8{Valid: tc.curABVValid, Float64: tc.curABV}
			gotVol, gotABV, gotLAA := applyDeposit(tc.curVol, curABV, tc.addVol, tc.addABV)
			if !floatNear(gotVol, tc.wantVol, 1e-9) {
				t.Errorf("volume: got %v want %v", gotVol, tc.wantVol)
			}
			if gotABV.Valid != tc.wantABVOK {
				t.Errorf("abv valid: got %v want %v", gotABV.Valid, tc.wantABVOK)
			}
			if tc.wantABVOK && !floatNear(gotABV.Float64, tc.wantABV, 1e-3) {
				t.Errorf("abv: got %v want %v", gotABV.Float64, tc.wantABV)
			}
			if !floatNear(gotLAA, tc.wantLAA, 1e-2) {
				t.Errorf("laa: got %v want %v", gotLAA, tc.wantLAA)
			}
		})
	}
}

func floatNear(a, b, tol float64) bool { return math.Abs(a-b) <= tol }
