// Package distilling implements the production-side math: projecting how
// much absolute alcohol a recipe should yield, and (later) reconciling
// projection against actual measurements at each stage.
//
// The chain of conversions for a fermentable input (grain or malt) is:
//
//	grain mass (kg)
//	  × extract fraction          → fermentable starch/sugar mass (kg)
//	  × mash efficiency           → sugar actually freed in the mash
//	  × 0.511 (Gay-Lussac)        → theoretical ethanol mass (kg)
//	  × ferment efficiency        → ethanol actually produced
//	  ÷ 0.78934 kg/L              → ethanol volume (L)
//	  × distillation recovery     → LAA captured in hearts
//
// These are intentionally simplified — real yield varies with grain
// quality, mash pH, yeast vitality, cut timing, and dozens of other
// things — but they're accurate enough to plan a batch and to flag when
// actuals diverge unusually from expectations.
package distilling

import (
	"math"

	"github.com/gallowaysoftware/stillhouse/backend/internal/units"
)

// Conversion constants.
const (
	// GayLussacRatio: theoretical ethanol mass produced per unit mass of
	// fermentable sugar (sucrose-equivalent). 100 g sugar → 51.1 g ethanol
	// + 48.9 g CO2.
	GayLussacRatio = 0.511

	// EthanolDensityKgPerL: density of pure ethanol at 20°C. Used to
	// convert ethanol mass to volume of absolute alcohol.
	EthanolDensityKgPerL = 0.78934
)

// Ingredient is one fermentable input to a projection.
type Ingredient struct {
	// Name is included for diagnostics; not used in math.
	Name string
	// MassKg is the mass of this ingredient, in kilograms.
	MassKg float64
	// Extract is the proportion of MassKg that is fermentable extract.
	// Typical values: corn ~0.72, malted barley ~0.78, unmalted rye
	// ~0.65, wheat ~0.74.
	//
	// Typed as a Fraction rather than a float, because the field next to
	// it in every caller is a strength in percent, and the two are a
	// hundredfold apart. See internal/units.
	Extract units.Fraction
}

// Efficiencies are the per-stage process efficiencies.
//
// All three are fractions and are typed as such. A value of 78 here
// instead of 0.78 would overstate a projection a hundredfold; the type
// is what stops it being passed from a field that means percent.
type Efficiencies struct {
	// Mash is the proportion of extract actually freed into the wort
	// during mashing. Typical small-scale: 0.75–0.90.
	Mash units.Fraction
	// Ferment is the proportion of the theoretical Gay-Lussac ethanol
	// mass actually produced. Typical: 0.85–0.95.
	Ferment units.Fraction
	// DistillationRecovery is the proportion of the wash's ethanol that
	// ends up in the hearts cut. Typical: 0.80–0.95.
	DistillationRecovery units.Fraction
}

// Projection is the result of projecting a recipe through one batch.
type Projection struct {
	// PerIngredient mirrors the order of the input slice; each entry holds
	// the intermediate computations for traceability.
	PerIngredient []IngredientResult
	// TotalProjectedLAA is the sum across PerIngredient.ProjectedLAA.
	TotalProjectedLAA float64
}

// IngredientResult is the per-input breakdown.
type IngredientResult struct {
	Name           string
	MassKg         float64
	FermentableKg  float64 // MassKg × Extract
	ExtractFreedKg float64 // FermentableKg × Mash
	EthanolMassKg  float64 // ExtractFreedKg × 0.511 × Ferment
	EthanolVolumeL float64 // EthanolMassKg ÷ 0.78934
	ProjectedLAA   float64 // EthanolVolumeL × DistillationRecovery
}

// ProjectBatch runs the full projection.
func ProjectBatch(ingredients []Ingredient, eff Efficiencies) Projection {
	p := Projection{PerIngredient: make([]IngredientResult, 0, len(ingredients))}
	for _, in := range ingredients {
		r := projectIngredient(in, eff)
		p.PerIngredient = append(p.PerIngredient, r)
		p.TotalProjectedLAA += r.ProjectedLAA
	}
	// Round to 4 decimal places — LAA is reported on B266 to that precision.
	p.TotalProjectedLAA = round4(p.TotalProjectedLAA)
	return p
}

// WashProjection is the projected wash (post-fermentation, pre-distillation).
type WashProjection struct {
	VolumeL float64
	ABVPct  float64 // 0..100
}

// ProjectWash estimates the wash's volume and ABV. It needs the water added
// to the mash and the per-ingredient projection.
//
// The volume approximation:
//
//	wash_L ≈ water_L + Σ(grain_kg × 0.6 L/kg)
//
// (Mashed grain displaces ~0.6 L of water per kg of dry grain. This is a
// rough average across grain types and is good enough for planning.)
//
// ABV is computed from the projected ethanol *before* the distillation
// recovery factor, since the wash exists pre-distillation.
func ProjectWash(ingredients []Ingredient, eff Efficiencies, waterAddedL float64) WashProjection {
	if waterAddedL <= 0 {
		return WashProjection{}
	}
	grainMassKg := 0.0
	ethanolMassKg := 0.0
	for _, in := range ingredients {
		grainMassKg += in.MassKg
		ethanolMassKg += in.MassKg * in.Extract.Float() *
			eff.Mash.Float() * GayLussacRatio * eff.Ferment.Float()
	}
	washVolumeL := waterAddedL + grainMassKg*0.6
	ethanolVolumeL := ethanolMassKg / EthanolDensityKgPerL
	abv := 0.0
	if washVolumeL > 0 {
		abv = ethanolVolumeL / washVolumeL * 100
	}
	return WashProjection{
		VolumeL: round2(washVolumeL),
		ABVPct:  round2(abv),
	}
}

func projectIngredient(in Ingredient, eff Efficiencies) IngredientResult {
	r := IngredientResult{Name: in.Name, MassKg: in.MassKg}
	if in.MassKg <= 0 || in.Extract <= 0 {
		return r
	}
	r.FermentableKg = in.MassKg * in.Extract.Float()
	r.ExtractFreedKg = r.FermentableKg * eff.Mash.Float()
	r.EthanolMassKg = r.ExtractFreedKg * GayLussacRatio * eff.Ferment.Float()
	r.EthanolVolumeL = r.EthanolMassKg / EthanolDensityKgPerL
	r.ProjectedLAA = round4(r.EthanolVolumeL * eff.DistillationRecovery.Float())
	return r
}

func round4(x float64) float64 { return math.Round(x*10000) / 10000 }
func round2(x float64) float64 { return math.Round(x*100) / 100 }
