package distilling

import (
	"fmt"
	"sort"
)

// CutKind is a fraction of a batch distillation, in the order it comes off
// the still. Mirrors the wire enum so this package stays free of protobuf.
type CutKind int

const (
	CutUnspecified CutKind = iota
	CutForeshots
	CutHeads
	CutHearts
	CutTails
	CutFeintsSaved
)

func (k CutKind) String() string {
	switch k {
	case CutForeshots:
		return "foreshots"
	case CutHeads:
		return "heads"
	case CutHearts:
		return "hearts"
	case CutTails:
		return "tails"
	case CutFeintsSaved:
		return "feints"
	default:
		return "unspecified"
	}
}

// Cut is one recorded fraction.
type Cut struct {
	Kind    CutKind
	VolumeL float64
	ABVPct  float64
	LAA     float64
	Order   int
}

// Severity ranks a finding.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityProblem
)

// Finding is one observation about a run.
type Finding struct {
	Severity Severity
	Code     string
	Title    string
	Detail   string
}

// RunAnalysis is what a distillation run's cuts add up to.
//
// The judgements here are deliberately limited to what holds for any
// spirit. Cut points are not universal — the curriculum puts a wash run's
// heads and tails cuts at 54 % and 48 % ABV, while a botanical spirit's
// tails are diverted anywhere from 30 % down to 20 % — so this reports the
// hearts window as a figure and leaves the judgement to the distiller,
// rather than failing a gin run against whisky numbers.
//
// What it does check is arithmetic that must hold either way: you cannot
// collect more alcohol than you charged, and strength falls as a run
// proceeds.
type RunAnalysis struct {
	ChargeLAA float64
	CutLAA    float64
	// AccountedPct is cut LAA as a percentage of charge LAA. The shortfall
	// is the run's losses — vapour that never condensed, what stayed in the
	// pot, and what soaked into the still.
	AccountedPct float64

	HeartsLAA      float64
	HeartsSharePct float64 // of the cut total
	// The strength at the first and last hearts fraction — the cut points
	// the distiller actually took, which is the number worth comparing
	// between runs of the same spirit.
	HeartsStartABV float64
	HeartsEndABV   float64
	HeartsSet      bool

	Findings []Finding
}

// AnalyseRun totals a run's charges and cuts and reports what doesn't add up.
func AnalyseRun(chargeLAA float64, cuts []Cut) RunAnalysis {
	a := RunAnalysis{ChargeLAA: chargeLAA}

	ordered := make([]Cut, len(cuts))
	copy(ordered, cuts)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Order < ordered[j].Order })

	var hearts []Cut
	for _, c := range ordered {
		a.CutLAA += c.LAA
		if c.Kind == CutHearts {
			hearts = append(hearts, c)
			a.HeartsLAA += c.LAA
		}
	}
	if a.CutLAA > 0 {
		a.HeartsSharePct = a.HeartsLAA / a.CutLAA * 100
	}
	if chargeLAA > 0 {
		a.AccountedPct = a.CutLAA / chargeLAA * 100
	}
	if len(hearts) > 0 {
		a.HeartsSet = true
		a.HeartsStartABV = hearts[0].ABVPct
		a.HeartsEndABV = hearts[len(hearts)-1].ABVPct
	}

	a.checkMassBalance()
	a.checkStrengthFalls(ordered)
	a.checkFractions(ordered, len(hearts))
	sort.SliceStable(a.Findings, func(i, j int) bool {
		return a.Findings[i].Severity > a.Findings[j].Severity
	})
	return a
}

func (a *RunAnalysis) checkMassBalance() {
	if a.ChargeLAA <= 0 || a.CutLAA <= 0 {
		return
	}
	switch {
	case a.AccountedPct > 100.5:
		a.Findings = append(a.Findings, Finding{
			Severity: SeverityProblem,
			Code:     "cuts_exceed_charge",
			Title: fmt.Sprintf("Cuts hold %.1f %% of what was charged — more alcohol came out than went in",
				a.AccountedPct),
			Detail: "A still cannot create alcohol. Check the charge strengths and the cut " +
				"measurements; the usual cause is a cut recorded at the wash's strength instead " +
				"of the distillate's, or a charge entered at the wrong volume.",
		})
	case a.AccountedPct < 80:
		a.Findings = append(a.Findings, Finding{
			Severity: SeverityWarning,
			Code:     "low_accounting",
			Title:    fmt.Sprintf("Only %.1f %% of the charged alcohol is accounted for in cuts", a.AccountedPct),
			Detail: "Some loss is normal — vapour that never condensed, what stays in the pot and the " +
				"line — but a fifth of the charge is a lot to leave unexplained. Check whether a " +
				"fraction went unrecorded.",
		})
	}
}

// checkStrengthFalls — strength drops as a run proceeds, because the more
// volatile fractions come off first. A later cut that is stronger than an
// earlier one is a mislabelled fraction or a bad reading.
func (a *RunAnalysis) checkStrengthFalls(ordered []Cut) {
	for i := 1; i < len(ordered); i++ {
		prev, cur := ordered[i-1], ordered[i]
		if cur.ABVPct > prev.ABVPct+0.5 {
			a.Findings = append(a.Findings, Finding{
				Severity: SeverityWarning,
				Code:     "strength_rises_through_run",
				Title: fmt.Sprintf("%s (%.1f %%) is stronger than the %s before it (%.1f %%)",
					cur.Kind, cur.ABVPct, prev.Kind, prev.ABVPct),
				Detail: "Strength falls as a run proceeds — the volatile fractions come off first. " +
					"A rise usually means two fractions were recorded in the wrong order, or one " +
					"was gauged warm without a temperature.",
			})
			return // one report is enough; the rest is noise
		}
	}
}

func (a *RunAnalysis) checkFractions(ordered []Cut, heartsCount int) {
	if len(ordered) == 0 {
		return
	}
	if heartsCount == 0 {
		a.Findings = append(a.Findings, Finding{
			Severity: SeverityWarning,
			Code:     "no_hearts",
			Title:    "No hearts fraction recorded",
			Detail: "The spirit cut is the product. A run with heads and tails but no hearts is " +
				"either incomplete in the record or was a stripping run — if it was a stripping " +
				"run, recording the output as feints keeps the ledger honest.",
		})
		return
	}
	var hasForeshots, hasHeads bool
	for _, c := range ordered {
		switch c.Kind {
		case CutForeshots:
			hasForeshots = true
		case CutHeads:
			hasHeads = true
		}
	}
	if !hasForeshots && !hasHeads {
		a.Findings = append(a.Findings, Finding{
			Severity: SeverityInfo,
			Code:     "no_heads",
			Title:    "No foreshots or heads recorded",
			Detail: "Expected on a stripping run. On a spirit run, the early fraction carries the " +
				"most volatile congeners — acetaldehyde, acetone, ethyl acetate — and leaving it " +
				"in the hearts is what makes a spirit read harsh.",
		})
	}
}
