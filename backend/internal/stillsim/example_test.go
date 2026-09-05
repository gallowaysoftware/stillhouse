package stillsim

import (
	"fmt"
	"testing"
	"time"
)

// TestPrintTheSitePlans prints the plans for the stills on site; run with -v
// to read them. Not an assertion — a way to look.
func TestPrintTheSitePlans(t *testing.T) {
	for _, tc := range []struct {
		still  Still
		charge Charge
		s      Settings
	}{
		{Presets()[0], Charge{VolumeL: 150, ABVPct: 25, TempC: 18}, Settings{Firing: 0.6, CutStrengths: []float64{75, 65, 48}, StopDistillateABV: 10, AmbientC: 20}},
		{Presets()[1], Charge{VolumeL: 150, ABVPct: 25, TempC: 18}, Settings{Firing: 0.6, RefluxRatio: 3, CutStrengths: []float64{90, 85, 80}, StopDistillateABV: 10, AmbientC: 20}},
		{Presets()[2], Charge{VolumeL: 25, ABVPct: 30, TempC: 18}, Settings{Firing: 0.7, CutStrengths: []float64{75, 65, 48}, StopDistillateABV: 10, AmbientC: 20}},
	} {
		plan, err := PlanRun(tc.still, tc.charge, tc.s)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s — %.0f L at %.0f %% (%.1f LAA), heat-up full then %.0f %%, %.2f stages: first drop at %v, run %v, %.1f kWh, collected %.1f LAA (%.0f %%)",
			plan.Still, tc.charge.VolumeL, tc.charge.ABVPct, plan.ChargeLAA, tc.s.Firing*100, plan.Stages,
			plan.TimeToFirstDrop.Round(time.Minute), plan.RunTime.Round(time.Minute), plan.EnergyKWh, plan.CollectedLAA, plan.ShareOfCharge*100)
		first := plan.Points[indexOfFirstDrop(plan)]
		t.Logf("   first distillate %.1f %%", first.DistillateABV)
		for _, c := range plan.Cuts {
			t.Logf("   passes %.0f %% at %v with %.1f LAA collected (%.0f %% of the charge)", c.ABV, c.At.Round(time.Minute), c.CollectedLAA, c.ShareOfCharge*100)
		}
		t.Logf("   %s", plan.Basis)
		_ = fmt.Sprintf
	}
}
