package fermenting

import (
	"math"
	"testing"
	"time"
)

var t0 = time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)

func at(hours float64) time.Time { return t0.Add(time.Duration(hours * float64(time.Hour))) }

func g(hours, sg float64) Reading {
	return Reading{At: at(hours), Gravity: sg, GravitySet: true}
}

func gt(hours, sg, temp float64) Reading {
	return Reading{At: at(hours), Gravity: sg, GravitySet: true, TempC: temp, TempSet: true}
}

// TestABVAgreesWithTheWashBandTheCurriculumDescribes is the reason this
// package does not use the curriculum's own divide-by-four rule.
//
// That rule turns a 1.060 → 0.995 ferment into 16.25 % ABV. The same
// curriculum states a distillery wash finishes at 8–10 % ABV. Both cannot
// be true; the standard factor reproduces the band, so it wins.
func TestABVAgreesWithTheWashBandTheCurriculumDescribes(t *testing.T) {
	for _, tc := range []struct{ og, fg float64 }{
		{1.055, 1.000},
		{1.060, 1.000},
		{1.065, 0.998},
		{1.070, 1.000},
	} {
		abv := EstimateABV(tc.og, tc.fg)
		if abv < 7 || abv > 10.5 {
			t.Errorf("OG %.3f → FG %.3f gives %.2f %% ABV, outside the 8–10 %% wash band "+
				"the curriculum describes", tc.og, tc.fg, abv)
		}
	}
	// And the rule that was rejected would put this one at 16 %.
	if abv := EstimateABV(1.060, 0.995); abv > 10 {
		t.Errorf("1.060 → 0.995 gives %.2f %% — a 1.060 wort cannot hold that much alcohol", abv)
	}
}

func TestAttenuation(t *testing.T) {
	// 60 points down to 0 is complete attenuation.
	if got := AttenuationFromGravity(1.060, 1.000); math.Abs(got-100) > 1e-9 {
		t.Errorf("attenuation = %.4f, want 100", got)
	}
	// Half way.
	if got := AttenuationFromGravity(1.060, 1.030); math.Abs(got-50) > 1e-9 {
		t.Errorf("attenuation = %.4f, want 50", got)
	}
	// Below 1.000 over-attenuates past 100 %, which is real and expected
	// for a dry distillery wash.
	if got := AttenuationFromGravity(1.060, 0.995); got <= 100 {
		t.Errorf("attenuation = %.4f, want over 100 for a wash finishing under 1.000", got)
	}
	if got := AttenuationFromGravity(1.000, 1.000); got != 0 {
		t.Errorf("no starting extract means no attenuation, got %v", got)
	}
}

func TestPhaseProgression(t *testing.T) {
	cases := []struct {
		name string
		logs []Reading
		want Phase
	}{
		{"nothing has moved yet", []Reading{g(0, 1.060), g(6, 1.059)}, PhaseLag},
		{"falling fast", []Reading{g(0, 1.060), g(12, 1.045), g(24, 1.028)}, PhaseGrowth},
		{"flattened out", []Reading{g(0, 1.060), g(24, 1.020), g(48, 1.0195)}, PhaseStationary},
		{"a single reading is not a curve", []Reading{g(0, 1.060)}, PhaseLag},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Analyse(tc.logs, 0, false).Phase; got != tc.want {
				t.Errorf("phase = %v, want %v", got, tc.want)
			}
		})
	}
	// With a target to compare against, a flat curve that reached it is
	// finished rather than merely stationary.
	fin := Analyse([]Reading{g(0, 1.060), g(48, 1.001), g(72, 1.0005)}, 1.001, true)
	if fin.Phase != PhaseFinished {
		t.Errorf("phase = %v, want finished", fin.Phase)
	}
}

