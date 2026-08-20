package distilling

import (
	"math"
	"testing"
)

// A 100 % malted barley bill at 78 % extract — the curriculum's reference
// material.
func maltBill(massKg float64) []Ingredient {
	return []Ingredient{{Name: "Malted barley", MassKg: massKg, ExtractPct: 0.78}}
}

func TestYieldBenchmarksForDistillingMalt(t *testing.T) {
	// Project at a realistic 84 % overall efficiency.
	eff := Efficiencies{Mash: 0.92, Ferment: 0.95, DistillationRecovery: 0.96}
	p := ProjectBatch(maltBill(1000), eff)
	y := CheckYield(maltBill(1000), p.TotalProjectedLAA)

	if !y.Measurable {
		t.Fatal("a bill with mass and extract must be measurable")
	}
	// Stoichiometric ceiling for 78 % extract: 0.78 × 1000 × 0.511 ÷ 0.78934.
	wantMax := 0.78 * 1000 * GayLussacRatio / EthanolDensityKgPerL
	if math.Abs(y.TheoreticalMaxLPerTonne-wantMax) > 1e-6 {
		t.Errorf("theoretical max = %.2f, want %.2f", y.TheoreticalMaxLPerTonne, wantMax)
	}
	// The curriculum's empirical figure, applied to the FERMENTABLE share
	// of the extract rather than all of it.
	wantAchievable := 78 * SpiritMaltFermentability * EmpiricalYieldLPerExtractPointPerTonne
	if math.Abs(y.AchievableLPerTonne-wantAchievable) > 1e-6 {
		t.Errorf("achievable = %.2f, want %.2f", y.AchievableLPerTonne, wantAchievable)
	}
	// The three anchors must stay in order, or one of the constants is
	// wrong: what a good process gets < what the best malt targets <
	// what the chemistry allows.
	if !(y.AchievableLPerTonne < MaxPredictedSpiritYieldLPerTonne &&
		MaxPredictedSpiritYieldLPerTonne < y.TheoreticalMaxLPerTonne) {
		t.Errorf("benchmarks out of order: achievable %.0f, best-malt target %.0f, ceiling %.0f",
			y.AchievableLPerTonne, MaxPredictedSpiritYieldLPerTonne, y.TheoreticalMaxLPerTonne)
	}
	// The empirical figure must sit below the stoichiometric one — it
	// already carries real losses. If this ever inverts, one of the two
	// constants is wrong.
	if !(y.AchievableLPerTonne < y.TheoreticalMaxLPerTonne) {
		t.Errorf("achievable %.2f should be below theoretical %.2f",
			y.AchievableLPerTonne, y.TheoreticalMaxLPerTonne)
	}
	t.Logf("84 %% process: %.0f L/tonne (achievable %.0f, ceiling %.0f)",
		y.LPerTonne, y.AchievableLPerTonne, y.TheoreticalMaxLPerTonne)
}

// TestPerfectEfficienciesAreImpossible is the case this check exists for:
// a recipe left with every efficiency at 1.0 projects a number no
// distillery has ever achieved, and it looks as confident as a real one.
func TestPerfectEfficienciesAreImpossible(t *testing.T) {
	eff := Efficiencies{Mash: 1, Ferment: 1, DistillationRecovery: 1}
	p := ProjectBatch(maltBill(1000), eff)
	y := CheckYield(maltBill(1000), p.TotalProjectedLAA)

	// At 100 % across the board the projection lands exactly on the
	// stoichiometric ceiling, not above it — so it trips the "above the
	// best malt" band rather than the impossible one.
	if len(y.Findings) == 0 {
		t.Fatal("a recipe with every efficiency at 1.0 must raise something")
	}
	if !has(y.Findings, "yield_above_best_malt") && !has(y.Findings, "yield_exceeds_theoretical") {
		t.Errorf("want a yield warning, got %+v", y.Findings)
	}
}

func TestImpossibleYieldIsFlagged(t *testing.T) {
	// A projection that could only come from a bad extract percentage.
	y := CheckYield(maltBill(1000), 600)
	if !has(y.Findings, "yield_exceeds_theoretical") {
		t.Errorf("600 L/tonne from 78 %% extract is impossible; got %+v", y.Findings)
	}
	if sev(y.Findings, "yield_exceeds_theoretical") != SeverityProblem {
		t.Error("physically impossible is a problem, not a nicety")
	}
}

func TestOptimisticYieldIsQueried(t *testing.T) {
	// Between the achievable benchmark (411) and the stoichiometric
	// ceiling (505) — possible, but ahead of industry.
	y := CheckYield(maltBill(1000), 490)
	if len(y.Findings) == 0 {
		t.Fatal("a projection ahead of the benchmark should say so")
	}
	if has(y.Findings, "yield_exceeds_theoretical") {
		t.Error("490 L/tonne is under the ceiling — not impossible, just optimistic")
	}
}

