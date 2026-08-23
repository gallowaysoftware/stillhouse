// Package forecast projects demand from what actually happened.
//
// Every function here is pure. The point of that is not tidiness: a
// forecast is a number somebody will plan production against, and the
// arithmetic behind it has to be checkable without standing up a
// database and a year of removals.
package forecast

import (
	"fmt"
	"math"
	"time"
)

// Method is how a projection is made. There is no default and no
// fallback: which one is right depends on whether a distillery's sales
// are trending or seasonal, which Stillhouse cannot see and the operator
// can.
type Method string

const (
	TrailingAverage    Method = "trailing_average"
	SamePeriodLastYear Method = "same_period_last_year"
	Manual             Method = "manual"
)

// Observation is one month of actual removals for one product.
type Observation struct {
	Month   time.Time // first of the month
	Bottles int32
}

// Result is a projection, or a refusal to make one.
//
// Available is false when the history cannot support the method asked
// for. That is a refusal, not a zero: a product with no sales history
// and a product forecast to sell nothing are different claims, and a
// production plan built on the second when the first is true would
// under-produce for a reason nobody could see.
type Result struct {
	Bottles   int32
	Available bool
	// Method and Basis travel with the number, so the figure can be
	// argued with rather than merely believed.
	Method Method
	Basis  string
	// Why not, when Available is false.
	Missing string
	// How many months of history went into it. Reported because a
	// three-month average over two months is a different claim from one
	// over three, and the operator should see which they have.
	MonthsUsed int32
}

// Project makes one product's projection for the month starting at `for_`.
//
// history must be sorted ascending and contain only complete months. It
// may be sparse: a month with no removals is a month with no removals,
// and this treats a missing month as zero rather than skipping it, since
// skipping would turn three months of history with one dry month into a
// two-month average that reads higher than the truth.
func Project(m Method, history []Observation, for_ time.Time, trailingMonths int32) Result {
	switch m {
	case TrailingAverage:
		return trailing(history, for_, trailingMonths)
	case SamePeriodLastYear:
		return seasonal(history, for_)
	case Manual:
		return Result{
			Method:  Manual,
			Missing: "the manual method takes its numbers from what the operator entered; nothing was entered for this product and period.",
		}
	}
	return Result{Missing: "no forecast method has been set"}
}

func trailing(history []Observation, for_ time.Time, months int32) Result {
	if months <= 0 {
		months = 3
	}
	res := Result{Method: TrailingAverage}

	// The window is the `months` complete months immediately before the
	// month being forecast.
	first := monthStart(for_).AddDate(0, -int(months), 0)
	last := monthStart(for_)

	var sum, n int32
	for _, o := range history {
		mo := monthStart(o.Month)
		if !mo.Before(first) && mo.Before(last) {
			sum += o.Bottles
			n++
		}
	}
	// Months inside the window with no removals are real zeros and are
	// counted: a dry month is information, and dropping it would inflate
	// the average.
	window := months
	if n == 0 {
		res.Missing = fmt.Sprintf(
			"no duty-paid removals for this product in the %d month(s) before %s, so there is nothing to average.",
			months, last.Format("January 2006"))
		return res
	}
	res.MonthsUsed = n
	res.Bottles = int32(float64(sum)/float64(window) + 0.5)
	res.Available = true
	res.Basis = fmt.Sprintf(
		"mean of %d complete month(s) to %s: %d bottles removed duty-paid over %d month(s)",
		window, last.AddDate(0, 0, -1).Format("2 January 2006"), sum, window)
	return res
}

func seasonal(history []Observation, for_ time.Time) Result {
	res := Result{Method: SamePeriodLastYear}
	want := monthStart(for_).AddDate(-1, 0, 0)
	for _, o := range history {
		if monthStart(o.Month).Equal(want) {
			res.Bottles = o.Bottles
			res.Available = true
			res.MonthsUsed = 1
			res.Basis = fmt.Sprintf("%d bottles removed duty-paid in %s",
				o.Bottles, want.Format("January 2006"))
			return res
		}
	}
	// Not zero. A year with no record is not a year with no sales, and
	// the difference decides whether somebody produces.
	res.Missing = fmt.Sprintf(
		"no record of %s, so there is no same-month-last-year to project from. A first year has none by definition.",
		want.Format("January 2006"))
	return res
}

func monthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// Requirement is what a forecast implies has to be made and bought.
//
// Every field can be unavailable independently, and the reasons differ:
// the alcohol figure needs only the product's own size and strength,
// while the grain figure needs a recipe somebody has linked. Reporting
// them as one availability would hide a usable answer behind a missing
// one.
type Requirement struct {
	BottlesToMake int32
	// LAA the bottles would contain. Exact arithmetic on the product's
	// own size and strength, so it is available whenever the forecast is.
	LAANeeded float64

	// Grain, through the recipe. Available only where a recipe is linked
	// and its projection produces alcohol.
	GrainAvailable bool
	GrainMissing   string
	// Scale is how many of the recipe's own batches the requirement comes
	// to. Reported because it is the number an operator reasons in — "two
	// and a bit mashes" — and because it makes the linearity assumption
	// visible rather than buried.
	Batches    float64
	GrainLines []GrainLine
}

// GrainLine is one material and how much of it.
type GrainLine struct {
	Material string
	Quantity float64
	UOM      string
}

// RecipeBatch is a linked recipe reduced to what scaling needs: the
// alcohol one batch of it projects, and the bill that produced that.
type RecipeBatch struct {
	Name         string
	ProjectedLAA float64
	Ingredients  []GrainLine
}

// Require turns a forecast into a requirement.
//
// The grain figure scales the recipe linearly, and that is an assumption
// rather than a fact: a mash tun has a size, and three batches of a
// recipe are three mashes rather than one large one. Batches is reported
// so the assumption is visible — an operator who sees 2.4 knows to round
// it to three mashes, which no amount of arithmetic here could decide
// for them.
func Require(forecastBottles, onHand int32, bottleSizeML int32, abvPct float64, r *RecipeBatch) Requirement {
	req := Requirement{}
	toMake := forecastBottles - onHand
	if toMake < 0 {
		toMake = 0
	}
	req.BottlesToMake = toMake
	req.LAANeeded = float64(toMake) * float64(bottleSizeML) / 1000 * abvPct / 100

	switch {
	case r == nil:
		req.GrainMissing = "no recipe is linked to this product, so the materials cannot be worked out. Link one on the product."
		return req
	case r.ProjectedLAA <= 0:
		req.GrainMissing = fmt.Sprintf(
			"%s projects no alcohol — check its grain bill and efficiencies — so there is nothing to scale.", r.Name)
		return req
	case req.LAANeeded <= 0:
		req.GrainMissing = "stock on hand already covers the forecast, so nothing needs making."
		return req
	}

	req.Batches = req.LAANeeded / r.ProjectedLAA
	req.GrainAvailable = true
	for _, in := range r.Ingredients {
		req.GrainLines = append(req.GrainLines, GrainLine{
			Material: in.Material,
			Quantity: roundTo(in.Quantity*req.Batches, 3),
			UOM:      in.UOM,
		})
	}
	req.Batches = roundTo(req.Batches, 2)
	return req
}

func roundTo(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
