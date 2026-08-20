package alcoholometry

import (
	"math"
	"testing"
)

// TestReductionMatchesCurriculumVolume checks the final-volume half of the
// calculation against the worked example in CIBD Module 2 (pre-package
// blending): 1,000 L at 63 % reduced to 40 % gives 1,575 L.
func TestReductionMatchesCurriculumVolume(t *testing.T) {
	requireTables(t)
	r, err := PlanReduction(1000, 63, 40)
	if err != nil {
		t.Fatalf("PlanReduction: %v", err)
	}
	if math.Abs(r.FinalVolumeL-1575) > 1e-9 {
		t.Errorf("final volume = %.4f, want 1575", r.FinalVolumeL)
	}
	// The curriculum then says "you therefore add 575 litres" — the naive
	// figure. We keep it for comparison but do not hand it to the operator
	// as the water to meter in.
	if math.Abs(r.NaiveWaterL-575) > 1e-9 {
		t.Errorf("naive water = %.4f, want 575", r.NaiveWaterL)
	}
	t.Logf("naive %.1f L vs contraction-corrected %.1f L (%.1f L extra, %.2f %% of final volume)",
		r.NaiveWaterL, r.WaterToAddL, r.ContractionL, r.ContractionL/r.FinalVolumeL*100)
}

// TestContractionIsRealAndBounded — the curriculum puts the volume
// contraction of an ethanol/water blend at one to two percent. The water
// that has to be added is correspondingly more than a volume balance
// suggests, and the correction should land in that neighbourhood rather
// than being either zero or wild.
func TestContractionIsRealAndBounded(t *testing.T) {
	requireTables(t)
	r, err := PlanReduction(1000, 63, 40)
	if err != nil {
		t.Fatal(err)
	}
	if r.ContractionL <= 0 {
		t.Fatalf("contraction = %.4f; mixing ethanol and water must swallow volume, not release it", r.ContractionL)
	}
	// Expressed against the final volume, the correction should sit inside
	// the curriculum's one-to-two-percent band.
	pct := r.ContractionL / r.FinalVolumeL * 100
	if pct < 0.5 || pct > 2.5 {
		t.Errorf("contraction is %.2f %% of final volume — outside the plausible 1–2 %% band", pct)
	}
}

// TestReductionConservesAlcohol is the invariant that matters: water
// cannot create or destroy absolute alcohol, so the LAA before and after
// must match. This is the same property stage 109 had to fix in bottling.
func TestReductionConservesAlcohol(t *testing.T) {
	requireTables(t)
	for _, tc := range []struct{ vol, from, to float64 }{
		{1000, 63, 40},
		{5000, 62, 40},
		{250, 94.5, 37.5},
		{75.5, 60, 46},
	} {
		r, err := PlanReduction(tc.vol, tc.from, tc.to)
		if err != nil {
			t.Fatalf("PlanReduction(%v): %v", tc, err)
		}
		startLAA := tc.vol * tc.from / 100
		endLAA := r.FinalVolumeL * tc.to / 100
		if math.Abs(startLAA-endLAA) > 1e-6 {
			t.Errorf("%v: LAA %.6f -> %.6f, alcohol was not conserved", tc, startLAA, endLAA)
		}
		if math.Abs(r.LAA-startLAA) > 1e-9 {
			t.Errorf("%v: reported LAA %.6f, want %.6f", tc, r.LAA, startLAA)
		}
	}
}

// TestCurriculumClaimIsWrong documents a checked discrepancy. A claim
// extracted from the curriculum states that 5,000 L at 62 % reduced to
// 40 % needs "approximately 3,250 litres" of water. Conservation of
// alcohol puts the final volume at 7,750 L, so the naive water figure is
// 2,750 L and the corrected one is near it — nowhere close to 3,250.
//
// Kept as a test so nobody "fixes" the calculation to match the claim.
func TestCurriculumClaimIsWrong(t *testing.T) {
	requireTables(t)
	r, err := PlanReduction(5000, 62, 40)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(r.FinalVolumeL-7750) > 1e-9 {
		t.Errorf("final volume = %.2f, want 7750", r.FinalVolumeL)
	}
	if r.WaterToAddL > 3100 {
		t.Errorf("water = %.1f L; the 3,250 L figure would imply a final strength near 37.6 %%, not 40 %%",
			r.WaterToAddL)
	}
}

func TestReductionRefusesImpossibleTargets(t *testing.T) {
	requireTables(t)
	// Water cannot raise strength.
	if _, err := PlanReduction(100, 40, 60); err == nil {
		t.Error("reducing 40 % to 60 % must be refused, not solved with negative water")
	}
	if _, err := PlanReduction(100, 40, 40); err == nil {
		t.Error("a no-op reduction must be refused rather than returning zero water")
	}
	if _, err := PlanReduction(0, 60, 40); err == nil {
		t.Error("zero volume must be refused")
	}
	if _, err := PlanReduction(100, 0, 40); err == nil {
		t.Error("zero starting strength must be refused")
	}
	if _, err := PlanReduction(100, 105, 40); err == nil {
		t.Error("a strength above 100 % must be refused")
	}
}

