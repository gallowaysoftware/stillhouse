// Package fermenting turns a fermentation's readings into the answer a
// distiller wants mid-ferment: is this going the way it should, and is it
// finished?
//
// Figures are from the IBD/CIBD distilling curriculum (Module 1 —
// fermentation) except where noted, and the one exception is noted loudly.
package fermenting

import (
	"fmt"
	"math"
	"time"
)

// ABVPerGravityPoint converts a fall in specific gravity to alcohol by
// volume: ABV ≈ (OG − FG) × 131.25.
//
// # Why not the curriculum's rule
//
// The curriculum works an example that takes attenuation in brewers'
// degrees and divides by four: a wash going from 1.060 to 0.995 is 65
// degrees of attenuation, "giving sixteen point two five percent ABV".
// That cannot be right. A 1.060 wort does not hold sixteen percent
// alcohol, and the same curriculum states plainly elsewhere that a
// distillery wash finishes at eight to ten percent ABV.
//
// The standard factor reconciles the two: 1.060 → 1.000 gives
// 0.060 × 131.25 = 7.9 % ABV, which lands exactly in the wash band the
// curriculum describes. The divide-by-four rule is off by roughly a
// factor of two, so it is deliberately not used here.
const ABVPerGravityPoint = 131.25

// Temperature control during fermentation: "the temperature is maintained
// at thirty-two degrees Celsius during the growth phase, then allowed to
// drop to twenty-eight degrees Celsius as the stationary phase begins."
const (
	GrowthPhaseTempC     = 32.0
	StationaryPhaseTempC = 28.0
	// Past this, thermal stress is a documented cause of stuck
	// fermentations and drives ethyl acetate production.
	ThermalStressTempC = 35.0
)

// StuckGravityPoints is how far above the target a plateaued gravity has
// to sit before it reads as stuck rather than finished. The curriculum's
// marker is a wash sitting at 1.005 when it should have gone lower —
// five points.
const StuckGravityPoints = 5.0

// Phase is where a fermentation has got to. The curriculum's four phases
// are lag, growth, stationary and post-fermentation.
type Phase int

const (
	PhaseUnknown Phase = iota
	PhaseLag
	PhaseGrowth
	PhaseStationary
	PhaseFinished
)

func (p Phase) String() string {
	switch p {
	case PhaseLag:
		return "lag"
	case PhaseGrowth:
		return "growth"
	case PhaseStationary:
		return "stationary"
	case PhaseFinished:
		return "finished"
	default:
		return "unknown"
	}
}

type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityProblem
)

type Finding struct {
	Severity Severity
	Code     string
	Title    string
	Detail   string
}

// Reading is one logged observation.
type Reading struct {
	At         time.Time
	Gravity    float64
	GravitySet bool
	PH         float64
	PHSet      bool
	TempC      float64
	TempSet    bool
}

// Analysis is what a fermentation's readings add up to.
type Analysis struct {
	Measurable bool

	OriginalGravity float64
	CurrentGravity  float64
	// AttenuationPct is how much of the original extract has gone, as a
	// percentage — the apparent attenuation.
	AttenuationPct float64
	// EstimatedABV is the wash strength implied by the gravity fall. This
	// is the number that decides what to charge the still with.
	EstimatedABV float64

	Phase        Phase
	HoursElapsed float64
	PeakTempC    float64
	TempSet      bool

	Findings []Finding
}

// Analyse walks a fermentation's readings in time order.
func Analyse(readings []Reading, targetFG float64, targetFGSet bool) Analysis {
	a := Analysis{}
	var gravities []Reading
	for _, r := range readings {
		if r.GravitySet {
			gravities = append(gravities, r)
		}
		if r.TempSet && (!a.TempSet || r.TempC > a.PeakTempC) {
			a.PeakTempC, a.TempSet = r.TempC, true
		}
	}
	a.checkTemperature(readings)
	a.checkPH(readings)

	if len(gravities) == 0 {
		return a
	}
	a.Measurable = true
	a.OriginalGravity = gravities[0].Gravity
	a.CurrentGravity = gravities[len(gravities)-1].Gravity
	a.HoursElapsed = gravities[len(gravities)-1].At.Sub(gravities[0].At).Hours()

	ogPoints := (a.OriginalGravity - 1) * 1000
	fgPoints := (a.CurrentGravity - 1) * 1000
	if ogPoints > 0 {
		a.AttenuationPct = (ogPoints - fgPoints) / ogPoints * 100
	}
	if drop := a.OriginalGravity - a.CurrentGravity; drop > 0 {
		a.EstimatedABV = drop * ABVPerGravityPoint
	}

	a.Phase = inferPhase(gravities, targetFG, targetFGSet)
	a.checkStuck(gravities, targetFG, targetFGSet)
	return a
}

