package distilling

import (
	"math"
	"testing"
)

func cut(kind CutKind, order int, vol, abv float64) Cut {
	return Cut{Kind: kind, Order: order, VolumeL: vol, ABVPct: abv, LAA: vol * abv / 100}
}

// A tidy spirit run: foreshots discarded, heads, a good hearts window,
// tails saved as feints.
func goodRun() []Cut {
	return []Cut{
		cut(CutForeshots, 1, 2, 82),
		cut(CutHeads, 2, 12, 78),
		cut(CutHearts, 3, 60, 72),
		cut(CutHearts, 4, 40, 62),
		cut(CutTails, 5, 30, 45),
		cut(CutFeintsSaved, 6, 25, 28),
	}
}

func TestAnalyseTidyRun(t *testing.T) {
	cuts := goodRun()
	total := 0.0
	for _, c := range cuts {
		total += c.LAA
	}
	a := AnalyseRun(total/0.9, cuts) // 90 % accounted for

	if math.Abs(a.CutLAA-total) > 1e-9 {
		t.Errorf("cut LAA = %v, want %v", a.CutLAA, total)
	}
	if math.Abs(a.AccountedPct-90) > 1e-6 {
		t.Errorf("accounted = %.4f %%, want 90", a.AccountedPct)
	}
	if !a.HeartsSet {
		t.Fatal("hearts should be detected")
	}
	// The window is the strength of the first and last hearts fraction —
	// the cut points the distiller actually took.
	if a.HeartsStartABV != 72 || a.HeartsEndABV != 62 {
		t.Errorf("hearts window = %.1f→%.1f, want 72→62", a.HeartsStartABV, a.HeartsEndABV)
	}
	heartsLAA := 60*0.72 + 40*0.62
	if math.Abs(a.HeartsLAA-heartsLAA) > 1e-9 {
		t.Errorf("hearts LAA = %v, want %v", a.HeartsLAA, heartsLAA)
	}
	for _, f := range a.Findings {
		if f.Severity > SeverityInfo {
			t.Errorf("tidy run should raise nothing serious, got: %s", f.Title)
		}
	}
}

// TestCutsCannotExceedTheCharge — the arithmetic that holds for any spirit.
func TestCutsCannotExceedTheCharge(t *testing.T) {
	cuts := goodRun()
	total := 0.0
	for _, c := range cuts {
		total += c.LAA
	}
	a := AnalyseRun(total*0.8, cuts) // charged less than came out
	if !has(a.Findings, "cuts_exceed_charge") {
		t.Errorf("collecting more than was charged must be flagged; got %+v", a.Findings)
	}
	// And it's a problem, not a nicety.
	if sev(a.Findings, "cuts_exceed_charge") != SeverityProblem {
		t.Error("more alcohol out than in is a problem-level finding")
	}
}

func TestLowAccountingIsFlagged(t *testing.T) {
	cuts := goodRun()
	total := 0.0
	for _, c := range cuts {
		total += c.LAA
	}
	a := AnalyseRun(total/0.6, cuts) // only 60 % accounted for
	if !has(a.Findings, "low_accounting") {
		t.Errorf("a 40 %% shortfall should be queried; got %+v", a.Findings)
	}
	// A normal run's losses must not trip it.
	b := AnalyseRun(total/0.92, cuts)
	if has(b.Findings, "low_accounting") {
		t.Error("92 %% accounted for is a normal run, not a finding")
	}
}

// TestStrengthMustFallThroughTheRun — the volatile fractions come off
// first, so a later cut being stronger is a mislabelled fraction or a bad
// gauge.
func TestStrengthMustFallThroughTheRun(t *testing.T) {
	cuts := []Cut{
		cut(CutHeads, 1, 10, 60),
		cut(CutHearts, 2, 50, 75), // stronger than the heads before it
		cut(CutTails, 3, 20, 40),
	}
	a := AnalyseRun(100, cuts)
	if !has(a.Findings, "strength_rises_through_run") {
		t.Errorf("a rising strength must be flagged; got %+v", a.Findings)
	}
	// A tidy run must not trip it.
	if has(AnalyseRun(100, goodRun()).Findings, "strength_rises_through_run") {
		t.Error("a falling profile should not be flagged")
	}
}

// TestSmallStrengthWobbleIsTolerated — gauges disagree by a tenth; the
// check exists for mislabelled fractions, not for measurement noise.
func TestSmallStrengthWobbleIsTolerated(t *testing.T) {
	cuts := []Cut{
		cut(CutHearts, 1, 50, 70.0),
		cut(CutHearts, 2, 50, 70.3),
	}
	if has(AnalyseRun(100, cuts).Findings, "strength_rises_through_run") {
		t.Error("a 0.3 point wobble between gauges is not a mislabelled fraction")
	}
}

func TestMissingHeartsIsFlagged(t *testing.T) {
	cuts := []Cut{
		cut(CutHeads, 1, 10, 78),
		cut(CutTails, 2, 40, 40),
	}
	a := AnalyseRun(100, cuts)
	if !has(a.Findings, "no_hearts") {
		t.Errorf("a run with no spirit cut should be queried; got %+v", a.Findings)
	}
}

func TestNoHeadsIsNotedButNotAlarming(t *testing.T) {
	cuts := []Cut{
		cut(CutHearts, 1, 60, 70),
		cut(CutTails, 2, 20, 40),
	}
	a := AnalyseRun(100, cuts)
	if !has(a.Findings, "no_heads") {
		t.Errorf("want the no-heads note; got %+v", a.Findings)
	}
	if sev(a.Findings, "no_heads") != SeverityInfo {
		t.Error("a stripping run is normal — this is information, not a warning")
	}
}

// TestCutOrderIsRespectedNotInputOrder — cuts arrive in whatever order the
// database returned them; the analysis sorts by the recorded cut order.
func TestCutOrderIsRespectedNotInputOrder(t *testing.T) {
	shuffled := []Cut{
		cut(CutTails, 5, 30, 45),
		cut(CutHearts, 3, 60, 72),
		cut(CutForeshots, 1, 2, 82),
		cut(CutHearts, 4, 40, 62),
		cut(CutHeads, 2, 12, 78),
	}
	a := AnalyseRun(200, shuffled)
	if a.HeartsStartABV != 72 || a.HeartsEndABV != 62 {
		t.Errorf("hearts window = %.1f→%.1f, want 72→62 regardless of input order",
			a.HeartsStartABV, a.HeartsEndABV)
	}
	if has(a.Findings, "strength_rises_through_run") {
		t.Error("sorting by cut order should show a falling profile")
	}
}

func TestEmptyRunSaysNothing(t *testing.T) {
	a := AnalyseRun(0, nil)
	if len(a.Findings) != 0 {
		t.Errorf("a run with no cuts has nothing to say yet, got %+v", a.Findings)
	}
	if a.HeartsSet {
		t.Error("no hearts on an empty run")
	}
}

func has(fs []Finding, code string) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}

func sev(fs []Finding, code string) Severity {
	for _, f := range fs {
		if f.Code == code {
			return f.Severity
		}
	}
	return -1
}
