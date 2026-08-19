package mashing

import "fmt"

// GrainDisplacementLPerKg is how much volume a kilogram of mashed grain
// occupies. Used only to estimate wash volume when it wasn't measured;
// mirrors the constant in internal/distilling so a projection and a bench
// reading of the same mash don't disagree.
const GrainDisplacementLPerKg = 0.6

// Efficiency is conversion measured at the tun, from the original gravity,
// rather than inferred weeks later from what the still gave back.
type Efficiency struct {
	// OriginalGravity as recorded, e.g. 1.055.
	OriginalGravity float64
	// Plato is the gravity expressed as extract mass percent.
	Plato float64
	// WashVolumeL is the volume the extract is dissolved in.
	WashVolumeL float64
	// WashVolumeEstimated is true when WashVolumeL came from water plus
	// grain displacement rather than a measurement.
	WashVolumeEstimated bool
	// ExtractMeasuredKg is the extract actually in solution.
	ExtractMeasuredKg float64
	// ExtractAvailableKg is what the grain bill could have given up:
	// Σ(mass × extract fraction).
	ExtractAvailableKg float64
	// Pct is measured ÷ available, as a percentage.
	Pct float64
}

// PlatoFromSG converts specific gravity to degrees Plato (extract as a
// percentage of wort mass).
//
// Uses the standard ASBC polynomial approximation P = 259 − 259/SG, which
// tracks the published tables closely across the gravity range a mash
// occupies (1.000–1.100). It is an approximation of a table, not a
// definition — but unlike an alcoholic strength, mash extract carries no
// duty consequence, so an approximation is appropriate here.
func PlatoFromSG(sg float64) float64 {
	if sg <= 0 {
		return 0
	}
	return 259 - 259/sg
}

// assessEfficiency computes conversion efficiency from the recorded
// original gravity, and reports when it falls short of what the bill
// should have given.
func (b *Bench) assessEfficiency(bill []GrainBillItem, r Readings) {
	if r.OriginalGravity == nil || *r.OriginalGravity <= 1.0 {
		return
	}
	available := 0.0
	for _, it := range bill {
		if it.MassKg > 0 && it.ExtractPct > 0 {
			available += it.MassKg * it.ExtractPct
		}
	}
	if available <= 0 {
		b.Findings = append(b.Findings, Finding{
			Severity: SeverityInfo,
			Code:     "no_extract_data",
			Title:    "Can't compute conversion efficiency",
			Detail:   "None of the fermentables on this mash carry an extract percentage. Set it on the material to unlock this.",
		})
		return
	}

	volume, estimated := 0.0, false
	switch {
	case r.WashVolumeL != nil && *r.WashVolumeL > 0:
		volume = *r.WashVolumeL
	case r.WaterVolumeL != nil && *r.WaterVolumeL > 0:
		volume = *r.WaterVolumeL + b.TotalGrainKg*GrainDisplacementLPerKg
		estimated = true
	default:
		return
	}

	sg := *r.OriginalGravity
	plato := PlatoFromSG(sg)
	// Wort mass ≈ volume × SG (SG is relative to water at ~1 kg/L), and
	// Plato is extract as a mass percentage of that.
	measured := volume * sg * plato / 100

	eff := &Efficiency{
		OriginalGravity:     sg,
		Plato:               plato,
		WashVolumeL:         volume,
		WashVolumeEstimated: estimated,
		ExtractMeasuredKg:   measured,
		ExtractAvailableKg:  available,
		Pct:                 measured / available * 100,
	}
	b.Efficiency = eff

	if estimated {
		b.Findings = append(b.Findings, Finding{
			Severity: SeverityInfo,
			Code:     "wash_volume_estimated",
			Title:    fmt.Sprintf("Wash volume estimated at %.0f L", volume),
			Detail: fmt.Sprintf("Water volume plus %.1f L/kg grain displacement. Record a wash volume "+
				"reading to base efficiency on a measurement instead.", GrainDisplacementLPerKg),
		})
	}

	switch {
	case eff.Pct > 100:
		b.Findings = append(b.Findings, Finding{
			Severity: SeverityWarning,
			Code:     "efficiency_impossible",
			Title:    fmt.Sprintf("Conversion efficiency computes to %.0f %%", eff.Pct),
			Detail: "Above 100 % means the inputs disagree rather than that the mash over-performed — " +
				"check the gravity reading, the wash volume, and the extract percentages on the materials.",
		})
	case eff.Pct < 60:
		b.Findings = append(b.Findings, Finding{
			Severity: SeverityProblem,
			Code:     "efficiency_low",
			Title:    fmt.Sprintf("Conversion efficiency %.0f %%", eff.Pct),
			Detail: "Well below what a healthy mash returns. Look at the conversion rest temperature, " +
				"the pH, and — if the bill has maize or rice in it — whether the cereal cook actually " +
				"gelatinised the starch before the malt went in.",
		})
	case eff.Pct < 75:
		b.Findings = append(b.Findings, Finding{
			Severity: SeverityWarning,
			Code:     "efficiency_modest",
			Title:    fmt.Sprintf("Conversion efficiency %.0f %%", eff.Pct),
			Detail:   "There is extract left in the tun. Small-scale mashes commonly sit at 75–90 %.",
		})
	}
}
