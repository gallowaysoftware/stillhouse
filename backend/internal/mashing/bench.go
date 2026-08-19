package mashing

import (
	"fmt"
	"math"
	"sort"
)

// Severity ranks a finding. The bench is advisory — nothing here blocks a
// write — so severity is what tells an operator which line to read first.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityProblem
)

// Finding is one piece of guidance about a mash.
type Finding struct {
	Severity Severity
	// Code is a stable identifier so the UI can style or link a finding
	// without matching on prose.
	Code string
	// Title is one line. Detail explains the consequence — the "so what",
	// which is the part that changes an operator's behaviour.
	Title  string
	Detail string
}

// GrainBillItem is one fermentable in the mash, as actually weighed in.
type GrainBillItem struct {
	Name   string
	Cereal Cereal
	MassKg float64
	// ExtractPct is the fraction (0..1) of MassKg that is fermentable
	// extract, from the material record.
	ExtractPct float64
	// Malted marks a grain that brings its own enzymes.
	Malted bool
}

// Readings are the measurements recorded against the mash. Pointers so
// "not measured" stays distinct from a measured zero.
type Readings struct {
	MashTempC    *float64
	PH           *float64
	WaterVolumeL *float64
	// WashVolumeL is the measured total volume of the mash/wash. When
	// absent it is estimated from water plus grain displacement.
	WashVolumeL     *float64
	OriginalGravity *float64
}

// Bench is the full assessment of one mash.
type Bench struct {
	// GelatinisationC is the range the bill as a whole must reach: the
	// hottest requirement across its cereals. Zero when no cereal in the
	// bill has a published figure.
	GelatinisationC TemperatureRange
	// GelatinisationKnown is false when at least one fermentable has no
	// published gelatinisation range, so the requirement may be understated.
	GelatinisationKnown bool
	// ConversionC is the rest window where the amylases work.
	ConversionC TemperatureRange
	// CerealCookRequired is set when the bill cannot be gelatinised and
	// converted in a single rest.
	CerealCookRequired bool

	// ThicknessLPerKg is water ÷ grain, when both are known.
	ThicknessLPerKg *float64
	// Efficiency is conversion measured from original gravity, when the
	// reading is available.
	Efficiency *Efficiency

	TotalGrainKg float64
	Findings     []Finding
}

// Assess builds the bench for a grain bill and its readings.
func Assess(bill []GrainBillItem, r Readings) Bench {
	b := Bench{ConversionC: Saccharification}
	for _, it := range bill {
		b.TotalGrainKg += it.MassKg
	}

	b.assessGelatinisation(bill)
	b.assessThickness(r)
	b.assessMashTemp(r)
	b.assessPH(r)
	b.assessEfficiency(bill, r)

	sort.SliceStable(b.Findings, func(i, j int) bool {
		return b.Findings[i].Severity > b.Findings[j].Severity
	})
	return b
}

// assessGelatinisation works out the hottest gelatinisation requirement in
// the bill and whether that collides with the enzymes.
//
// The collision is the whole point: maize needs 70–80 °C to gelatinise,
// but beta-amylase is dead by 80 °C and the conversion window tops out at
// 70 °C. A bill with maize in it therefore cannot be gelatinised and
// converted in one rest — the maize has to be cooked separately and cooled
// before the malt goes in.
func (b *Bench) assessGelatinisation(bill []GrainBillItem) {
	var hottest TemperatureRange
	var driver string
	unknown := []string{}
	any := false

	for _, it := range bill {
		if it.MassKg <= 0 {
			continue
		}
		g, ok := Gelatinisation(it.Cereal)
		if !ok {
			unknown = append(unknown, it.Name)
			continue
		}
		any = true
		if g.MaxC > hottest.MaxC {
			hottest = g
			driver = it.Name
		}
	}
	if !any {
		return
	}
	b.GelatinisationC = hottest
	b.GelatinisationKnown = len(unknown) == 0

	if len(unknown) > 0 {
		b.Findings = append(b.Findings, Finding{
			Severity: SeverityInfo,
			Code:     "cereal_unknown",
			Title:    fmt.Sprintf("No published gelatinisation range for %s", joinNames(unknown)),
			Detail: "Set the cereal on these materials to include them in the temperature guidance. " +
				"Until then the requirement below may be understated.",
		})
	}

	// Can the bill FULLY gelatinise inside the window where the amylases
	// still work? The top of the range is what matters: maize starts to
	// gelatinise at 70 °C but is not fully disrupted until 80 °C, and
	// partially-swollen granules hold onto starch that never converts.
	if hottest.MaxC > Saccharification.MaxC {
		b.CerealCookRequired = true
		b.Findings = append(b.Findings, Finding{
			Severity: SeverityProblem,
			Code:     "cereal_cook_required",
			Title: fmt.Sprintf("%s needs %.0f–%.0f °C to gelatinise — above the %.0f–%.0f °C conversion window",
				driver, hottest.MinC, hottest.MaxC, Saccharification.MinC, Saccharification.MaxC),
			Detail: fmt.Sprintf(
				"Cook it separately (%.0f °C for %d minutes fully disrupts the granules), then cool below "+
					"%.0f °C before the malt or exogenous enzymes go in — above that they are denatured instantly, "+
					"and the starch you just freed never converts. High-amylose maize needs at least %.0f °C.",
				PreGelatinisationC, PreGelatinisationMinutes, EnzymeDenaturationC, HighAmyloseMaizeCookC),
		})
		return
	}

	// Everything gelatinises inside the conversion window — say where the
	// rest has to sit to satisfy both.
	restMin := math.Max(hottest.MinC, Saccharification.MinC)
	b.Findings = append(b.Findings, Finding{
		Severity: SeverityInfo,
		Code:     "single_rest",
		Title: fmt.Sprintf("Single rest works: hold %.0f–%.0f °C",
			restMin, Saccharification.MaxC),
		Detail: fmt.Sprintf(
			"%s gelatinises by %.0f °C, inside the amylase window. Toward %.0f °C favours beta-amylase "+
				"and maximises fermentability; toward %.0f °C favours alpha-amylase, leaving more dextrin "+
				"and more body.",
			driver, hottest.MaxC, BetaAmylaseOptimum.MinC, AlphaAmylaseOptimum.MaxC),
	})
}

