package stillsim

import (
	"math"
	"testing"
	"time"
)

// The curriculum's anchors on the equilibrium: an 8 % wash boils near 94 °C
// and throws 45–50 %; 28 % low wines boil near 86 °C and throw about 75 %;
// nothing passes the azeotrope (CIBD Module 2 — distillation theory, batch
// distillation).
func TestEquilibriumAnchors(t *testing.T) {
	for _, tc := range []struct {
		abv, boilMin, boilMax, vapMin, vapMax float64
	}{
		{8, 94.0, 94.6, 45, 50},
		{28, 86.0, 86.5, 74, 76},
		{97.2, 78.1, 78.2, 97.2, 97.2},
	} {
		if b := bubblePointC(tc.abv); b < tc.boilMin || b > tc.boilMax {
			t.Errorf("%v %% boils at %.2f, want %v–%v", tc.abv, b, tc.boilMin, tc.boilMax)
		}
		if v := vapourABV(tc.abv); v < tc.vapMin || v > tc.vapMax {
			t.Errorf("%v %% throws %.2f, want %v–%v", tc.abv, v, tc.vapMin, tc.vapMax)
		}
	}
	for abv := 0.0; abv <= 100; abv += 0.5 {
		v := vapourABV(abv)
		if abv <= 97.2 && (v < abv-0.01 || v > 97.21) {
			t.Fatalf("below the azeotrope %v %% threw %v", abv, v)
		}
		if abv > 97.2 && (v < 97.19 || v > abv+0.01) {
			t.Fatalf("above the azeotrope %v %% threw %v", abv, v)
		}
	}
}

// Strength and volume round-trip through mass: 40 % is 33.3 % by mass, and
// 1,000 L at 63 % holds 630 LAA (the curriculum's reduction example, which
// stillhouse's plan_reduction agrees with).
func TestMassAndVolume(t *testing.T) {
	if w := massFraction(40); math.Abs(w-0.333) > 0.002 {
		t.Errorf("40 %% by mass = %.4f, want 0.333", w)
	}
	e, w := massesOf(1000, 63)
	if laa := e / EthanolDensityKgPerL; math.Abs(laa-630) < 1 == false {
		t.Errorf("1,000 L at 63 %% holds %.1f LAA, want 630", laa)
	}
	if v := volumeL(e, w); math.Abs(v-1000) > 1 {
		t.Errorf("round trip volume %.1f", v)
	}
	if a := abvOf(e, w); math.Abs(a-63) > 0.1 {
		t.Errorf("round trip strength %.2f", a)
	}
}

