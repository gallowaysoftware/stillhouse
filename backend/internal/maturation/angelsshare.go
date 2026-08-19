// Package maturation turns a barrel's fill and regauge history into an
// answer to the question a warehouse actually raises: is this cask losing
// what it should be losing, or is something wrong with it?
//
// Stillhouse already recorded every regauge as a loss. What it never did
// was say whether the loss was normal. A weeping barrel, a bad bung and a
// mis-read dip all look identical in a ledger that only stores the number.
//
// Figures are from the IBD/CIBD distilling curriculum (Module 2 —
// maturation) and cited on the constant that carries them.
package maturation

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Climate describes the warehouse's evaporation regime. The curriculum
// contrasts two, and they behave in opposite directions.
type Climate int

const (
	// ClimateCoolHumid — Scotland, and every Canadian warehouse worth the
	// name. Ethanol's vapour pressure dominates, so alcohol leaves faster
	// than water and the strength falls.
	ClimateCoolHumid Climate = iota
	// ClimateHotDry — Kentucky. Water leaves faster than alcohol and the
	// strength climbs.
	ClimateHotDry
)

// AnnualLossBand is the expected annual volume loss for a climate, as a
// percentage of the volume at the start of each year.
//
// Cool/humid: "The angel's share in these cool, humid environments
// typically ranges from two percent to three percent of total volume
// annually." Hot/dry: "loss rates can reach four to five percent per
// year".
func AnnualLossBand(c Climate) (minPct, maxPct float64) {
	switch c {
	case ClimateHotDry:
		return 4, 5
	default:
		return 2, 3
	}
}

// ExpectedStrengthDrift returns the direction the strength should move in
// a climate: negative loses strength, positive gains it.
//
// "In cool, humid climates like Scotland, loss rates are around two
// percent, with ABV decreasing." The curriculum's worked case is a cask
// filled at 63 % reading 60 % after three years — about one point a year.
func ExpectedStrengthDrift(c Climate) float64 {
	switch c {
	case ClimateHotDry:
		return +1.0
	default:
		return -1.0
	}
}

// RackhouseLevels is the height of the typical rackhouse the curriculum
// describes, running 110 °F at level one to 145 °F at level nine.
const RackhouseLevels = 9

// ClimateForLevel refines the warehouse climate by shelf height, and
// reports whether the level was usable.
//
// "Lower levels are cooler and more humid, favoring alcohol evaporation
// and water ingress... Higher levels are hotter and drier, favoring water
// evaporation." So a cask high in the stack behaves like the hot/dry case
// even in a cool building — which is exactly why the position is worth
// recording.
func ClimateForLevel(base Climate, levelPosition string) (Climate, bool) {
	lvl, ok := ParseLevel(levelPosition)
	if !ok {
		return base, false
	}
	// The top third of the stack runs hot and dry enough to reverse the
	// direction of drift.
	if lvl >= (RackhouseLevels*2)/3 {
		return ClimateHotDry, true
	}
	return base, true
}

// ParseLevel pulls a shelf number out of the free-text level field.
// Operators write "3", "L3", "level 3" and worse; anything without a
// number is simply unknown rather than guessed at.
func ParseLevel(s string) (int, bool) {
	digits := strings.Builder{}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		} else if digits.Len() > 0 {
			break
		}
	}
	if digits.Len() == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(digits.String())
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// MinimumDays is how long a cask must have been in wood before an
// annualised rate means anything. Annualising a fortnight's evaporation
// produces a confident, ridiculous number, and the whole point of this
// package is to stop reporting numbers that aren't earned.
const MinimumDays = 90

// Assessment is what the warehouse wants to know about one cask.
type Assessment struct {
	// Measurable is false when there isn't enough history to say anything.
	// Everything below is meaningless when it is false.
	Measurable bool
	// WhyNot explains the refusal when Measurable is false.
	WhyNot string

	DaysAged float64
	Years    float64

	// AnnualVolumeLossPct is the compounded annual rate: losses compound
	// on a shrinking volume, so a geometric rate is the honest one over
	// multiple years.
	AnnualVolumeLossPct float64
	// AnnualLAALossPct is the same for absolute alcohol — the figure that
	// costs money, since duty is charged on LAA.
	AnnualLAALossPct float64
	// StrengthDriftPerYear is percentage points of ABV per year, signed.
	StrengthDriftPerYear float64

	ExpectedMinPct, ExpectedMaxPct float64
	ExpectedDriftSign              float64
	Climate                        Climate
	ClimateFromLevel               bool

	Findings []Finding
}

// Finding is one observation about a cask.
type Finding struct {
	Severity Severity
	Code     string
	Title    string
	Detail   string
}

type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityProblem
)

// Snapshot is a cask's state at a point in time.
type Snapshot struct {
	VolumeL     float64
	StrengthPct float64
	LAA         float64
}

