package rpc

import (
	"strings"
	"testing"
	"time"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The opening balance on a B266 is reverse-walked from the closing one,
// which means the return always balances against itself: a movement that
// was never recorded, or recorded against the wrong line, is absorbed into
// opening rather than showing up as a discrepancy. Every figure on the
// return is derived from the same ledger, so nothing on it can catch that.
//
// The prior return's closing balance is the one number that can. It is not
// derived from the current ledger — it is what the licensee already sent
// CRA — so it does not move when the ledger moves underneath it. These
// tests are about that comparison and nothing else.

var priorStart = time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
var priorEnd = time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)

// continuous is a totals struct whose books tie out: the prior period
// closed where this one opens.
//
// The opening balance is reverse-walked as closing - receipts +
// withdrawals, so to make this period open at 100 with 40 produced and 25
// sent to packaging, it must close at 115.
func continuous() b266Totals {
	return b266Totals{
		byReason: map[string]float64{
			"production_gauge":      40,
			"transfer_to_packaging": 25,
		},
		bulkClosingLAA:       115,
		priorFiled:           true,
		priorStart:           priorStart,
		priorEnd:             priorEnd,
		priorBulkClosing:     100,
		priorPackagedClosing: 0,
	}
}

func TestContinuity_AgreeingBooksAreNotABlocker(t *testing.T) {
	rep := projectB266(continuous(), testPeriodStart, testPeriodEnd, testGeneratedAt)

	if rep.BulkOpeningLaa != 100 {
		t.Fatalf("opening: got %v, want 100 — the fixture itself is wrong", rep.BulkOpeningLaa)
	}
	c := rep.GetContinuity()
	if !c.GetChecked() {
		t.Error("checked: got false, want true — there is a prior filed period")
	}
	if c.GetBulkDiscrepancyLaa() != 0 {
		t.Errorf("discrepancy: got %v, want 0", c.GetBulkDiscrepancyLaa())
	}
	if c.GetPriorPeriodEnd() != "2026-04-30" {
		t.Errorf("prior end: got %q", c.GetPriorPeriodEnd())
	}
	for _, b := range rep.GetFilingBlockers() {
		if strings.Contains(b, "opening balance") {
			t.Errorf("continuous books produced a blocker: %s", b)
		}
	}
}

// The case the whole feature exists for. The prior return was filed
// showing 100 LAA on hand; someone then recorded a 30 LAA production into
// that closed period. The current period's opening walks back to 130,
// which no longer matches what CRA holds.
func TestContinuity_BreakIsReportedAndPointsAtTheEntries(t *testing.T) {
	tt := continuous()
	tt.bulkClosingLAA = 145 // the late 30 LAA is on hand now
	tt.backdatedCount = 1
	tt.backdatedNetLAA = 30
	tt.backdated = []*stillhousev1.B266BackdatedEntry{{
		Id: "m1", Kind: "bulk movement", Reason: "production_gauge",
		Laa: 30, OccurredAt: "2026-04-12", CreatedAt: "2026-05-20",
		Container: "Spirit Receiver 1",
	}}

	rep := projectB266(tt, testPeriodStart, testPeriodEnd, testGeneratedAt)
	c := rep.GetContinuity()

	if c.GetBulkDiscrepancyLaa() != 30 {
		t.Fatalf("discrepancy: got %v, want 30", c.GetBulkDiscrepancyLaa())
	}
	joined := strings.Join(rep.GetFilingBlockers(), " | ")
	if !strings.Contains(joined, "Bulk opening balance is 130.0000") {
		t.Errorf("blocker does not state the opening balance: %s", joined)
	}
	if !strings.Contains(joined, "2026-04-01 to 2026-04-30") {
		t.Errorf("blocker does not name the filed period: %s", joined)
	}
	// The point of naming the backdated entries is that the operator can
	// act without going looking, so the blocker has to say the difference
	// is explained rather than leaving them to compare two numbers.
	if !strings.Contains(joined, "accounts for the bulk difference exactly") {
		t.Errorf("blocker does not tie the entries to the difference: %s", joined)
	}
	if !strings.Contains(joined, "1 entry dated inside that filed period") {
		t.Errorf("blocker miscounts or misinflects: %s", joined)
	}
}