// inferPhase reads the shape of the gravity curve. Lag is a gravity that
// hasn't moved yet; growth is a falling one; stationary is a curve that
// has flattened; finished is flat at or below the target.
//
// It looks only at the last two readings, so one mistyped gravity in the
// middle of a series will skew the label. That is a deliberate trade — the
// phase is a glance-level summary, and the findings that actually matter
// (stuck, thermal stress, pH crash) look across the whole series and are
// not fooled by a single bad point.
func inferPhase(g []Reading, targetFG float64, targetFGSet bool) Phase {
	if len(g) < 2 {
		return PhaseLag
	}
	last := g[len(g)-1]
	prev := g[len(g)-2]
	recentDrop := prev.Gravity - last.Gravity
	totalDrop := g[0].Gravity - last.Gravity

	switch {
	case totalDrop < 0.002:
		return PhaseLag
	case recentDrop > 0.005:
		return PhaseGrowth
	case targetFGSet && last.Gravity <= targetFG+0.001:
		return PhaseFinished
	case recentDrop < 0.001:
		return PhaseStationary
	default:
		return PhaseGrowth
	}
}

func (a *Analysis) checkStuck(g []Reading, targetFG float64, targetFGSet bool) {
	if len(g) < 3 || !targetFGSet {
		return
	}
	last := g[len(g)-1]
	// Flat across the last two intervals...
	flat := (g[len(g)-3].Gravity-last.Gravity)*1000 < 2
	// ...but still well above where it was meant to finish.
	above := (last.Gravity - targetFG) * 1000
	if flat && above > StuckGravityPoints {
		a.Findings = append(a.Findings, Finding{
			Severity: SeverityProblem,
			Code:     "stuck_fermentation",
			Title: fmt.Sprintf("Gravity has stalled at %.3f, %.0f points above the %.3f target",
				last.Gravity, above, targetFG),
			Detail: "A fermentation that stops short leaves sugar unconverted, and that is alcohol " +
				"you have already paid for. The documented causes are thermal stress, nutrient " +
				"deficiency and contamination — check the temperature history first, since it is " +
				"the one recorded here.",
		})
	}
}

func (a *Analysis) checkTemperature(readings []Reading) {
	for _, r := range readings {
		if r.TempSet && r.TempC > ThermalStressTempC {
			a.Findings = append(a.Findings, Finding{
				Severity: SeverityWarning,
				Code:     "thermal_stress",
				Title: fmt.Sprintf("Reached %.1f °C, above the %.0f °C stress threshold",
					r.TempC, ThermalStressTempC),
				Detail: fmt.Sprintf("Thermal stress is a documented cause of stuck fermentations, and it "+
					"drives ethyl acetate — solvent and nail-varnish notes that carry into the spirit. "+
					"A controlled ferment holds %.0f °C through growth and eases to %.0f °C at the "+
					"stationary phase.", GrowthPhaseTempC, StationaryPhaseTempC),
			})
			return
		}
	}
}

// checkPH — lactic acid bacteria dropping the pH is the classic
// contamination signature, and it stalls the yeast.
func (a *Analysis) checkPH(readings []Reading) {
	var first, last float64
	var seen bool
	for _, r := range readings {
		if !r.PHSet {
			continue
		}
		if !seen {
			first, seen = r.PH, true
		}
		last = r.PH
	}
	if !seen {
		return
	}
	if drop := first - last; drop > 0.8 {
		a.Findings = append(a.Findings, Finding{
			Severity: SeverityWarning,
			Code:     "ph_crash",
			Title:    fmt.Sprintf("pH fell %.2f, from %.2f to %.2f", drop, first, last),
			Detail: "Some fall is normal as the yeast works. A steep one is the signature of lactic " +
				"acid bacteria taking hold, which stalls the yeast, cuts the yield and can leave a " +
				"ropey slime in the fermenter.",
		})
	}
}

// AttenuationFromGravity is the apparent attenuation for a gravity pair,
// exposed for callers that only have the two endpoints.
func AttenuationFromGravity(og, fg float64) float64 {
	ogPoints := (og - 1) * 1000
	if ogPoints <= 0 {
		return 0
	}
	return (ogPoints - (fg-1)*1000) / ogPoints * 100
}

// EstimateABV is the wash strength implied by a gravity fall.
func EstimateABV(og, fg float64) float64 {
	return math.Max(0, (og-fg)*ABVPerGravityPoint)
}