// Assess compares a cask's fill against its current state.
func Assess(fill, now Snapshot, daysAged float64, base Climate, levelPosition string) Assessment {
	a := Assessment{DaysAged: daysAged}
	climate, fromLevel := ClimateForLevel(base, levelPosition)
	a.Climate, a.ClimateFromLevel = climate, fromLevel
	a.ExpectedMinPct, a.ExpectedMaxPct = AnnualLossBand(climate)
	a.ExpectedDriftSign = ExpectedStrengthDrift(climate)

	switch {
	case daysAged < MinimumDays:
		a.WhyNot = fmt.Sprintf(
			"only %.0f days in wood — an annual rate needs at least %d days behind it to mean anything",
			daysAged, MinimumDays)
		return a
	case fill.VolumeL <= 0 || now.VolumeL <= 0:
		a.WhyNot = "no fill volume on record to compare against"
		return a
	case now.VolumeL > fill.VolumeL:
		a.WhyNot = "the cask holds more than it was filled with — check the gauges before reading a loss rate"
		return a
	}

	a.Measurable = true
	a.Years = daysAged / 365.25
	a.AnnualVolumeLossPct = annualisedLossPct(fill.VolumeL, now.VolumeL, a.Years)
	if fill.LAA > 0 && now.LAA >= 0 && now.LAA <= fill.LAA {
		a.AnnualLAALossPct = annualisedLossPct(fill.LAA, now.LAA, a.Years)
	}
	if fill.StrengthPct > 0 && now.StrengthPct > 0 {
		a.StrengthDriftPerYear = (now.StrengthPct - fill.StrengthPct) / a.Years
	}

	a.assessLossRate()
	a.assessDrift()
	return a
}

// annualisedLossPct is the compounded rate r where end = start × (1−r)^years.
func annualisedLossPct(start, end, years float64) float64 {
	if start <= 0 || years <= 0 || end < 0 {
		return 0
	}
	return (1 - math.Pow(end/start, 1/years)) * 100
}

func (a *Assessment) assessLossRate() {
	r := a.AnnualVolumeLossPct
	switch {
	case r > a.ExpectedMaxPct*2:
		a.Findings = append(a.Findings, Finding{
			Severity: SeverityProblem,
			Code:     "loss_far_above_band",
			Title: fmt.Sprintf("Losing %.1f %% a year — more than double the expected %.0f–%.0f %%",
				r, a.ExpectedMinPct, a.ExpectedMaxPct),
			Detail: "A cask this far outside the band is usually leaking rather than breathing. " +
				"Check the bung and the head joints, and look underneath it.",
		})
	case r > a.ExpectedMaxPct:
		a.Findings = append(a.Findings, Finding{
			Severity: SeverityWarning,
			Code:     "loss_above_band",
			Title: fmt.Sprintf("Losing %.1f %% a year, above the expected %.0f–%.0f %%",
				r, a.ExpectedMinPct, a.ExpectedMaxPct),
			Detail: "Worth an eye. A dry store, a draughty position, or a cask that needs its hoops driven " +
				"will all show up like this.",
		})
	case r < a.ExpectedMinPct/2:
		a.Findings = append(a.Findings, Finding{
			Severity: SeverityInfo,
			Code:     "loss_below_band",
			Title: fmt.Sprintf("Losing only %.1f %% a year, below the expected %.0f–%.0f %%",
				r, a.ExpectedMinPct, a.ExpectedMaxPct),
			Detail: "Not a problem in itself — a tight cask in a cold, damp corner really does hold on to " +
				"its contents. Worth confirming the regauge was a fresh measurement rather than a copy " +
				"of the fill figures.",
		})
	}
}

func (a *Assessment) assessDrift() {
	if a.StrengthDriftPerYear == 0 {
		return
	}
	wrongWay := (a.ExpectedDriftSign < 0 && a.StrengthDriftPerYear > 0.25) ||
		(a.ExpectedDriftSign > 0 && a.StrengthDriftPerYear < -0.25)
	if !wrongWay {
		return
	}
	expected, actual := "falling", "rising"
	if a.ExpectedDriftSign > 0 {
		expected, actual = "rising", "falling"
	}
	where := "a cool, humid warehouse"
	if a.Climate == ClimateHotDry {
		where = "a hot, dry position high in the stack"
	}
	a.Findings = append(a.Findings, Finding{
		Severity: SeverityWarning,
		Code:     "drift_wrong_direction",
		Title: fmt.Sprintf("Strength is %s %.1f points a year, but %s should be %s it",
			actual, math.Abs(a.StrengthDriftPerYear), where, expected),
		Detail: "Strength moving against the position is usually a measurement problem rather than a " +
			"chemistry one — an uncorrected warm gauge reads high. Confirm the reading was taken with " +
			"a temperature and resolved to 20 °C.",
	})
}
