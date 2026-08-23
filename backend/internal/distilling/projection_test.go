package distilling

import (
	"math"
	"testing"
)

func TestProjectBatch_SingleGrain(t *testing.T) {
	// 100 kg of malted barley at 78% extract, 85% mash, 92% ferment, 90% distill.
	//   100 × 0.78 × 0.85          = 66.30 kg fermentable freed
	//   × 0.511 × 0.92             = 31.168956 kg ethanol
	//   ÷ 0.78934                  = 39.4873... L ethanol in wash
	//   × 0.90                     = 35.5386 L LAA captured
	in := []Ingredient{{Name: "Maris Otter", MassKg: 100, Extract: 0.78}}
	eff := Efficiencies{Mash: 0.85, Ferment: 0.92, DistillationRecovery: 0.90}
	p := ProjectBatch(in, eff)

	if got, want := len(p.PerIngredient), 1; got != want {
		t.Fatalf("len = %d, want %d", got, want)
	}
	if got, want := p.TotalProjectedLAA, 35.5386; !floatNear(got, want, 0.001) {
		t.Errorf("TotalProjectedLAA = %v, want ~%v", got, want)
	}
	r := p.PerIngredient[0]
	if got, want := r.EthanolMassKg, 31.168956; !floatNear(got, want, 0.0001) {
		t.Errorf("EthanolMassKg = %v, want ~%v", got, want)
	}
}

func TestProjectBatch_MixedMashBill(t *testing.T) {
	// A pretend Canadian rye-style mash bill: 70 kg rye + 25 kg corn + 5 kg malt.
	// Sum of per-ingredient projected LAA must equal Total.
	in := []Ingredient{
		{Name: "Rye", MassKg: 70, Extract: 0.65},
		{Name: "Corn", MassKg: 25, Extract: 0.72},
		{Name: "Malted barley", MassKg: 5, Extract: 0.78},
	}
	eff := Efficiencies{Mash: 0.85, Ferment: 0.92, DistillationRecovery: 0.90}
	p := ProjectBatch(in, eff)

	sum := 0.0
	for _, r := range p.PerIngredient {
		sum += r.ProjectedLAA
	}
	if got, want := p.TotalProjectedLAA, round4(sum); !floatNear(got, want, 0.001) {
		t.Errorf("Total %v != Σ per-ingredient %v", got, want)
	}
	if p.TotalProjectedLAA <= 0 {
		t.Errorf("TotalProjectedLAA should be positive, got %v", p.TotalProjectedLAA)
	}
}

func TestProjectBatch_RoundingAndDegenerate(t *testing.T) {
	// Empty / zero inputs yield zero.
	cases := []struct {
		name string
		in   []Ingredient
		eff  Efficiencies
	}{
		{"empty", nil, Efficiencies{Mash: 0.85, Ferment: 0.92, DistillationRecovery: 0.90}},
		{"zero mass", []Ingredient{{MassKg: 0, Extract: 0.78}}, Efficiencies{Mash: 0.85, Ferment: 0.92, DistillationRecovery: 0.90}},
		{"zero extract", []Ingredient{{MassKg: 100, Extract: 0}}, Efficiencies{Mash: 0.85, Ferment: 0.92, DistillationRecovery: 0.90}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if p := ProjectBatch(tc.in, tc.eff); p.TotalProjectedLAA != 0 {
				t.Errorf("expected 0, got %v", p.TotalProjectedLAA)
			}
		})
	}
}

func TestProjectWash(t *testing.T) {
	// 100 kg grain @ 0.78 extract, 0.85 mash, 0.92 ferment, +300 L water.
	//   wash volume ≈ 300 + 100 × 0.6 = 360 L
	//   ethanol mass = 100 × 0.78 × 0.85 × 0.511 × 0.92 = 31.166316 kg
	//   ethanol vol  = 31.166316 / 0.78934 = 39.4843 L
	//   ABV          = 39.4843 / 360 × 100 = ~10.97 %
	in := []Ingredient{{MassKg: 100, Extract: 0.78}}
	eff := Efficiencies{Mash: 0.85, Ferment: 0.92, DistillationRecovery: 0.90}
	w := ProjectWash(in, eff, 300)
	if got, want := w.VolumeL, 360.0; !floatNear(got, want, 0.01) {
		t.Errorf("VolumeL = %v, want %v", got, want)
	}
	if got, want := w.ABVPct, 10.97; !floatNear(got, want, 0.05) {
		t.Errorf("ABVPct = %v, want ~%v", got, want)
	}
}

func TestProjectWash_NoWater(t *testing.T) {
	in := []Ingredient{{MassKg: 100, Extract: 0.78}}
	eff := Efficiencies{Mash: 0.85, Ferment: 0.92, DistillationRecovery: 0.90}
	if w := ProjectWash(in, eff, 0); w.VolumeL != 0 || w.ABVPct != 0 {
		t.Errorf("expected zero projection without water, got %+v", w)
	}
}

func TestEfficienciesAreApplied(t *testing.T) {
	// 100% efficiencies should yield exactly the theoretical max.
	in := []Ingredient{{MassKg: 100, Extract: 1.0}}
	full := Efficiencies{Mash: 1, Ferment: 1, DistillationRecovery: 1}
	p := ProjectBatch(in, full)
	// 100 × 1 × 1 × 0.511 × 1 = 51.1 kg ethanol
	// / 0.78934 = 64.7378... L
	if got, want := p.TotalProjectedLAA, 64.7378; !floatNear(got, want, 0.001) {
		t.Errorf("theoretical max = %v, want ~%v", got, want)
	}

	// Half each efficiency should drop output to 0.5 × 0.5 × 0.5 = 12.5% of theoretical max.
	half := Efficiencies{Mash: 0.5, Ferment: 0.5, DistillationRecovery: 0.5}
	q := ProjectBatch(in, half)
	if got, want := q.TotalProjectedLAA, p.TotalProjectedLAA*0.125; !floatNear(got, want, 0.001) {
		t.Errorf("half-efficiency = %v, want ~%v", got, want)
	}
}

func floatNear(a, b, tol float64) bool { return math.Abs(a-b) <= tol }
