package alcoholometry

import (
	"math"
	"testing"
)

// TestBlendConservesAlcohol — vatting neither creates nor destroys
// absolute alcohol, whatever happens to the volume.
func TestBlendConservesAlcohol(t *testing.T) {
	sources := []BlendSource{
		{Label: "Cask 12", VolumeL: 200, StrengthPct: 62},
		{Label: "Cask 19", VolumeL: 180, StrengthPct: 58},
		{Label: "Cask 04", VolumeL: 95, StrengthPct: 64.5},
	}
	p, err := PlanBlend(sources, 0)
	if err != nil {
		t.Fatalf("PlanBlend: %v", err)
	}
	want := 200*0.62 + 180*0.58 + 95*0.645
	if math.Abs(p.TotalLAA-want) > 1e-9 {
		t.Errorf("total LAA = %.6f, want %.6f", p.TotalLAA, want)
	}
	// And the blend itself must hold exactly that.
	got := p.BlendVolumeL * p.BlendStrengthPct / 100
	if math.Abs(got-want) > 1e-4 {
		t.Errorf("blend holds %.6f L LAA, want %.6f", got, want)
	}
}

// TestBlendContractsRatherThanAdding is the point of doing this through
// the tables: the parcels do not simply add up.
func TestBlendContractsRatherThanAdding(t *testing.T) {
	p, err := PlanBlend([]BlendSource{
		{VolumeL: 500, StrengthPct: 70},
		{VolumeL: 500, StrengthPct: 40},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.NaiveVolumeL != 1000 {
		t.Errorf("naive volume = %v, want 1000", p.NaiveVolumeL)
	}
	if !(p.BlendVolumeL < p.NaiveVolumeL) {
		t.Errorf("blend volume %.4f should be under the naive %.4f — ethanol and water contract",
			p.BlendVolumeL, p.NaiveVolumeL)
	}
	if p.ContractionL <= 0 {
		t.Errorf("contraction = %.4f, want positive", p.ContractionL)
	}
	// It's small, but it is not nothing.
	pct := p.ContractionL / p.NaiveVolumeL * 100
	if pct > 3 {
		t.Errorf("contraction of %.2f %% is implausibly large", pct)
	}
	t.Logf("500 L @ 70 %% + 500 L @ 40 %% = %.2f L @ %.3f %% (not 1000 L; %.2f L lost to contraction)",
		p.BlendVolumeL, p.BlendStrengthPct, p.ContractionL)
}

// TestBlendStrengthIsNotTheVolumeWeightedMean — the naive answer, and why
// it's wrong.
func TestBlendStrengthIsNotTheVolumeWeightedMean(t *testing.T) {
	p, err := PlanBlend([]BlendSource{
		{VolumeL: 500, StrengthPct: 70},
		{VolumeL: 500, StrengthPct: 40},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	naive := (500*70 + 500*40) / 1000.0 // 55 %
	if math.Abs(p.BlendStrengthPct-naive) < 1e-6 {
		t.Error("blend strength matched the volume-weighted mean exactly — contraction was ignored")
	}
	// Contraction concentrates the alcohol into less volume, so the true
	// strength sits slightly above the naive average.
	if !(p.BlendStrengthPct > naive) {
		t.Errorf("blend strength %.4f should be above the naive mean %.4f", p.BlendStrengthPct, naive)
	}
}

func TestBlendThenReduce(t *testing.T) {
	p, err := PlanBlend([]BlendSource{
		{VolumeL: 200, StrengthPct: 62},
		{VolumeL: 180, StrengthPct: 58},
	}, 46)
	if err != nil {
		t.Fatalf("PlanBlend: %v", err)
	}
	if !p.ReductionSet || p.Reduction == nil {
		t.Fatal("a target strength should produce a reduction plan")
	}
	// Reducing conserves the alcohol the blend held.
	if math.Abs(p.Reduction.LAA-p.TotalLAA) > 1e-6 {
		t.Errorf("reduction LAA %.6f != blend LAA %.6f", p.Reduction.LAA, p.TotalLAA)
	}
	// And lands on the target.
	got := p.Reduction.FinalVolumeL * 46 / 100
	if math.Abs(got-p.TotalLAA) > 1e-6 {
		t.Errorf("final parcel holds %.6f L LAA at 46 %%, want %.6f", got, p.TotalLAA)
	}
	if p.Reduction.WaterToAddKg <= 0 {
		t.Error("bringing 60 %-ish spirit to 46 % needs water")
	}
}

func TestBlendRefusesToStrengthenWithWater(t *testing.T) {
	_, err := PlanBlend([]BlendSource{
		{VolumeL: 100, StrengthPct: 45},
		{VolumeL: 100, StrengthPct: 43},
	}, 60)
	if err == nil {
		t.Error("a target above the blend's own strength must be refused, not solved")
	}
}

func TestBlendValidatesItsInputs(t *testing.T) {
	if _, err := PlanBlend([]BlendSource{{VolumeL: 100, StrengthPct: 60}}, 0); err == nil {
		t.Error("one source is not a blend")
	}
	if _, err := PlanBlend([]BlendSource{
		{VolumeL: 0, StrengthPct: 60}, {VolumeL: 100, StrengthPct: 50},
	}, 0); err == nil {
		t.Error("a zero-volume source must be refused")
	}
	if _, err := PlanBlend([]BlendSource{
		{VolumeL: 100, StrengthPct: 0}, {VolumeL: 100, StrengthPct: 50},
	}, 0); err == nil {
		t.Error("a zero-strength source must be refused")
	}
	if _, err := PlanBlend([]BlendSource{
		{VolumeL: 100, StrengthPct: 105}, {VolumeL: 100, StrengthPct: 50},
	}, 0); err == nil {
		t.Error("a strength over 100 % must be refused")
	}
}

// TestBlendOfIdenticalParcelsIsTheSameSpirit — a sanity anchor: vatting a
// spirit with itself changes nothing but the quantity.
func TestBlendOfIdenticalParcelsIsTheSameSpirit(t *testing.T) {
	p, err := PlanBlend([]BlendSource{
		{VolumeL: 150, StrengthPct: 63},
		{VolumeL: 150, StrengthPct: 63},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(p.BlendStrengthPct-63) > 1e-3 {
		t.Errorf("blend strength = %.4f, want 63", p.BlendStrengthPct)
	}
	if math.Abs(p.BlendVolumeL-300) > 1e-3 {
		t.Errorf("blend volume = %.4f, want 300 — identical parcels don't contract", p.BlendVolumeL)
	}
}
