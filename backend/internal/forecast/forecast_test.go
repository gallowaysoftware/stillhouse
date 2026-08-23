package forecast

import (
	"strings"
	"testing"
	"time"
)

func obs(y int, m time.Month, n int32) Observation {
	return Observation{Month: time.Date(y, m, 1, 0, 0, 0, 0, time.UTC), Bottles: n}
}

var forMonth = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

// A dry month inside the window is information. Dropping it — averaging
// only the months that had sales — reads higher than the truth and would
// have somebody produce for demand that is not there.
func TestTrailingAverage_CountsDryMonths(t *testing.T) {
	h := []Observation{
		obs(2026, 4, 300),
		// May: nothing sold. Absent from the history entirely, which is
		// how a sparse ledger looks.
		obs(2026, 6, 300),
	}
	got := Project(TrailingAverage, h, forMonth, 3)
	if !got.Available {
		t.Fatalf("refused: %s", got.Missing)
	}
	// 600 over three months, not 300 over two.
	if got.Bottles != 200 {
		t.Errorf("bottles: got %d, want 200 — a dry month must not be dropped", got.Bottles)
	}
	if got.MonthsUsed != 2 {
		t.Errorf("months used: got %d, want the 2 that had removals", got.MonthsUsed)
	}
	if !strings.Contains(got.Basis, "600 bottles") {
		t.Errorf("basis does not show the working: %q", got.Basis)
	}
}

// The window is the months before the one being forecast, and nothing
// else. Including the forecast month itself would project from data that
// does not exist yet; reaching further back would silently widen the
// average the operator asked for.
func TestTrailingAverage_WindowIsExact(t *testing.T) {
	h := []Observation{
		obs(2026, 1, 9000), // far outside a 3-month window
		obs(2026, 5, 100),
		obs(2026, 6, 200),
		obs(2026, 7, 5000), // the month being forecast; must not count
	}
	got := Project(TrailingAverage, h, forMonth, 3)
	if !got.Available {
		t.Fatalf("refused: %s", got.Missing)
	}
	if got.Bottles != 100 { // (100+200)/3 = 100
		t.Errorf("bottles: got %d, want 100 — window is April–June only", got.Bottles)
	}
}

// No history is a refusal, not a forecast of zero. A product nobody has
// sold and a product forecast to sell nothing are different claims, and
// planning on the second when the first is true under-produces for a
// reason nobody can see.
func TestTrailingAverage_NoHistoryRefuses(t *testing.T) {
	got := Project(TrailingAverage, nil, forMonth, 3)
	if got.Available {
		t.Fatalf("forecast %d bottles from no history at all", got.Bottles)
	}
	if got.Bottles != 0 {
		t.Errorf("refused but reported %d", got.Bottles)
	}
	if !strings.Contains(got.Missing, "nothing to average") {
		t.Errorf("refusal does not explain: %q", got.Missing)
	}
}

func TestSeasonal_UsesTheSameMonthLastYear(t *testing.T) {
	h := []Observation{obs(2025, 7, 450), obs(2026, 6, 10)}
	got := Project(SamePeriodLastYear, h, forMonth, 0)
	if !got.Available {
		t.Fatalf("refused: %s", got.Missing)
	}
	if got.Bottles != 450 {
		t.Errorf("bottles: got %d, want July 2025's 450", got.Bottles)
	}
	if !strings.Contains(got.Basis, "July 2025") {
		t.Errorf("basis does not name the month: %q", got.Basis)
	}
}

// A first year has no same-month-last-year by definition, and that is a
// refusal rather than zero.
func TestSeasonal_FirstYearRefuses(t *testing.T) {
	got := Project(SamePeriodLastYear, []Observation{obs(2026, 6, 100)}, forMonth, 0)
	if got.Available {
		t.Fatalf("projected %d from a year with no record", got.Bottles)
	}
	if !strings.Contains(got.Missing, "first year") {
		t.Errorf("refusal does not explain: %q", got.Missing)
	}
}

// The two methods must be able to disagree, or choosing between them is
// theatre. Same history, seasonal sales, very different answers.
func TestMethodsDisagree(t *testing.T) {
	h := []Observation{
		obs(2025, 7, 1000),                                   // last summer: busy
		obs(2026, 4, 60), obs(2026, 5, 60), obs(2026, 6, 60), // spring: quiet
	}
	trail := Project(TrailingAverage, h, forMonth, 3)
	seas := Project(SamePeriodLastYear, h, forMonth, 0)
	if !trail.Available || !seas.Available {
		t.Fatal("one method refused")
	}
	if trail.Bottles == seas.Bottles {
		t.Errorf("both methods gave %d — a setting that changes nothing is not a choice",
			trail.Bottles)
	}
	if trail.Bottles != 60 || seas.Bottles != 1000 {
		t.Errorf("trailing %d (want 60), seasonal %d (want 1000)", trail.Bottles, seas.Bottles)
	}
}

// An unset method refuses rather than picking one.
func TestNoMethodRefuses(t *testing.T) {
	got := Project("", []Observation{obs(2026, 6, 100)}, forMonth, 3)
	if got.Available {
		t.Error("forecast without a method")
	}
}
