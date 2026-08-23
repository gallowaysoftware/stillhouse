package forecast

import (
	"math"
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

// A forecast is only useful if it says what has to be made and bought.
// The two halves fail independently and for different reasons, and
// collapsing them into one availability would hide a usable answer
// behind a missing one.

func aRecipe() *RecipeBatch {
	return &RecipeBatch{
		Name:         "House rye v3",
		ProjectedLAA: 100,
		Ingredients: []GrainLine{
			{Material: "Rye", Quantity: 200, UOM: "kg"},
			{Material: "Malted barley", Quantity: 50, UOM: "kg"},
		},
	}
}

func TestRequire_NetsAgainstStockAndScalesTheBill(t *testing.T) {
	// 1000 forecast, 400 on hand: 600 to make. 750 mL at 40% = 0.3 LAA
	// each, so 180 LAA — 1.8 batches of a recipe that makes 100.
	got := Require(1000, 400, 750, 40, aRecipe())

	if got.BottlesToMake != 600 {
		t.Errorf("bottles: got %d, want 600", got.BottlesToMake)
	}
	if got.LAANeeded != 180 {
		t.Errorf("LAA: got %v, want 180", got.LAANeeded)
	}
	if !got.GrainAvailable {
		t.Fatalf("grain refused: %s", got.GrainMissing)
	}
	if got.Batches != 1.8 {
		t.Errorf("batches: got %v, want 1.8", got.Batches)
	}
	if len(got.GrainLines) != 2 || got.GrainLines[0].Quantity != 360 {
		t.Errorf("grain lines: %+v", got.GrainLines)
	}
}

// Stock already covering the forecast means nothing to make, and that has
// to read as nothing rather than as a negative requirement.
func TestRequire_StockCoveringTheForecastNeedsNothing(t *testing.T) {
	got := Require(100, 400, 750, 40, aRecipe())
	if got.BottlesToMake != 0 {
		t.Errorf("bottles: got %d, want 0", got.BottlesToMake)
	}
	if got.LAANeeded != 0 {
		t.Errorf("LAA: got %v, want 0", got.LAANeeded)
	}
	if got.GrainAvailable {
		t.Error("asked for grain when nothing needs making")
	}
}

// The alcohol figure needs only the product's own size and strength, so
// it must survive a missing recipe. Reporting both as one availability
// would hide it.
func TestRequire_NoRecipeStillGivesTheAlcoholFigure(t *testing.T) {
	got := Require(1000, 0, 750, 40, nil)
	if got.LAANeeded != 300 {
		t.Errorf("LAA: got %v, want 300 — this needs no recipe", got.LAANeeded)
	}
	if got.BottlesToMake != 1000 {
		t.Errorf("bottles: got %d", got.BottlesToMake)
	}
	if got.GrainAvailable {
		t.Error("produced grain quantities with no recipe linked")
	}
	if !strings.Contains(got.GrainMissing, "Link one") {
		t.Errorf("refusal does not say what to do: %q", got.GrainMissing)
	}
}

// A recipe that projects no alcohol cannot be scaled, and dividing by it
// would produce an infinity that reads as a very large grain order.
func TestRequire_RecipeProjectingNothingRefuses(t *testing.T) {
	r := aRecipe()
	r.ProjectedLAA = 0
	got := Require(1000, 0, 750, 40, r)
	if got.GrainAvailable {
		t.Fatalf("scaled a recipe that projects nothing: %+v", got.GrainLines)
	}
	for _, l := range got.GrainLines {
		if math.IsInf(l.Quantity, 0) || math.IsNaN(l.Quantity) {
			t.Errorf("%s: %v", l.Material, l.Quantity)
		}
	}
}

// A batch count is arithmetic; a mash count is a fact about the plant.
// 2.4 batches is three mashes on one tun and two on a larger one, and
// Stillhouse cannot pick the tun.

var needBy = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

func aReq() Requirement {
	return Requirement{GrainAvailable: true, Batches: 2.4, LAANeeded: 240}
}

func TestPlan_RoundsBatchesUpToWholeMashes(t *testing.T) {
	// A tun that holds exactly one batch: 2.4 batches is three mashes.
	got := Plan(aReq(), &Plant{Name: "Mash tun 1", CapacityL: 1000, BatchVolumeL: 1000},
		Lead{MaxDays: 14, TotalLines: 2, Slowest: "Rye"}, needBy)
	if !got.MashesAvailable {
		t.Fatalf("refused: %s", got.MashesMissing)
	}
	if got.Mashes != 3 {
		t.Errorf("mashes: got %d, want 3 — 2.4 batches does not fit in two", got.Mashes)
	}

	// A tun that holds two batches: the same requirement is two mashes.
	big := Plan(aReq(), &Plant{Name: "Big tun", CapacityL: 2000, BatchVolumeL: 1000},
		Lead{MaxDays: 14, TotalLines: 2}, needBy)
	if big.Mashes != 2 {
		t.Errorf("larger tun: got %d mashes, want 2", big.Mashes)
	}
}

// No stated vessel refuses rather than assuming one. Picking the largest
// would be right at a distillery with one tun and wrong at any that has
// reason to own two.
func TestPlan_NoVesselRefusesTheMashCount(t *testing.T) {
	got := Plan(aReq(), nil, Lead{MaxDays: 14, TotalLines: 2}, needBy)
	if got.MashesAvailable {
		t.Fatalf("counted %d mashes with no vessel stated", got.Mashes)
	}
	if got.Mashes != 0 {
		t.Errorf("refused but reported %d", got.Mashes)
	}
	if !strings.Contains(got.MashesMissing, "larger one") {
		t.Errorf("refusal does not explain: %q", got.MashesMissing)
	}
	// The order-by half is independent and still works.
	if !got.OrderByAvailable {
		t.Errorf("a missing vessel also killed the order date: %s", got.OrderByMissing)
	}
}

// The order date counts back from when the materials are needed, and the
// longest lead time sets it — an order arrives when its slowest line does.
func TestPlan_OrderByCountsBackFromTheSlowestLine(t *testing.T) {
	got := Plan(aReq(), &Plant{Name: "T", CapacityL: 1000, BatchVolumeL: 1000},
		Lead{MaxDays: 21, TotalLines: 3, Slowest: "Malted barley"}, needBy)
	if !got.OrderByAvailable {
		t.Fatalf("refused: %s", got.OrderByMissing)
	}
	if got.OrderBy != "2026-08-11" {
		t.Errorf("order by: got %s, want 2026-08-11 (21 days before 1 September)", got.OrderBy)
	}
}

// A bill where some lines have no lead time gives a date that is only
// right for the rest, and a date somebody trusts is worse than none.
func TestPlan_PartialLeadTimesRefuseTheDate(t *testing.T) {
	got := Plan(aReq(), &Plant{Name: "T", CapacityL: 1000, BatchVolumeL: 1000},
		Lead{MaxDays: 21, TotalLines: 4, WithoutLeadTime: 2}, needBy)
	if got.OrderByAvailable {
		t.Fatalf("gave the date %s over a bill half of which has no lead time", got.OrderBy)
	}
	if !strings.Contains(got.OrderByMissing, "2 of 4") {
		t.Errorf("refusal does not say how many: %q", got.OrderByMissing)
	}
	// And the mash half is independent.
	if !got.MashesAvailable {
		t.Errorf("missing lead times also killed the mash count: %s", got.MashesMissing)
	}
}

// A vessel with no capacity recorded cannot say how many mashes anything
// comes to, and dividing by it would produce an infinity.
func TestPlan_VesselWithoutCapacityRefuses(t *testing.T) {
	got := Plan(aReq(), &Plant{Name: "Unmeasured tun", BatchVolumeL: 1000},
		Lead{MaxDays: 7, TotalLines: 1}, needBy)
	if got.MashesAvailable {
		t.Fatalf("counted %d mashes against a vessel with no capacity", got.Mashes)
	}
	if !strings.Contains(got.MashesMissing, "no capacity") {
		t.Errorf("refusal: %q", got.MashesMissing)
	}
}
