package rpc

import (
	"github.com/jackc/pgx/v5/pgtype"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/maturation"
)

// warehouseClimate is the baseline evaporation regime for a Canadian
// distillery: cool and humid, so casks lose about 2–3 % of their volume a
// year and lose strength as they go.
//
// Hardcoded rather than configured because every tenant Stillhouse serves
// holds a CRA spirits licence, and there is no part of Canada that
// matures like Kentucky. A cask high in the stack still gets treated as
// hot and dry — that comes from its recorded level, not from this.
const warehouseClimate = maturation.ClimateCoolHumid

// buildMaturation assesses a cask's angel's share against the fill it is
// living off. Returns nil when there is no fill to compare with, which is
// the normal state for a cask that has never been filled.
func buildMaturation(
	fillVolumeL, fillAbvPct, fillLAA pgtype.Float8,
	currentVolumeL, currentAbvPct, currentLAA float64,
	daysAged int32,
	levelPosition string,
) *stillhousev1.MaturationAssessment {
	if !fillVolumeL.Valid || fillVolumeL.Float64 <= 0 {
		return nil
	}
	a := maturation.Assess(
		maturation.Snapshot{
			VolumeL:     fillVolumeL.Float64,
			StrengthPct: floatOr(fillAbvPct),
			LAA:         floatOr(fillLAA),
		},
		maturation.Snapshot{
			VolumeL:     currentVolumeL,
			StrengthPct: currentAbvPct,
			LAA:         currentLAA,
		},
		float64(daysAged),
		warehouseClimate,
		levelPosition,
	)

	out := &stillhousev1.MaturationAssessment{
		Measurable:           a.Measurable,
		WhyNot:               a.WhyNot,
		AnnualVolumeLossPct:  round2(a.AnnualVolumeLossPct),
		AnnualLaaLossPct:     round2(a.AnnualLAALossPct),
		StrengthDriftPerYear: round2(a.StrengthDriftPerYear),
		ExpectedMinPct:       a.ExpectedMinPct,
		ExpectedMaxPct:       a.ExpectedMaxPct,
		ExpectedDriftSign:    a.ExpectedDriftSign,
		ClimateFromLevel:     a.ClimateFromLevel,
		HotDry:               a.Climate == maturation.ClimateHotDry,
	}
	for _, f := range a.Findings {
		out.Findings = append(out.Findings, &stillhousev1.MaturationFinding{
			Severity: maturationSeverityToProto(f.Severity),
			Code:     f.Code,
			Title:    f.Title,
			Detail:   f.Detail,
		})
	}
	return out
}

func maturationSeverityToProto(s maturation.Severity) stillhousev1.MaturationFindingSeverity {
	switch s {
	case maturation.SeverityProblem:
		return stillhousev1.MaturationFindingSeverity_MATURATION_FINDING_SEVERITY_PROBLEM
	case maturation.SeverityWarning:
		return stillhousev1.MaturationFindingSeverity_MATURATION_FINDING_SEVERITY_WARNING
	default:
		return stillhousev1.MaturationFindingSeverity_MATURATION_FINDING_SEVERITY_INFO
	}
}

func floatOr(f pgtype.Float8) float64 {
	if f.Valid {
		return f.Float64
	}
	return 0
}
