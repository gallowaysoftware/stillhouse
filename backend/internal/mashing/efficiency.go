package mashing

import (
	"fmt"
	"strings"

	"github.com/gallowaysoftware/stillhouse/backend/internal/units"
)

// Efficiency is conversion measured at the tun, from the original gravity,
// rather than inferred weeks later from what the still gave back.
type Efficiency struct {
	// OriginalGravity as recorded, e.g. 1.055.
	OriginalGravity float64
	// Plato is the gravity expressed as extract mass percent.
	Plato float64
	// WashVolumeL is the volume the extract is dissolved in, as measured.
	WashVolumeL float64
	// ExtractMeasuredKg is the extract actually in solution.
	ExtractMeasuredKg float64
	// ExtractAvailableKg is what the grain bill could have given up:
	// Σ(mass × extract fraction).
	ExtractAvailableKg float64
	// Percent is measured ÷ available. Typed, because the field it is
	// most often confused with — the recipe's mash_efficiency_pct — is a
	// *fraction* of the same quantity, and the two sit three letters
	// apart in the same product. See internal/units.
	Percent units.Percent
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
	var unknown []string
	for _, it := range bill {
		switch {
		case it.MassKg <= 0:
		case it.Extract > 0:
			available += it.MassKg * it.Extract.Float()
		default:
			unknown = append(unknown, it.Name)
		}
	}
	// The gravity holds every grain's extract, so dividing it by part of
	// the bill's would credit the rest to the mash.
	if available > 0 && len(unknown) > 0 {
		b.Findings = append(b.Findings, Finding{
			Severity: SeverityInfo,
			Code:     "partial_extract_data",
			Title:    "Can't compute conversion efficiency",
			Detail: fmt.Sprintf("%s has no extract figure, and the gravity includes what it gave. "+
				"Set it on the material to unlock this.", strings.Join(unknown, ", ")),
		})
		return
	}
	if available <= 0 {
		b.Findings = append(b.Findings, Finding{
			Severity: SeverityInfo,
			Code:     "no_extract_data",
			Title:    "Can't compute conversion efficiency",
			Detail:   "None of the fermentables on this mash carry an extract figure. Set it on the material to unlock this.",
		})
		return
	}

	// Liquor in is not wash out: the grain keeps some, and how much is not a
	// figure Stillhouse has a source for. Efficiency waits for a measurement.
	if r.WashVolumeL == nil || *r.WashVolumeL <= 0 {
		b.Findings = append(b.Findings, Finding{
			Severity: SeverityInfo,
			Code:     "no_wash_volume",
			Title:    "Record the wash volume to see conversion efficiency",
			Detail:   "Efficiency is the extract in the wash against what the grain could give, so it needs the volume collected.",
		})
		return
	}
	volume := *r.WashVolumeL

	sg := *r.OriginalGravity
	plato := PlatoFromSG(sg)
	measured := ExtractInSolutionKg(sg, volume)

	eff := &Efficiency{
		OriginalGravity:    sg,
		Plato:              plato,
		WashVolumeL:        volume,
		ExtractMeasuredKg:  measured,
		ExtractAvailableKg: available,
		Percent:            units.Percent(measured / available * 100),
	}
	b.Efficiency = eff

	switch {
	case eff.Percent > 100:
		b.Findings = append(b.Findings, Finding{
			Severity: SeverityWarning,
			Code:     "efficiency_impossible",
			Title:    fmt.Sprintf("Conversion efficiency computes to %.0f %%", eff.Percent),
			Detail: "Above 100 % means the inputs disagree rather than that the mash over-performed — " +
				"check the gravity reading, the wash volume, and the extract percentages on the materials.",
		})
	case eff.Percent < 60:
		b.Findings = append(b.Findings, Finding{
			Severity: SeverityProblem,
			Code:     "efficiency_low",
			Title:    fmt.Sprintf("Conversion efficiency %.0f %%", eff.Percent),
			Detail: "Well below what a healthy mash returns. Look at the conversion rest temperature, " +
				"the pH, and — if the bill has maize or rice in it — whether the cereal cook actually " +
				"gelatinised the starch before the malt went in.",
		})
	case eff.Percent < 75:
		b.Findings = append(b.Findings, Finding{
			Severity: SeverityWarning,
			Code:     "efficiency_modest",
			Title:    fmt.Sprintf("Conversion efficiency %.0f %%", eff.Percent),
			Detail:   "There is extract left in the tun. Small-scale mashes commonly sit at 75–90 %.",
		})
	}
}

// ExtractInSolutionKg is the extract actually dissolved in a wash, from
// its gravity and volume.
//
//	wort mass ≈ volume × SG   (SG is relative to water at ~1 kg/L)
//	extract   = wort mass × Plato / 100
//
// Exported so that everything computing a conversion efficiency does it
// with this arithmetic rather than its own. The cross-tenant benchmarks
// (stage 209) are the reason that matters beyond tidiness: a cohort is
// only comparable if every tenant's figure came out of the same code, and
// a second copy of this in SQL would have been the first thing to drift.
func ExtractInSolutionKg(originalGravity, washVolumeL float64) float64 {
	return washVolumeL * originalGravity * PlatoFromSG(originalGravity) / 100
}

// ConversionPercent is extract in solution as a share of what the grain
// bill could have given up.
//
// Returns ok=false where the inputs cannot support a figure, rather than
// a zero: a mash with no gravity reading and a mash that converted
// nothing are different claims.
func ConversionPercent(extractAvailableKg, originalGravity, washVolumeL float64) (float64, bool) {
	if extractAvailableKg <= 0 || originalGravity <= 1.0 || washVolumeL <= 0 {
		return 0, false
	}
	return ExtractInSolutionKg(originalGravity, washVolumeL) / extractAvailableKg * 100, true
}