// A break with nothing recorded late is a different problem with a
// different fix, and must not be described as if a late entry caused it.
func TestContinuity_UnexplainedBreakSaysSo(t *testing.T) {
	tt := continuous()
	tt.bulkClosingLAA = 130 // opening walks to 115 against a filed 100

	rep := projectB266(tt, testPeriodStart, testPeriodEnd, testGeneratedAt)
	joined := strings.Join(rep.GetFilingBlockers(), " | ")

	if !strings.Contains(joined, "not explained by a late entry") {
		t.Errorf("want the unexplained wording, got: %s", joined)
	}
	if strings.Contains(joined, "entries dated inside") {
		t.Errorf("blamed a late entry when there were none: %s", joined)
	}
}

// No prior filed period is not a clean bill of health, and the two must be
// distinguishable on the wire. A first return has nothing to check
// against; reporting checked=true with a zero discrepancy would say the
// opposite of the truth.
func TestContinuity_FirstReturnIsUncheckedNotClean(t *testing.T) {
	tt := continuous()
	tt.priorFiled = false

	rep := projectB266(tt, testPeriodStart, testPeriodEnd, testGeneratedAt)
	c := rep.GetContinuity()

	if c.GetChecked() {
		t.Error("checked: got true with no prior filed period")
	}
	if c.GetBulkDiscrepancyLaa() != 0 || c.GetPriorPeriodEnd() != "" {
		t.Errorf("unchecked continuity carries comparison figures: %+v", c)
	}
	for _, b := range rep.GetFilingBlockers() {
		if strings.Contains(b, "opening balance") {
			t.Errorf("first return produced a continuity blocker: %s", b)
		}
	}
	// It still reports this return's own opening balance, which is what
	// the next period will be compared against.
	if c.GetBulkOpeningLaa() != 100 {
		t.Errorf("opening: got %v, want 100", c.GetBulkOpeningLaa())
	}
}

// Periods that do not abut are legitimate — a licensee with nothing to
// report need not file — but the comparison reaches across the gap, so
// anything that moved inside it reads as a break. A signal that fires
// without explaining that is one an operator learns to ignore.
func TestContinuity_GapBetweenPeriodsIsNamed(t *testing.T) {
	tt := continuous()
	tt.priorEnd = time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	tt.priorStart = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	rep := projectB266(tt, testPeriodStart, testPeriodEnd, testGeneratedAt)
	c := rep.GetContinuity()

	if !c.GetGap() {
		t.Fatal("gap: got false for 2026-03-31 → 2026-05-01")
	}
	if !strings.Contains(c.GetGapNote(), "30 days") {
		t.Errorf("gap note does not size the span: %q", c.GetGapNote())
	}
	// Abutting periods must not be flagged.
	tt2 := continuous()
	if projectB266(tt2, testPeriodStart, testPeriodEnd, testGeneratedAt).GetContinuity().GetGap() {
		t.Error("gap reported for periods that abut exactly")
	}
}

// The list of offending entries is capped; the count and the net effect
// are not. A period with more late entries than the cap must still report
// the true total, or the operator reconciles against a number that is
// short by however many the cap hid.
func TestContinuity_TruncationReportsTheWholeSet(t *testing.T) {
	tt := continuous()
	tt.bulkClosingLAA = 145
	tt.backdatedCount = 57
	tt.backdatedNetLAA = 30
	for i := 0; i < backdatedListLimit; i++ {
		tt.backdated = append(tt.backdated, &stillhousev1.B266BackdatedEntry{Id: "m", Laa: 1})
	}

	c := projectB266(tt, testPeriodStart, testPeriodEnd, testGeneratedAt).GetContinuity()
	if got := c.GetBackdatedTruncated(); got != 57-backdatedListLimit {
		t.Errorf("truncated: got %d, want %d", got, 57-backdatedListLimit)
	}
	if c.GetBackdatedNetLaa() != 30 {
		t.Errorf("net: got %v, want the whole set's 30", c.GetBackdatedNetLaa())
	}
	joined := strings.Join(projectB266(tt, testPeriodStart, testPeriodEnd, testGeneratedAt).GetFilingBlockers(), " | ")
	if !strings.Contains(joined, "57 entries") {
		t.Errorf("blocker reports the capped list rather than the whole set: %s", joined)
	}
}

// Both sides are round4 figures. Comparing at exact equality would report
// a break on floating-point noise, which would make the check useless
// precisely because it never stops firing.
func TestContinuity_ToleranceAbsorbsRoundingNoise(t *testing.T) {
	tt := continuous()
	tt.priorBulkClosing = 100.00001

	rep := projectB266(tt, testPeriodStart, testPeriodEnd, testGeneratedAt)
	for _, b := range rep.GetFilingBlockers() {
		if strings.Contains(b, "opening balance") {
			t.Errorf("rounding noise produced a blocker: %s", b)
		}
	}
}
