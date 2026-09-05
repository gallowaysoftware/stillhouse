package rpc

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The planner over the wire: presets come back as specs, a plan comes back
// with its envelope and its basis, and a refusal is the model's own sentence.
// No database: a plan reads nothing.
func TestPlanRunOverTheWire(t *testing.T) {
	svc := NewEngineeringService(testLogger())
	ctx := WithUser(context.Background(), sqlcgen.User{Role: sqlcgen.UserRoleViewer})

	presets, err := svc.ListStillPresets(ctx, connect.NewRequest(&stillhousev1.ListStillPresetsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(presets.Msg.GetPresets()); n != 3 {
		t.Fatalf("want the three stills on site, got %d", n)
	}
	pot := presets.Msg.GetPresets()[0]
	if pot.GetPotVolumeL() != 200 || pot.GetRatedW() != 11_000 || pot.GetHeatingKind() != stillhousev1.StillHeatingKind_STILL_HEATING_KIND_ELECTRIC {
		t.Errorf("the first preset is not the 200 L electric pot: %+v", pot)
	}

	resp, err := svc.PlanRun(ctx, connect.NewRequest(&stillhousev1.PlanRunRequest{
		Still:    pot,
		Charge:   &stillhousev1.RunCharge{VolumeL: 150, AbvPct: 25, TemperatureC: 18},
		Settings: &stillhousev1.RunSettings{FiringPct: 60, CutStrengthsPct: []float64{75, 65, 48}, StopDistillateAbvPct: 10},
	}))
	if err != nil {
		t.Fatal(err)
	}
	p := resp.Msg.GetPlan()
	if len(p.GetCuts()) != 3 || p.GetCuts()[2].GetAbvPct() != 48 {
		t.Errorf("cuts %+v", p.GetCuts())
	}
	if p.GetCuts()[2].GetShareOfCharge() < 0.74 || p.GetCuts()[2].GetShareOfCharge() > 0.9 {
		t.Errorf("48 %% passed at %.0f %% of the charge", p.GetCuts()[2].GetShareOfCharge()*100)
	}
	if len(p.GetPoints()) < 100 || p.GetRunTimeS() < 3*3600 || p.GetEnergyKwh() <= 0 {
		t.Errorf("plan too thin: %d points, %d s, %.1f kWh", len(p.GetPoints()), p.GetRunTimeS(), p.GetEnergyKwh())
	}
	if p.GetBasis() == "" || len(p.GetDirections()) == 0 {
		t.Errorf("a plan says where it came from and what it does not predict")
	}

	_, err = svc.PlanRun(ctx, connect.NewRequest(&stillhousev1.PlanRunRequest{
		Still:    pot,
		Charge:   &stillhousev1.RunCharge{VolumeL: 250, AbvPct: 25},
		Settings: &stillhousev1.RunSettings{FiringPct: 60},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument || err == nil {
		t.Fatalf("a charge past the pot must refuse as invalid_argument, got %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "200 L") {
		t.Errorf("the refusal names the pot: %q", got)
	}
}