// TestStuckFermentationIsFlagged — the curriculum's marker is a wash
// sitting well above where it should have finished and no longer moving.
func TestStuckFermentationIsFlagged(t *testing.T) {
	logs := []Reading{g(0, 1.060), g(24, 1.030), g(48, 1.0205), g(72, 1.020), g(96, 1.020)}
	a := Analyse(logs, 1.002, true)
	if !has(a.Findings, "stuck_fermentation") {
		t.Errorf("a wash stalled 18 points above target should be flagged; got %+v", a.Findings)
	}
	// A ferment that reached its target must not be called stuck.
	done := Analyse([]Reading{g(0, 1.060), g(48, 1.005), g(72, 1.002), g(96, 1.002)}, 1.002, true)
	if has(done.Findings, "stuck_fermentation") {
		t.Error("a ferment that hit its target is finished, not stuck")
	}
}

func TestThermalStressIsFlagged(t *testing.T) {
	logs := []Reading{gt(0, 1.060, 30), gt(12, 1.045, 36.5), gt(24, 1.030, 31)}
	a := Analyse(logs, 0, false)
	if !has(a.Findings, "thermal_stress") {
		t.Errorf("36.5 °C should be flagged; got %+v", a.Findings)
	}
	if a.PeakTempC != 36.5 {
		t.Errorf("peak temp = %v, want 36.5", a.PeakTempC)
	}
	// A well-controlled ferment must stay quiet.
	ok := Analyse([]Reading{gt(0, 1.060, 32), gt(24, 1.020, 30), gt(48, 1.005, 28)}, 0, false)
	if has(ok.Findings, "thermal_stress") {
		t.Error("32 °C through growth easing to 28 °C is the controlled profile, not a finding")
	}
}

// TestPHCrashIsFlagged — lactic acid bacteria dropping the pH is the
// classic contamination signature.
func TestPHCrashIsFlagged(t *testing.T) {
	logs := []Reading{
		{At: at(0), PH: 5.2, PHSet: true, Gravity: 1.060, GravitySet: true},
		{At: at(48), PH: 4.1, PHSet: true, Gravity: 1.030, GravitySet: true},
	}
	if !has(Analyse(logs, 0, false).Findings, "ph_crash") {
		t.Error("a 1.1 point pH fall should be flagged as possible contamination")
	}
	// The normal working drop must not.
	normal := []Reading{
		{At: at(0), PH: 5.2, PHSet: true, Gravity: 1.060, GravitySet: true},
		{At: at(48), PH: 4.8, PHSet: true, Gravity: 1.010, GravitySet: true},
	}
	if has(Analyse(normal, 0, false).Findings, "ph_crash") {
		t.Error("a 0.4 point drop is the yeast working, not an infection")
	}
}

func TestNoGravityReadingsIsNotMeasurable(t *testing.T) {
	a := Analyse([]Reading{{At: at(0), TempC: 30, TempSet: true}}, 0, false)
	if a.Measurable {
		t.Error("without a gravity there is no attenuation to report")
	}
	// But a temperature problem is still worth saying.
	hot := Analyse([]Reading{{At: at(0), TempC: 38, TempSet: true}}, 0, false)
	if !has(hot.Findings, "thermal_stress") {
		t.Error("temperature findings should not need a gravity reading")
	}
}

func TestAnalysisReportsTheCurve(t *testing.T) {
	logs := []Reading{gt(0, 1.062, 30), gt(24, 1.030, 33), gt(72, 1.002, 28)}
	a := Analyse(logs, 1.002, true)
	if !a.Measurable {
		t.Fatal("should be measurable")
	}
	if a.OriginalGravity != 1.062 || a.CurrentGravity != 1.002 {
		t.Errorf("gravities = %.3f → %.3f, want 1.062 → 1.002", a.OriginalGravity, a.CurrentGravity)
	}
	if math.Abs(a.HoursElapsed-72) > 1e-9 {
		t.Errorf("elapsed = %v h, want 72", a.HoursElapsed)
	}
	wantABV := (1.062 - 1.002) * ABVPerGravityPoint
	if math.Abs(a.EstimatedABV-wantABV) > 1e-9 {
		t.Errorf("ABV = %.4f, want %.4f", a.EstimatedABV, wantABV)
	}
	if a.EstimatedABV < 7 || a.EstimatedABV > 10.5 {
		t.Errorf("a finished wash should land in the 8–10 %% band, got %.2f", a.EstimatedABV)
	}
}

func has(fs []Finding, code string) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}
