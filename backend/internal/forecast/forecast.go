// Package forecast projects demand from what actually happened.
//
// Every function here is pure. The point of that is not tidiness: a
// forecast is a number somebody will plan production against, and the
// arithmetic behind it has to be checkable without standing up a
// database and a year of removals.
package forecast

import (
	"fmt"
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
