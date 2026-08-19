package mashing

import "errors"

// SpecificHeatRatio is the specific heat capacity of grain relative to
// water: grain ≈ 1.72 kJ/kg·K against water's 4.18 kJ/kg·K.
//
// This is the only constant in the strike calculation, and it is what makes
// the result an estimate rather than a guarantee — real grain varies with
// moisture content, and a cold tun steals heat the equation knows nothing
// about. Distilleries settle on their own working figure after a few
// mashes; this gets a new one close on the first try.
const SpecificHeatRatio = 1.72 / 4.18

// StrikeTemperature returns the temperature to heat the mash liquor to so
// that grain at grainTempC lands at targetTempC once mashed in.
//
// Derived from an energy balance rather than a rule of thumb — the heat the
// water gives up equals the heat the grain takes on:
//
//	m_water · c_water · (T_strike − T_target) = m_grain · c_grain · (T_target − T_grain)
//
// Dividing through by m_grain · c_water and writing R for the water-to-grain
// ratio in litres per kilogram (1 L of water ≈ 1 kg):
//
//	T_strike = T_target + (SpecificHeatRatio / R) · (T_target − T_grain)
//
// The thinner the mash, the more thermal mass the water has and the less it
// has to be overshot; a 2.5 L/kg mash needs noticeably hotter liquor than a
// 3.5 L/kg one for the same target.
func StrikeTemperature(targetTempC, grainTempC, thicknessLPerKg float64) (float64, error) {
	if thicknessLPerKg <= 0 {
		return 0, errors.New("mash thickness must be > 0 L/kg")
	}
	return targetTempC + (SpecificHeatRatio/thicknessLPerKg)*(targetTempC-grainTempC), nil
}

// StrikePlan is a strike calculation with the sanity checks that matter at
// the tun.
type StrikePlan struct {
	TargetTempC     float64
	GrainTempC      float64
	ThicknessLPerKg float64
	StrikeTempC     float64
	// WaterVolumeL is the liquor needed for GrainKg at this thickness.
	WaterVolumeL float64
	GrainKg      float64
	Findings     []Finding
}

// PlanStrike computes the strike temperature and liquor volume for a mash,
// and flags the two ways it can go wrong.
func PlanStrike(targetTempC, grainTempC, thicknessLPerKg, grainKg float64) (StrikePlan, error) {
	strike, err := StrikeTemperature(targetTempC, grainTempC, thicknessLPerKg)
	if err != nil {
		return StrikePlan{}, err
	}
	p := StrikePlan{
		TargetTempC:     targetTempC,
		GrainTempC:      grainTempC,
		ThicknessLPerKg: thicknessLPerKg,
		StrikeTempC:     strike,
		GrainKg:         grainKg,
		WaterVolumeL:    grainKg * thicknessLPerKg,
	}

	// The trap: a thick mash aiming high can put the required liquor
	// temperature past the point where it destroys the enzymes it is
	// meant to activate.
	if strike >= EnzymeDenaturationC {
		p.Findings = append(p.Findings, Finding{
			Severity: SeverityProblem,
			Code:     "strike_denatures",
			Title:    "Strike temperature is at or above the enzyme denaturation point",
			Detail: "Liquor this hot destroys the amylases as the grain hits it. Thin the mash, " +
				"warm the grain, or mash in cooler and raise to the rest temperature with heat.",
		})
	}
	if !Saccharification.Contains(targetTempC) {
		p.Findings = append(p.Findings, Finding{
			Severity: SeverityWarning,
			Code:     "target_outside_conversion",
			Title:    "Target rest is outside the conversion window",
			Detail:   "Intended for a cereal cook or a mash-out, this is fine. For conversion, aim inside the amylase window.",
		})
	}
	if !MashThickness.Contains(thicknessLPerKg) {
		p.Findings = append(p.Findings, Finding{
			Severity: SeverityInfo,
			Code:     "thickness_unusual",
			Title:    "Thickness is outside the usual band",
			Detail:   "Workable, but the strike figure gets less reliable the further you sit from ordinary mash ratios.",
		})
	}
	return p, nil
}