func TestConservativeYieldIsNoted(t *testing.T) {
	y := CheckYield(maltBill(1000), 250)
	if !has(y.Findings, "yield_conservative") {
		t.Errorf("300 L/tonne leaves yield in the tun; got %+v", y.Findings)
	}
	if sev(y.Findings, "yield_conservative") != SeverityInfo {
		t.Error("a conservative plan is information, not a warning")
	}
}

func TestRealisticYieldSaysNothing(t *testing.T) {
	// 78 % extract × 87 % fermentable × 6.06 = 411 L/tonne achievable;
	// landing just under it is a good plan, not a finding.
	y := CheckYield(maltBill(1000), 400)
	for _, f := range y.Findings {
		t.Errorf("a realistic projection should be quiet, got: %s", f.Title)
	}
}

func TestYieldScalesWithBatchSize(t *testing.T) {
	// L/tonne is size-independent: the same recipe at any scale reads the
	// same, which is the point of quoting it that way.
	eff := Efficiencies{Mash: 0.9, Ferment: 0.9, DistillationRecovery: 0.9}
	small := ProjectBatch(maltBill(250), eff)
	large := ProjectBatch(maltBill(4000), eff)
	ys := CheckYield(maltBill(250), small.TotalProjectedLAA)
	yl := CheckYield(maltBill(4000), large.TotalProjectedLAA)
	if math.Abs(ys.LPerTonne-yl.LPerTonne) > 0.05 {
		t.Errorf("L/tonne should not depend on batch size: %.3f vs %.3f", ys.LPerTonne, yl.LPerTonne)
	}
}

func TestMixedBillUsesWeightedExtract(t *testing.T) {
	bill := []Ingredient{
		{Name: "Maize", MassKg: 800, ExtractPct: 0.72},
		{Name: "Malted barley", MassKg: 200, ExtractPct: 0.78},
	}
	y := CheckYield(bill, 400)
	want := (800*0.72 + 200*0.78) / 1000
	if math.Abs(y.WeightedExtractPct-want) > 1e-9 {
		t.Errorf("weighted extract = %.5f, want %.5f", y.WeightedExtractPct, want)
	}
}

func TestNoExtractDataIsNotMeasurable(t *testing.T) {
	y := CheckYield([]Ingredient{{Name: "Mystery grain", MassKg: 500}}, 200)
	if y.Measurable {
		t.Error("a bill with no extract percentage cannot be benchmarked")
	}
	if len(y.Findings) != 0 {
		t.Error("and it must not invent findings about it")
	}
}

// TestCheckYieldCatchesPercentAsFraction: found by QA. A material's extract
// is a fraction, but the field is named extract_pct, so 78 gets typed where
// 0.78 belongs. 340 kg of rye then projected 26,520 kg of fermentable
// extract, a 1077% ABV wash and 14,270 LAA from 400 kg of grain.
//
// The check didn't catch it, and couldn't: the "physically impossible"
// ceiling is computed from the same extract figure, so it rescaled with the
// error — 35,675 L/tonne against a "theoretical max" of 50,689, graded
// merely a warning about optimistic efficiencies. A ceiling derived from
// the suspect input cannot test that input. Extract above 1.0 is the one
// thing that needs no comparison: more extract than grain is not a
// process claim, it's a broken number.
func TestCheckYieldCatchesPercentAsFraction(t *testing.T) {
	y := CheckYield([]Ingredient{
		{Name: "Rye (unmalted)", MassKg: 340, ExtractPct: 78},
		{Name: "Malted barley", MassKg: 60, ExtractPct: 80},
	}, 14270.13)

	if !y.Measurable {
		t.Fatal("check reported not measurable")
	}
	if !hasCode(y.Findings, "extract_out_of_range") {
		t.Errorf("findings = %v, want extract_out_of_range — an extract of %.2f means more "+
			"extract than grain", codes(y.Findings), y.WeightedExtractPct)
	}
	for _, f := range y.Findings {
		if f.Code == "extract_out_of_range" && f.Severity != SeverityProblem {
			t.Errorf("extract_out_of_range severity = %v, want problem", f.Severity)
		}
	}
}

// A sane bill must stay quiet — the guard has to catch the unit slip
// without shouting at ordinary recipes.
func TestCheckYieldQuietOnSaneBill(t *testing.T) {
	y := CheckYield([]Ingredient{
		{Name: "Rye (unmalted)", MassKg: 340, ExtractPct: 0.78},
		{Name: "Malted barley", MassKg: 60, ExtractPct: 0.80},
	}, 142.70)

	if hasCode(y.Findings, "extract_out_of_range") {
		t.Errorf("extract_out_of_range fired on a normal bill (extract %.3f)", y.WeightedExtractPct)
	}
	if len(y.Findings) != 0 {
		t.Errorf("findings = %v, want none for a 357 L/tonne projection", codes(y.Findings))
	}
}

func hasCode(fs []Finding, code string) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}

func codes(fs []Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Code)
	}
	return out
}
