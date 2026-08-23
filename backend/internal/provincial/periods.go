// Package provincial computes provincial reporting periods.
//
// Pure date arithmetic, deliberately, for the same reason internal/filing
// is: nothing here needs a database, so all of it can be exercised
// without one — and period boundaries are exactly the kind of thing that
// is obvious until February.
//
// What this package does NOT know is when anything is due. A due date is
// a fact about a particular board's rules, and Stillhouse does not ship
// other people's deadlines from memory; the licensee records how many
// days after period end their report is owed, and a definition with none
// recorded produces periods with no due date rather than a guessed one.
package provincial

import (
	"fmt"
	"time"

	"github.com/gallowaysoftware/stillhouse/backend/internal/filing"
)

// Cadence is how often a report is owed.
type Cadence int

const (
	Monthly Cadence = iota
	Quarterly
	SemiAnnual
	Annual
	// PerShipment and Other have no period boundaries to compute. They
	// are legitimate — a board that wants a form with every delivery is
	// not unusual — and asking for periods on one is answered with a
	// refusal rather than an empty list, so the caller can say why.
	PerShipment
	Other
)

// Months is how many months one period spans.
func (c Cadence) Months() (int, error) {
	switch c {
	case Monthly:
		return 1, nil
	case Quarterly:
		return 3, nil
	case SemiAnnual:
		return 6, nil
	case Annual:
		return 12, nil
	case PerShipment:
		return 0, fmt.Errorf("a per-shipment report has no reporting period — " +
			"it is filed with the delivery")
	default:
		return 0, fmt.Errorf("this report's cadence has no fixed period; record " +
			"the periods you owe by hand")
	}
}

// Period is one provincial reporting period.
type Period struct {
	Start time.Time
	End   time.Time
	// DueOn is zero when the definition records no due-days. A zero due
	// date is never overdue: inventing the deadline would be worse than
	// having none, because it would look like one.
	DueOn time.Time
}

// Periods returns every period of the given cadence whose end falls
// within [from, to].
//
// followsExciseClock decides which calendar the boundaries come from. A
// province that wants calendar months while the licensee files excise on
// a fiscal month ending on the 25th is the ordinary case, not the
// exception, and quietly assuming they agree is how a period gets
// reported twice — or missed.
//
// dueDays is days after period end; negative means the licensee has
// recorded none.
func Periods(
	c Cadence, b filing.Basis, followsExciseClock bool,
	from, to time.Time, dueDays int,
) ([]Period, error) {
	months, err := c.Months()
	if err != nil {
		return nil, err
	}
	from, to = day(from), day(to)
	if to.Before(from) {
		return nil, fmt.Errorf("the range ends before it starts")
	}

	var out []Period
	// Walk period ends forward from the first one at or after `from`.
	// Anchoring on ends rather than starts is what keeps a quarter a
	// quarter when the fiscal month does not begin on the first.
	end := firstEndOnOrAfter(c, b, followsExciseClock, from, months)
	for !end.After(to) {
		start := startFor(b, followsExciseClock, end, months)
		p := Period{Start: start, End: end}
		if dueDays >= 0 {
			p.DueOn = end.AddDate(0, 0, dueDays)
		}
		out = append(out, p)
		end = nextEnd(b, followsExciseClock, end, months)
		// A cadence of zero months is impossible here (Months would have
		// errored), so the walk always advances.
	}
	return out, nil
}

// firstEndOnOrAfter finds the first period end at or after t.
//
// Calendar periods are anchored on the calendar year — quarters end in
// March, June, September and December, halves in June and December —
// rather than counted back from today, which would give a different
// answer every time it was asked.
func firstEndOnOrAfter(
	c Cadence, b filing.Basis, exciseClock bool, t time.Time, months int,
) time.Time {
	end := monthEndOnOrAfter(b, exciseClock, t)
	if months == 1 {
		return end
	}
	// Advance until the period ending here is aligned to the year. The
	// month a period ends in decides which slot it fills.
	for (int(end.Month())-1)%months != months-1 {
		end = nextMonthEnd(b, exciseClock, end)
	}
	return end
}

func startFor(b filing.Basis, exciseClock bool, end time.Time, months int) time.Time {
	start := monthStartFor(b, exciseClock, end)
	for i := 1; i < months; i++ {
		start = monthStartFor(b, exciseClock, day(start).AddDate(0, 0, -1))
	}
	return start
}

func nextEnd(b filing.Basis, exciseClock bool, end time.Time, months int) time.Time {
	for i := 0; i < months; i++ {
		end = nextMonthEnd(b, exciseClock, end)
	}
	return end
}

// The three month-boundary primitives, switched by which calendar
// applies. The excise-clock versions are filing's, so a definition that
// follows the excise clock lands on exactly the same boundaries the B266
// does — which is the point of the flag.
func monthEndOnOrAfter(b filing.Basis, exciseClock bool, t time.Time) time.Time {
	if exciseClock {
		return b.MonthEndOnOrAfter(t)
	}
	return lastDayOfMonth(t)
}

func monthStartFor(b filing.Basis, exciseClock bool, monthEnd time.Time) time.Time {
	if exciseClock {
		return b.MonthStartFor(monthEnd)
	}
	return time.Date(monthEnd.Year(), monthEnd.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func nextMonthEnd(b filing.Basis, exciseClock bool, monthEnd time.Time) time.Time {
	if exciseClock {
		return b.NextMonthEnd(monthEnd)
	}
	return lastDayOfMonth(day(monthEnd).AddDate(0, 0, 1))
}

func day(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// lastDayOfMonth via the first of the next month less a day, so February
// and leap years look after themselves.
func lastDayOfMonth(t time.Time) time.Time {
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	return first.AddDate(0, 1, -1)
}
