package distilling

import (
	"fmt"

	"github.com/gallowaysoftware/stillhouse/backend/internal/units"
)

// EmpiricalYieldLPerExtractPointPerTonne is the curriculum's working rule
// for predicted spirit yield: 6.06 litres of absolute alcohol per
// percentage point of fermentable extract, per tonne of grain.
//
// Note it sits below the stoichiometric figure — 1000 kg × 1 % extract ×
// 0.511 ÷ 0.78934 is 6.47 L — because it already carries the losses a real
// distillery takes across mashing, fermentation and the cut. That is what
// makes it the useful benchmark: a recipe projecting above it is claiming
// a better-than-industry process, not merely a good one.
const EmpiricalYieldLPerExtractPointPerTonne = 6.06

// SpiritMaltFermentability is the fraction of a distilling malt's extract
// that actually ferments — "malt bred for maximum spirit yield will have
// high beta-amylase activity, often exceeding eighty-seven percent
// fermentability".
//
// This matters because the two figures are easy to conflate. A material's
// recorded extract percentage is its total extract; the 6.06 factor above
// is quoted per point of FERMENTABLE extract. Multiplying the empirical
// factor by total extract overstates the benchmark by about fifteen
// percent — for 78 % malt it gives 473 L/tonne against a real target near
// 425, which would have had Stillhouse calling a perfectly good recipe
// optimistic.
const SpiritMaltFermentability = 0.87

// MaxPredictedSpiritYieldLPerTonne is the yield the curriculum says malt
// bred specifically for spirit production should be targeting — "the
// Predicted Spirit Yield should target four hundred twenty-five liters per
// tonne". A recipe projecting past this is not describing barley.
const MaxPredictedSpiritYieldLPerTonne = 425.0

// YieldCheck compares what a recipe projects against what grain can
// actually give.
//
// The reason this exists: the projection multiplies three efficiencies the
// operator types in, and nothing ever asked whether the product was
// physically possible. A recipe with mash, ferment and recovery all left
// at 1.0 projects a yield no distillery has ever achieved, and the number
// looks just as confident as a real one.
type YieldCheck struct {
	// GrainKg and ProjectedLAA are the inputs, restated.
	GrainKg      float64
	ProjectedLAA float64
	// LPerTonne is the projection expressed the way the industry quotes
	// yield, so it can be compared with a published figure.
	LPerTonne float64
	// WeightedExtract is the bill's extract, mass-weighted. A fraction,
	// and typed as one — see internal/units.
	WeightedExtract units.Fraction
	// TheoreticalMaxLPerTonne is the stoichiometric ceiling for this bill:
	// every gram of extract converted and every gram of sugar fermented,
	// with nothing lost anywhere. Physically unreachable.
	TheoreticalMaxLPerTonne float64
	// AchievableLPerTonne is the curriculum's empirical figure for this
	// bill — what a competent distillery should actually get.
	AchievableLPerTonne float64
	// Measurable is false when the bill has no mass or no extract data.
	Measurable bool

	Findings []Finding
}

// CheckYield assesses a projection against the grain that produced it.
func CheckYield(ingredients []Ingredient, projectedLAA float64) YieldCheck {
	y := YieldCheck{ProjectedLAA: projectedLAA}

	extractKg := 0.0
	for _, in := range ingredients {
		if in.MassKg <= 0 {
			continue
		}
		y.GrainKg += in.MassKg
		extractKg += in.MassKg * in.Extract.Float()
	}
	if y.GrainKg <= 0 || extractKg <= 0 {
		return y
	}
	y.Measurable = true
	y.WeightedExtract = units.Fraction(extractKg / y.GrainKg)
	tonnes := y.GrainKg / 1000
	y.LPerTonne = projectedLAA / tonnes

	// Stoichiometric ceiling: extract → ethanol by Gay-Lussac, converted
	// to volume. Nothing lost at any stage.
	y.TheoreticalMaxLPerTonne = y.WeightedExtract.Float() * 1000 * GayLussacRatio / EthanolDensityKgPerL
	// And what the curriculum says is actually achievable — the empirical
	// factor applied to the fermentable share of the extract, not to all
	// of it.
	y.AchievableLPerTonne = y.WeightedExtract.Float() * SpiritMaltFermentability * 100 *
		EmpiricalYieldLPerExtractPointPerTonne

	y.assess()
	return y
}