// TestReductionToBottlingStrengths walks the band the curriculum gives for
// bottling — 37 % to 50 % — from a typical cask strength.
func TestReductionToBottlingStrengths(t *testing.T) {
	requireTables(t)
	for target := 37.0; target <= 50.0; target += 0.5 {
		r, err := PlanReduction(500, 62, target)
		if err != nil {
			t.Fatalf("target %.1f: %v", target, err)
		}
		if r.WaterToAddL <= 0 {
			t.Errorf("target %.1f: water %.2f should be positive", target, r.WaterToAddL)
		}
		if r.FinalVolumeL <= 500 {
			t.Errorf("target %.1f: final volume %.2f should exceed the starting 500 L", target, r.FinalVolumeL)
		}
		// Weaker target → more water, monotonically.
		if target > 37.0 {
			prev, _ := PlanReduction(500, 62, target-0.5)
			if !(r.WaterToAddL < prev.WaterToAddL) {
				t.Errorf("target %.1f needs %.2f L, but %.1f needed %.2f — should decrease as target rises",
					target, r.WaterToAddL, target-0.5, prev.WaterToAddL)
			}
		}
	}
}

// TestReductionByWeightIsExact — the point of working in mass. The water
// mass is a plain difference with no correction term, and adding it to the
// starting mass must land exactly on the final mass. No contraction, no
// fudge factor.
func TestReductionByWeightIsExact(t *testing.T) {
	requireTables(t)
	r, err := PlanReduction(1000, 63, 40)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs((r.FromMassKg+r.WaterToAddKg)-r.FinalMassKg) > 1e-9 {
		t.Errorf("mass balance doesn't close: %.6f + %.6f != %.6f",
			r.FromMassKg, r.WaterToAddKg, r.FinalMassKg)
	}
	if r.FromMassKg <= 0 || r.WaterToAddKg <= 0 {
		t.Errorf("masses must be positive: from %.3f, water %.3f", r.FromMassKg, r.WaterToAddKg)
	}
	// Spirit at 63 % is lighter than water, so 1,000 L weighs well under
	// a tonne; the blend at 40 % is denser.
	if !(r.FromMassKg < 1000) {
		t.Errorf("1,000 L of 63 %% spirit should weigh under 1,000 kg, got %.1f", r.FromMassKg)
	}
	t.Logf("weigh it: %.1f kg spirit + %.1f kg water = %.1f kg at %.0f %%",
		r.FromMassKg, r.WaterToAddKg, r.FinalMassKg, r.ToStrength)
}

// TestMassVolumeRoundTrip — the two gauging modes must describe the same
// liquid.
func TestMassVolumeRoundTrip(t *testing.T) {
	requireTables(t)
	for _, tc := range []struct{ strength, volume float64 }{
		{63, 1000}, {40, 1575}, {94.5, 250}, {5, 800},
	} {
		mass, err := MassFromVolume(tc.strength, tc.volume)
		if err != nil {
			t.Fatalf("MassFromVolume(%v): %v", tc, err)
		}
		back, err := VolumeFromMass(tc.strength, mass)
		if err != nil {
			t.Fatalf("VolumeFromMass(%v): %v", tc, err)
		}
		if math.Abs(back-tc.volume) > 1e-6 {
			t.Errorf("%v: round trip %.6f -> %.6f", tc, tc.volume, back)
		}
	}
}

// TestPlanFromMassMatchesPlanFromVolume — entering the same charge as a
// weight or as a volume must give the same plan.
func TestPlanFromMassMatchesPlanFromVolume(t *testing.T) {
	requireTables(t)
	byVolume, err := PlanReduction(1000, 63, 40)
	if err != nil {
		t.Fatal(err)
	}
	byMass, err := PlanReductionFromMass(byVolume.FromMassKg, 63, 40)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(byMass.FinalVolumeL-byVolume.FinalVolumeL) > 1e-6 {
		t.Errorf("final volume differs by entry mode: %.6f vs %.6f",
			byMass.FinalVolumeL, byVolume.FinalVolumeL)
	}
	if math.Abs(byMass.WaterToAddKg-byVolume.WaterToAddKg) > 1e-6 {
		t.Errorf("water mass differs by entry mode: %.6f vs %.6f",
			byMass.WaterToAddKg, byVolume.WaterToAddKg)
	}
}

func TestPlanFromMassRefusesBadInput(t *testing.T) {
	requireTables(t)
	if _, err := PlanReductionFromMass(0, 63, 40); err == nil {
		t.Error("zero mass must be refused")
	}
	if _, err := PlanReductionFromMass(900, 40, 63); err == nil {
		t.Error("water cannot raise strength, by mass either")
	}
}
