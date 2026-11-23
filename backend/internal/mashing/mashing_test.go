package mashing

import (
	"math"
	"testing"
)

func TestGelatinisationCoverage(t *testing.T) {
	// Cereals the curriculum gives a figure for.
	for _, tc := range []struct {
		c        Cereal
		min, max float64
	}{
		{CerealBarley, 61, 62},
		{CerealWheat, 52, 65},
		{CerealRye, 60, 65},
		{CerealMaize, 70, 80},
		{CerealRice, 70, 80},
	} {
		g, ok := Gelatinisation(tc.c)
		if !ok {
			t.Errorf("cereal %v should have a published range", tc.c)
			continue
		}
		if g.MinC != tc.min || g.MaxC != tc.max {
			t.Errorf("cereal %v = %v–%v, want %v–%v", tc.c, g.MinC, g.MaxC, tc.min, tc.max)
		}
	}
	// Oats have no figure in the curriculum, so we must not invent one.
	if _, ok := Gelatinisation(CerealOat); ok {
		t.Error("oats have no published gelatinisation range — must report unknown, not guess")
	}
	if _, ok := Gelatinisation(CerealUnspecified); ok {
		t.Error("unspecified cereal must report unknown")
	}
}

// TestMaizeForcesCerealCook is the finding the bench exists to surface:
// maize gelatinises above the window where the amylases survive.
func TestMaizeForcesCerealCook(t *testing.T) {
	b := Assess([]GrainBillItem{
		{Name: "Flaked maize", Cereal: CerealMaize, MassKg: 80, Extract: 0.72},
		{Name: "Malted barley", Cereal: CerealBarley, MassKg: 20, Extract: 0.78, Malted: true},
	}, Readings{})

	if !b.CerealCookRequired {
		t.Fatal("a maize bill must require a separate cereal cook")
	}
	if !hasCode(b.Findings, "cereal_cook_required") {
		t.Error("want a cereal_cook_required finding")
	}
	if b.GelatinisationC.MaxC != 80 {
		t.Errorf("bill gelatinisation ceiling = %v, want 80 (driven by the maize)", b.GelatinisationC.MaxC)
	}
}

func TestAllBarleyMashesInOneRest(t *testing.T) {
	b := Assess([]GrainBillItem{
		{Name: "Malted barley", Cereal: CerealBarley, MassKg: 100, Extract: 0.78, Malted: true},
	}, Readings{})
	if b.CerealCookRequired {
		t.Error("an all-barley bill gelatinises inside the conversion window")
	}
	if !hasCode(b.Findings, "single_rest") {
		t.Error("want a single_rest finding")
	}
}

func TestUnknownCerealIsReportedNotGuessed(t *testing.T) {
	b := Assess([]GrainBillItem{
		{Name: "Malted barley", Cereal: CerealBarley, MassKg: 50, Extract: 0.78},
		{Name: "Naked oats", Cereal: CerealOat, MassKg: 50, Extract: 0.60},
	}, Readings{})
	if b.GelatinisationKnown {
		t.Error("a bill containing a cereal with no published figure is not fully known")
	}
	if !hasCode(b.Findings, "cereal_unknown") {
		t.Error("want the operator told which material is missing a cereal")
	}
}

func TestPHFindings(t *testing.T) {
	low, high, ok := 4.8, 5.9, 5.4
	if f := findingsFor(Readings{PH: &low}); !hasCode(f, "ph_out_of_band") {
		t.Error("low pH should be flagged")
	}
	if f := findingsFor(Readings{PH: &high}); !hasCode(f, "ph_out_of_band") {
		t.Error("high pH should be flagged")
	}
	if f := findingsFor(Readings{PH: &ok}); hasCode(f, "ph_out_of_band") {
		t.Error("pH 5.4 sits in the optimum and should not be flagged")
	}
}

func TestMashTempFindings(t *testing.T) {
	for _, tc := range []struct {
		name string
		temp float64
		code string
	}{
		{"denaturing", 82, "temp_denaturing"},
		{"above window", 73, "temp_above_conversion"},
		{"below window", 55, "temp_below_conversion"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if f := findingsFor(Readings{MashTempC: &tc.temp}); !hasCode(f, tc.code) {
				t.Errorf("%.0f °C should raise %s", tc.temp, tc.code)
			}
		})
	}
	good := 64.0
	if f := findingsFor(Readings{MashTempC: &good}); hasCode(f, "temp_above_conversion") || hasCode(f, "temp_below_conversion") {
		t.Error("64 °C is a normal conversion rest")
	}
}