func (y *YieldCheck) assess() {
	// Before comparing against anything derived: extract is the fraction of
	// the grain's mass that is fermentable, so it cannot exceed 1. Above
	// that the bill says there is more extract than there is grain, and
	// every ceiling below — theoretical and achievable alike — is computed
	// from that same figure, so they all rescale with the error and none of
	// them can detect it. This is the one test that needs no comparison.
	//
	// It happens because the field is named extract_pct and holds a
	// fraction: 78 gets typed where 0.78 belongs, and the projection comes
	// back a hundredfold too big while looking exactly as confident as a
	// real one.
	if y.WeightedExtract > 1 {
		y.Findings = append(y.Findings, Finding{
			Severity: SeverityProblem,
			Code:     "extract_out_of_range",
			Title: fmt.Sprintf("Extract of %.2f means more extract than grain — this projection is not usable",
				y.WeightedExtract.Float()),
			Detail: "Extract is a fraction of the ingredient's mass, not a percentage: malted " +
				"barley is about 0.80, not 80. Correct it on the materials and the projection " +
				"will fall by roughly a hundredfold.",
		})
		// Everything below compares against ceilings built from this
		// number. Reporting them too would just be more wrong arithmetic.
		return
	}

	switch {
	case y.LPerTonne > y.TheoreticalMaxLPerTonne:
		y.Findings = append(y.Findings, Finding{
			Severity: SeverityProblem,
			Code:     "yield_exceeds_theoretical",
			Title: fmt.Sprintf("Projected %.0f L/tonne is above the %.0f L/tonne this grain can physically give",
				y.LPerTonne, y.TheoreticalMaxLPerTonne),
			Detail: "That ceiling assumes every gram of extract converts and every gram of sugar " +
				"ferments, with nothing lost anywhere — it cannot be reached, let alone beaten. " +
				"Check the efficiency figures on the recipe and the extract percentage on the materials.",
		})
	case y.LPerTonne > MaxPredictedSpiritYieldLPerTonne:
		y.Findings = append(y.Findings, Finding{
			Severity: SeverityWarning,
			Code:     "yield_above_best_malt",
			Title: fmt.Sprintf("Projected %.0f L/tonne is above the %.0f L/tonne target for malt bred for spirit yield",
				y.LPerTonne, MaxPredictedSpiritYieldLPerTonne),
			Detail: "Achievable only with the best distilling malt and a process to match. If this is a " +
				"mixed bill or unmalted grain, the efficiencies are probably optimistic.",
		})
	case y.LPerTonne > y.AchievableLPerTonne*1.05:
		y.Findings = append(y.Findings, Finding{
			Severity: SeverityWarning,
			Code:     "yield_above_empirical",
			Title: fmt.Sprintf("Projected %.0f L/tonne is ahead of the %.0f L/tonne this bill should give",
				y.LPerTonne, y.AchievableLPerTonne),
			Detail: "The benchmark already allows for normal losses across mashing, fermentation and " +
				"the cut. Beating it means claiming a better-than-industry process — worth confirming " +
				"against what you actually collected last time.",
		})
	case y.LPerTonne < y.AchievableLPerTonne*0.75:
		y.Findings = append(y.Findings, Finding{
			Severity: SeverityInfo,
			Code:     "yield_conservative",
			Title: fmt.Sprintf("Projected %.0f L/tonne is well under the %.0f L/tonne this bill should give",
				y.LPerTonne, y.AchievableLPerTonne),
			Detail: "Nothing wrong with a conservative plan. If it matches what you actually collect, " +
				"there is yield being left in the tun — look at the conversion rest and the mash pH.",
		})
	}
}
