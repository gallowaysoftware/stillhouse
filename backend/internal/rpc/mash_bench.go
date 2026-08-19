package rpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/mashing"
)

// buildMashBench turns a mash's actual grain bill and recorded metrics into
// the guidance shown on the mash bench. Purely derived — it reads nothing
// and writes nothing, so it can be recomputed on every fetch and always
// reflects the latest reading.
func buildMashBench(
	ingredients []sqlcgen.ListMashIngredientsRow,
	metrics []sqlcgen.MashMetric,
) *stillhousev1.MashBench {
	bill := make([]mashing.GrainBillItem, 0, len(ingredients))
	for _, ing := range ingredients {
		kind := materialKindToProto(ing.MaterialKind)
		// Only fermentables carry starch to gelatinise; yeast, water and
		// packaging are not part of this conversation.
		if kind != stillhousev1.MaterialKind_MATERIAL_KIND_GRAIN &&
			kind != stillhousev1.MaterialKind_MATERIAL_KIND_MALT {
			continue
		}
		item := mashing.GrainBillItem{
			Name:   ing.MaterialName,
			Cereal: cerealToMashing(cerealToProto(ing.MaterialCereal)),
			MassKg: ing.QuantityUsed,
			Malted: kind == stillhousev1.MaterialKind_MATERIAL_KIND_MALT,
		}
		if ing.MaterialExtractPct.Valid {
			item.ExtractPct = ing.MaterialExtractPct.Float64
		}
		bill = append(bill, item)
	}
	if len(bill) == 0 {
		return nil
	}

	// Take the most recent reading of each kind — a mash gets gauged more
	// than once, and the latest is the one that describes it now.
	var r mashing.Readings
	for _, m := range metrics {
		v := m.Value
		switch mashMetricKindToProto(m.Kind) {
		case stillhousev1.MashMetricKind_MASH_METRIC_KIND_MASH_TEMP_C:
			r.MashTempC = latest(r.MashTempC, v)
		case stillhousev1.MashMetricKind_MASH_METRIC_KIND_MASH_PH:
			r.PH = latest(r.PH, v)
		case stillhousev1.MashMetricKind_MASH_METRIC_KIND_WATER_VOLUME_L:
			r.WaterVolumeL = latest(r.WaterVolumeL, v)
		case stillhousev1.MashMetricKind_MASH_METRIC_KIND_WASH_VOLUME_L:
			r.WashVolumeL = latest(r.WashVolumeL, v)
		case stillhousev1.MashMetricKind_MASH_METRIC_KIND_ORIGINAL_GRAVITY:
			r.OriginalGravity = latest(r.OriginalGravity, v)
		}
	}

	b := mashing.Assess(bill, r)
	out := &stillhousev1.MashBench{
		GelatinisationC:     tempRangeToProto(b.GelatinisationC),
		GelatinisationKnown: b.GelatinisationKnown,
		ConversionC:         tempRangeToProto(b.ConversionC),
		CerealCookRequired:  b.CerealCookRequired,
		TotalGrainKg:        round4(b.TotalGrainKg),
		Findings:            findingsToProto(b.Findings),
	}
	if b.ThicknessLPerKg != nil {
		out.ThicknessLPerKg = round4(*b.ThicknessLPerKg)
		out.ThicknessLPerKgSet = true
	}
	if b.Efficiency != nil {
		out.Efficiency = &stillhousev1.MashEfficiency{
			OriginalGravity:     b.Efficiency.OriginalGravity,
			Plato:               round2(b.Efficiency.Plato),
			WashVolumeL:         round2(b.Efficiency.WashVolumeL),
			WashVolumeEstimated: b.Efficiency.WashVolumeEstimated,
			ExtractMeasuredKg:   round2(b.Efficiency.ExtractMeasuredKg),
			ExtractAvailableKg:  round2(b.Efficiency.ExtractAvailableKg),
			Pct:                 round2(b.Efficiency.Pct),
		}
		out.EfficiencySet = true
	}
	return out
}

// latest keeps the most recently seen value. ListMashMetrics returns rows
// ordered oldest-first, so plain overwrite is "latest wins".
func latest(_ *float64, v float64) *float64 { return &v }

func tempRangeToProto(r mashing.TemperatureRange) *stillhousev1.TemperatureRange {
	return &stillhousev1.TemperatureRange{MinC: r.MinC, MaxC: r.MaxC}
}

func findingsToProto(fs []mashing.Finding) []*stillhousev1.MashFinding {
	out := make([]*stillhousev1.MashFinding, 0, len(fs))
	for _, f := range fs {
		out = append(out, &stillhousev1.MashFinding{
			Severity: findingSeverityToProto(f.Severity),
			Code:     f.Code,
			Title:    f.Title,
			Detail:   f.Detail,
		})
	}
	return out
}

func findingSeverityToProto(s mashing.Severity) stillhousev1.MashFindingSeverity {
	switch s {
	case mashing.SeverityProblem:
		return stillhousev1.MashFindingSeverity_MASH_FINDING_SEVERITY_PROBLEM
	case mashing.SeverityWarning:
		return stillhousev1.MashFindingSeverity_MASH_FINDING_SEVERITY_WARNING
	default:
		return stillhousev1.MashFindingSeverity_MASH_FINDING_SEVERITY_INFO
	}
}

// PlanStrike answers the question asked while the liquor is heating: how
// hot does the water need to be so the grain lands on the rest temperature?
func (s *MashService) PlanStrike(
	ctx context.Context,
	req *connect.Request[stillhousev1.PlanStrikeRequest],
) (*connect.Response[stillhousev1.PlanStrikeResponse], error) {
	if _, ok := CurrentUser(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	if in.GetThicknessLPerKg() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("thickness_l_per_kg must be > 0"))
	}
	plan, err := mashing.PlanStrike(
		in.GetTargetTempC(), in.GetGrainTempC(), in.GetThicknessLPerKg(), in.GetGrainKg())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&stillhousev1.PlanStrikeResponse{
		StrikeTempC:  round2(plan.StrikeTempC),
		WaterVolumeL: round2(plan.WaterVolumeL),
		Findings:     findingsToProto(plan.Findings),
	}), nil
}