func TestPlatoFromSG(t *testing.T) {
	// Anchors every brewer and distiller knows.
	for _, tc := range []struct{ sg, plato float64 }{
		{1.040, 9.96},
		{1.060, 14.66},
		{1.000, 0},
	} {
		if got := PlatoFromSG(tc.sg); math.Abs(got-tc.plato) > 0.05 {
			t.Errorf("PlatoFromSG(%.3f) = %.2f, want ~%.2f", tc.sg, got, tc.plato)
		}
	}
}

func TestEfficiencyFromGravity(t *testing.T) {
	og, water, wash := 1.055, 300.0, 280.0
	barley := []GrainBillItem{{Name: "Malted barley", Cereal: CerealBarley, MassKg: 100, Extract: 0.78}}
	if b := Assess(barley, Readings{OriginalGravity: &og, WaterVolumeL: &water}); b.Efficiency != nil || !hasCode(b.Findings, "no_wash_volume") {
		t.Error("liquor in is not wash out; with no wash volume there is no efficiency")
	}
	b := Assess(barley, Readings{OriginalGravity: &og, WashVolumeL: &wash})
	if b.Efficiency == nil {
		t.Fatal("want an efficiency figure")
	}
	if b.Efficiency.ExtractAvailableKg != 78 {
		t.Errorf("available extract = %v, want 78", b.Efficiency.ExtractAvailableKg)
	}
	if b.Efficiency.Percent < 50 || b.Efficiency.Percent > 90 {
		t.Errorf("efficiency %.1f %% is outside any believable range", b.Efficiency.Percent)
	}
	part := Assess(append(barley, GrainBillItem{Name: "Rye", Cereal: CerealRye, MassKg: 50}), Readings{OriginalGravity: &og, WashVolumeL: &wash})
	if part.Efficiency != nil || !hasCode(part.Findings, "partial_extract_data") {
		t.Error("a grain with no extract figure inflates the efficiency of the rest")
	}
}

func TestEfficiencyOver100IsCalledOut(t *testing.T) {
	og, wash := 1.090, 400.0
	b := Assess([]GrainBillItem{
		{Name: "Malted barley", Cereal: CerealBarley, MassKg: 30, Extract: 0.78},
	}, Readings{OriginalGravity: &og, WashVolumeL: &wash})
	if !hasCode(b.Findings, "efficiency_impossible") {
		t.Error("an impossible efficiency should be reported as bad inputs, not celebrated")
	}
}

func TestStrikeTemperature(t *testing.T) {
	// Warmer target and colder grain both push the strike temperature up.
	base, err := StrikeTemperature(64, 18, 3.0)
	if err != nil {
		t.Fatal(err)
	}
	colder, _ := StrikeTemperature(64, 5, 3.0)
	if !(colder > base) {
		t.Error("colder grain needs hotter liquor")
	}
	thinner, _ := StrikeTemperature(64, 18, 3.5)
	if !(thinner < base) {
		t.Error("a thinner mash needs less overshoot — more water, more thermal mass")
	}
	// Sanity against the energy balance done by hand:
	// 64 + (0.4115/3.0)(64−18) = 64 + 6.31 = 70.31
	want := 64 + (SpecificHeatRatio/3.0)*(64-18)
	if math.Abs(base-want) > 1e-9 {
		t.Errorf("strike = %v, want %v", base, want)
	}
	if _, err := StrikeTemperature(64, 18, 0); err == nil {
		t.Error("zero thickness must be refused, not divided by")
	}
}

func TestPlanStrikeFlagsDenaturingLiquor(t *testing.T) {
	// A very thick mash aiming high with cold grain.
	p, err := PlanStrike(70, 2, 0.5, 100)
	if err != nil {
		t.Fatal(err)
	}
	if p.StrikeTempC < EnzymeDenaturationC {
		t.Fatalf("expected this case to exceed the denaturation point, got %.1f", p.StrikeTempC)
	}
	if !hasCode(p.Findings, "strike_denatures") {
		t.Error("liquor hot enough to denature the amylases must be flagged")
	}
	if p.WaterVolumeL != 50 {
		t.Errorf("water volume = %v, want 50", p.WaterVolumeL)
	}
}

func findingsFor(r Readings) []Finding {
	return Assess([]GrainBillItem{
		{Name: "Malted barley", Cereal: CerealBarley, MassKg: 100, Extract: 0.78},
	}, r).Findings
}

func hasCode(fs []Finding, code string) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}