// The curriculum's worked spirit run: a plain pot charged at 29 % passes 48 %
// distillate near 80 % of the charge's alcohol collected and ends near 98 %
// at 8 % (CIBD Module 2 — batch distillation). Held here on a 3,200 L charge
// under enough heat for a run of some hours.
func TestTheCurriculumsSpiritRun(t *testing.T) {
	still := Still{
		Name:       "curriculum spirit still",
		PotVolumeL: 4000,
		Heating:    Heating{Kind: HeatingSteam, RatedW: 200_000, Efficiency: 1},
		Geometry:   GeometrySquat,
	}
	plan, err := PlanRun(still, Charge{VolumeL: 3200, ABVPct: 29, TempC: 20}, Settings{
		Firing: 1, CutStrengths: []float64{75, 48}, StopDistillateABV: 8, AmbientC: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Stages != StagesSquatPot {
		t.Errorf("stages %v", plan.Stages)
	}
	if len(plan.Cuts) != 2 {
		t.Fatalf("cuts %+v", plan.Cuts)
	}
	first := plan.Points[indexOfFirstDrop(plan)]
	if first.DistillateABV < 72 || first.DistillateABV > 78 {
		t.Errorf("first distillate %.1f %%, want 74–78 (the curriculum: 78 %%)", first.DistillateABV)
	}
	at48 := plan.Cuts[1]
	if at48.ShareOfCharge < 0.74 || at48.ShareOfCharge > 0.86 {
		t.Errorf("48 %% passed with %.0f %% of the alcohol collected, want near 80", at48.ShareOfCharge*100)
	}
	if plan.ShareOfCharge < 0.95 {
		t.Errorf("the run ended with %.0f %% collected, want near 98", plan.ShareOfCharge*100)
	}
	if plan.RunTime < 3*time.Hour || plan.RunTime > 9*time.Hour {
		t.Errorf("run took %v", plan.RunTime)
	}
	if plan.Basis == "" || plan.Basis[:9] != "estimated" {
		t.Errorf("basis %q", plan.Basis)
	}
}

func indexOfFirstDrop(p Plan) int {
	for i, pt := range p.Points {
		if pt.DistillateABV > 0 {
			return i
		}
	}
	return len(p.Points) - 1
}

// The 200 L still on site: a 150 L charge of 25 % low wines, heated up at
// full and run at 60 %, boils within the hour and a half and runs a working
// afternoon on a few dollars of electricity.
func TestThe200LStillOnSite(t *testing.T) {
	still := Presets()[0]
	plan, err := PlanRun(still, Charge{VolumeL: 150, ABVPct: 25, TempC: 18}, Settings{
		Firing: 0.6, CutStrengths: []float64{70, 60, 48}, StopDistillateABV: 10, AmbientC: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.TimeToFirstDrop < 45*time.Minute || plan.TimeToFirstDrop > 90*time.Minute {
		t.Errorf("first drop after %v", plan.TimeToFirstDrop)
	}
	if plan.RunTime < 4*time.Hour || plan.RunTime > 8*time.Hour {
		t.Errorf("run took %v", plan.RunTime)
	}
	if plan.EnergyKWh < 20 || plan.EnergyKWh > 50 {
		t.Errorf("energy %.1f kWh", plan.EnergyKWh)
	}
	// The alcohol accounted for: the charge's 37.5 LAA, nearly all of it collected.
	if plan.ShareOfCharge < 0.95 {
		t.Errorf("collected %.0f %% of the charge", plan.ShareOfCharge*100)
	}
	if math.Abs(plan.ChargeLAA-37.5) > 0.5 {
		t.Errorf("charge LAA %.2f", plan.ChargeLAA)
	}
}

// Plates and a dephlegmator make a stronger, tighter spirit than the pot
// alone; the same still in pot mode is the weaker of the two.
func TestPlatesAndDephlegmatorStrengthen(t *testing.T) {
	pot, column := Presets()[0], Presets()[1]
	charge := Charge{VolumeL: 150, ABVPct: 25, TempC: 18}
	base := Settings{Firing: 0.6, CutStrengths: []float64{80}, StopDistillateABV: 10, AmbientC: 20}
	potPlan, err := PlanRun(pot, charge, base)
	if err != nil {
		t.Fatal(err)
	}
	withReflux := base
	withReflux.RefluxRatio = 3
	colPlan, err := PlanRun(column, charge, withReflux)
	if err != nil {
		t.Fatal(err)
	}
	if colPlan.Stages <= potPlan.Stages+2 {
		t.Errorf("column stages %.2f vs pot %.2f", colPlan.Stages, potPlan.Stages)
	}
	potFirst := potPlan.Points[indexOfFirstDrop(potPlan)].DistillateABV
	colFirst := colPlan.Points[indexOfFirstDrop(colPlan)].DistillateABV
	if colFirst < potFirst+10 {
		t.Errorf("column first distillate %.1f vs pot %.1f", colFirst, potFirst)
	}
	if colFirst > 96 {
		t.Errorf("column first distillate %.1f passes what four plates can do", colFirst)
	}
	// Every part sent back down was boiled, so the column run takes and
	// burns more than the pot's at the same firing — though not the four
	// times a reflux of three suggests, because a 94 % stream carries almost
	// none of the water the pot run spends its heat on.
	if colPlan.RunTime < potPlan.RunTime*12/10 || colPlan.EnergyKWh < potPlan.EnergyKWh*1.2 {
		t.Errorf("column run %v / %.0f kWh vs pot %v / %.0f kWh", colPlan.RunTime, colPlan.EnergyKWh, potPlan.RunTime, potPlan.EnergyKWh)
	}
}

// Calibration overrides the specification, and the plan says so.
func TestCalibrationSpeaks(t *testing.T) {
	still := Presets()[2]
	still.Calibration = &Calibration{Stages: 1.1, HeatLossW: 150, Efficiency: 0.92, Runs: 3, On: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), RMSStrengthPts: 1.8}
	plan, err := PlanRun(still, Charge{VolumeL: 25, ABVPct: 30}, Settings{Firing: 0.8, StopDistillateABV: 10, AmbientC: 20})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Stages != 1.1 {
		t.Errorf("stages %v", plan.Stages)
	}
	if plan.Basis != "calibrated on 3 run(s), last 2026-10-02, fit within 1.8 points" {
		t.Errorf("basis %q", plan.Basis)
	}
}

// Refusals name what would work.
func TestRefusals(t *testing.T) {
	still := Presets()[2]
	if _, err := PlanRun(still, Charge{VolumeL: 40, ABVPct: 30}, Settings{Firing: 1}); err == nil {
		t.Error("a charge past the pot must refuse")
	}
	if _, err := PlanRun(still, Charge{VolumeL: 20, ABVPct: 30}, Settings{Firing: 0}); err == nil {
		t.Error("no firing must refuse")
	}
	if _, err := PlanRun(still, Charge{VolumeL: 20, ABVPct: 30}, Settings{Firing: 1, CutStrengths: []float64{48, 70}}); err == nil {
		t.Error("ascending cuts must refuse")
	}
	cold := still
	cold.HeatLossW = 5000
	if _, err := PlanRun(cold, Charge{VolumeL: 20, ABVPct: 30}, Settings{Firing: 0.1}); err == nil {
		t.Error("a still that cannot reach the boil must say so")
	}
}

// A plan is deterministic and the alcohol is conserved: collected plus what
// is left equals the charge.
func TestAlcoholIsConserved(t *testing.T) {
	still := Presets()[0]
	charge := Charge{VolumeL: 150, ABVPct: 25}
	settings := Settings{Firing: 0.5, StopDistillateABV: 10, AmbientC: 20}
	a, err := PlanRun(still, charge, settings)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := PlanRun(still, charge, settings)
	if a.CollectedLAA != b.CollectedLAA || a.RunTime != b.RunTime {
		t.Error("two plans of one run differ")
	}
	if a.ShareOfCharge > 1.0001 {
		t.Errorf("collected %.4f of the charge", a.ShareOfCharge)
	}
}