func (b *Bench) assessThickness(r Readings) {
	if r.WaterVolumeL == nil || b.TotalGrainKg <= 0 {
		return
	}
	t := *r.WaterVolumeL / b.TotalGrainKg
	b.ThicknessLPerKg = &t
	if MashThickness.Contains(t) {
		return
	}
	sev, detail := SeverityWarning, ""
	if t < MashThickness.Min {
		detail = "A thick mash is harder to stir and to keep at an even temperature, and can leave " +
			"starch unconverted in pockets the enzymes never reach."
	} else {
		detail = "A thin mash converts readily but dilutes the wash, which means a weaker charge to " +
			"the still and more energy spent distilling water."
	}
	b.Findings = append(b.Findings, Finding{
		Severity: sev,
		Code:     "thickness_out_of_band",
		Title: fmt.Sprintf("Mash thickness %.2f L/kg is outside the usual %.1f–%.1f L/kg",
			t, MashThickness.Min, MashThickness.Max),
		Detail: detail,
	})
}

func (b *Bench) assessMashTemp(r Readings) {
	if r.MashTempC == nil {
		return
	}
	t := *r.MashTempC
	switch {
	case t >= EnzymeDenaturationC:
		b.Findings = append(b.Findings, Finding{
			Severity: SeverityProblem,
			Code:     "temp_denaturing",
			Title:    fmt.Sprintf("Recorded mash temperature %.1f °C is at or above %.0f °C", t, EnzymeDenaturationC),
			Detail: "Beta-amylase is denatured at this temperature and any enzymes added now are destroyed " +
				"on contact. If this reading was taken during a cereal cook it is expected — cool below " +
				fmt.Sprintf("%.0f °C before mashing in.", EnzymeDenaturationC),
		})
	case t > Saccharification.MaxC:
		b.Findings = append(b.Findings, Finding{
			Severity: SeverityWarning,
			Code:     "temp_above_conversion",
			Title:    fmt.Sprintf("Recorded %.1f °C is above the %.0f–%.0f °C conversion window", t, Saccharification.MinC, Saccharification.MaxC),
			Detail: fmt.Sprintf("Beta-amylase falls away above %.0f °C, so the wash will finish less "+
				"fermentable and yield less alcohol. %.0f °C is the mash-out temperature — deliberate there, "+
				"a problem during conversion.", BetaAmylase.MaxC, MashOutC),
		})
	case t < Saccharification.MinC:
		b.Findings = append(b.Findings, Finding{
			Severity: SeverityWarning,
			Code:     "temp_below_conversion",
			Title:    fmt.Sprintf("Recorded %.1f °C is below the %.0f–%.0f °C conversion window", t, Saccharification.MinC, Saccharification.MaxC),
			Detail:   "Conversion will be slow or incomplete, and starch that never converts is extract you paid for and cannot ferment.",
		})
	}
}

func (b *Bench) assessPH(r Readings) {
	if r.PH == nil {
		return
	}
	ph := *r.PH
	if MashPH.Contains(ph) {
		return
	}
	detail := ""
	sev := SeverityWarning
	if ph > MashPH.Max {
		detail = fmt.Sprintf(
			"Above %.1f — usually high residual alkalinity in the liquor — both amylases are severely "+
				"inhibited. Treat the liquor or acidify the mash.", MashPHAlkalineFrom)
	} else {
		// Canon's worked case: alpha-amylase inhibition at low pH took a
		// wash from 8 % to 6.5 % ABV.
		sev = SeverityProblem
		detail = "Low pH inhibits alpha-amylase. The curriculum's worked case has this taking a wash " +
			"from 8 % down to 6.5 % ABV — roughly a 19 % loss of yield, from a number you can fix with liquor treatment."
	}
	b.Findings = append(b.Findings, Finding{
		Severity: sev,
		Code:     "ph_out_of_band",
		Title:    fmt.Sprintf("Mash pH %.2f is outside the %.1f–%.1f amylase optimum", ph, MashPH.Min, MashPH.Max),
		Detail:   detail,
	})
}

func joinNames(names []string) string {
	switch len(names) {
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		out := ""
		for i, n := range names[:len(names)-1] {
			if i > 0 {
				out += ", "
			}
			out += n
		}
		return out + " and " + names[len(names)-1]
	}
}
