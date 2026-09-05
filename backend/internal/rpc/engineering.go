package rpc

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/stillsim"
)

// EngineeringService is the flow chart's models over the wire. A plan is a
// calculation: it reads nothing and writes nothing, so it needs no tenant
// transaction, and a refusal is the model's own sentence.
type EngineeringService struct {
	logger *slog.Logger
}

func NewEngineeringService(logger *slog.Logger) *EngineeringService {
	return &EngineeringService{logger: logger}
}

func (s *EngineeringService) ListStillPresets(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListStillPresetsRequest],
) (*connect.Response[stillhousev1.ListStillPresetsResponse], error) {
	if _, ok := CurrentUser(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	out := &stillhousev1.ListStillPresetsResponse{}
	for _, p := range stillsim.Presets() {
		out.Presets = append(out.Presets, stillToProto(p))
	}
	return connect.NewResponse(out), nil
}

func (s *EngineeringService) PlanRun(
	ctx context.Context,
	req *connect.Request[stillhousev1.PlanRunRequest],
) (*connect.Response[stillhousev1.PlanRunResponse], error) {
	if _, ok := CurrentUser(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	if in.GetStill() == nil || in.GetCharge() == nil || in.GetSettings() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a plan needs a still, a charge and settings"))
	}
	still := stillFromProto(in.GetStill())
	charge := stillsim.Charge{
		VolumeL: in.GetCharge().GetVolumeL(),
		ABVPct:  in.GetCharge().GetAbvPct(),
		TempC:   in.GetCharge().GetTemperatureC(),
	}
	st := in.GetSettings()
	settings := stillsim.Settings{
		Firing:            st.GetFiringPct() / 100,
		HeatUpFiring:      st.GetHeatUpFiringPct() / 100,
		RefluxRatio:       st.GetRefluxRatio(),
		CutStrengths:      st.GetCutStrengthsPct(),
		StopDistillateABV: st.GetStopDistillateAbvPct(),
		AmbientC:          st.GetAmbientC(),
	}
	if settings.AmbientC == 0 {
		settings.AmbientC = 20
	}
	plan, err := stillsim.PlanRun(still, charge, settings)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&stillhousev1.PlanRunResponse{Plan: planToProto(plan)}), nil
}

func stillFromProto(p *stillhousev1.StillSpec) stillsim.Still {
	s := stillsim.Still{
		Name:         p.GetName(),
		PotVolumeL:   p.GetPotVolumeL(),
		Heating:      stillsim.Heating{Kind: stillsim.HeatingElectric, RatedW: p.GetRatedW(), Efficiency: p.GetHeatingEfficiency()},
		BoilBall:     p.GetBoilBall(),
		Plates:       int(p.GetPlates()),
		Dephlegmator: p.GetDephlegmator(),
	}
	if p.GetHeatingKind() == stillhousev1.StillHeatingKind_STILL_HEATING_KIND_STEAM {
		s.Heating.Kind = stillsim.HeatingSteam
	}
	switch p.GetGeometry() {
	case stillhousev1.StillGeometry_STILL_GEOMETRY_SQUAT:
		s.Geometry = stillsim.GeometrySquat
	case stillhousev1.StillGeometry_STILL_GEOMETRY_TALL:
		s.Geometry = stillsim.GeometryTall
	default:
		s.Geometry = stillsim.GeometryStandard
	}
	if p.GetHeatLossWSet() {
		s.HeatLossW = p.GetHeatLossW()
	}
	return s
}

func stillToProto(s stillsim.Still) *stillhousev1.StillSpec {
	out := &stillhousev1.StillSpec{
		Name:              s.Name,
		PotVolumeL:        s.PotVolumeL,
		HeatingKind:       stillhousev1.StillHeatingKind_STILL_HEATING_KIND_ELECTRIC,
		RatedW:            s.Heating.RatedW,
		HeatingEfficiency: s.Heating.Efficiency,
		Geometry:          stillhousev1.StillGeometry_STILL_GEOMETRY_STANDARD,
		BoilBall:          s.BoilBall,
		Plates:            int32(s.Plates),
		Dephlegmator:      s.Dephlegmator,
	}
	if s.Heating.Kind == stillsim.HeatingSteam {
		out.HeatingKind = stillhousev1.StillHeatingKind_STILL_HEATING_KIND_STEAM
	}
	switch s.Geometry {
	case stillsim.GeometrySquat:
		out.Geometry = stillhousev1.StillGeometry_STILL_GEOMETRY_SQUAT
	case stillsim.GeometryTall:
		out.Geometry = stillhousev1.StillGeometry_STILL_GEOMETRY_TALL
	}
	if s.HeatLossW > 0 {
		out.HeatLossW, out.HeatLossWSet = s.HeatLossW, true
	}
	return out
}

func planToProto(p stillsim.Plan) *stillhousev1.RunPlan {
	out := &stillhousev1.RunPlan{
		Still:            p.Still,
		ChargeLaa:        p.ChargeLAA,
		Stages:           p.Stages,
		Basis:            p.Basis,
		TimeToFirstDropS: int64(p.TimeToFirstDrop / time.Second),
		RunTimeS:         int64(p.RunTime / time.Second),
		CollectedLaa:     p.CollectedLAA,
		ShareOfCharge:    p.ShareOfCharge,
		EnergyKwh:        p.EnergyKWh,
		Directions:       p.Directions,
	}
	for _, pt := range p.Points {
		out.Points = append(out.Points, &stillhousev1.PlanPoint{
			AtS:              int64(pt.At / time.Second),
			DistillateAbvPct: pt.DistillateABV,
			PotAbvPct:        pt.PotABV,
			PotTemperatureC:  pt.PotTempC,
			CollectedLaa:     pt.CollectedLAA,
			CollectedVolumeL: pt.CollectedVolumeL,
			EnergyKwh:        pt.EnergyKWh,
		})
	}
	for _, c := range p.Cuts {
		out.Cuts = append(out.Cuts, &stillhousev1.PlanCut{
			AbvPct:        c.ABV,
			AtS:           int64(c.At / time.Second),
			CollectedLaa:  c.CollectedLAA,
			ShareOfCharge: c.ShareOfCharge,
		})
	}
	return out
}
