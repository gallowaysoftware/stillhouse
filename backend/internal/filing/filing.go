// Package filing computes the reporting calendar: when a fiscal month
// ends, which period comes next, and when its return is due.
//
// Stillhouse assumed a calendar month and derived nothing from it. Under
// EDM3-1-1 ¶50 neither assumption holds. A fiscal month is set by
// notification (form B268) rather than assumed — a licensee may elect
// fiscal months ending on a fixed day — and an authorized licensee may
// file semi-annually (form B284) rather than monthly. The due date, the
// last day of the fiscal month following the reporting period, existed
// nowhere at all.
//
// Pure arithmetic over dates, deliberately: this is the same lesson as the
// B266 projection in stage 139. Nothing here needs a database, so all of it
// can be exercised without one.
package filing

import (
	"fmt"
	"time"
)

// Frequency is how often the licensee files.
type Frequency int

const (
	Monthly Frequency = iota
	SemiAnnual
)

// MonthBasis is how the licensee's fiscal months are defined.
type MonthBasis int

const (
	// CalendarMonth: fiscal months are calendar months. The default, and
	// what every tenant was implicitly on.
	CalendarMonth MonthBasis = iota
	// FixedDayOfMonth: fiscal months end on a nominated day — the 25th,
	// say — so a fiscal month runs from the 26th to the 25th.
	FixedDayOfMonth
)

// Basis is one licensee's reporting calendar.
type Basis struct {
	Frequency  Frequency
	MonthBasis MonthBasis
	// MonthEndDay is the day fiscal months end on, when MonthBasis is
	// FixedDayOfMonth. Between 1 and 28: a fiscal month ending on the 30th
	// has no February.
	MonthEndDay int
}

// monthsPerPeriod is how many fiscal months one reporting period spans.
func (b Basis) monthsPerPeriod() int {
	if b.Frequency == SemiAnnual {
		return 6
	}
	return 1
}

// Validate reports whether the basis is internally coherent.
func (b Basis) Validate() error {
	if b.MonthBasis == FixedDayOfMonth && (b.MonthEndDay < 1 || b.MonthEndDay > 28) {
		return fmt.Errorf("a fiscal month ending on day %d cannot be applied to every month; elect a day between 1 and 28", b.MonthEndDay)
	}
	return nil
}

func day(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// lastDayOfMonth returns the final calendar day of t's month. Built by
// stepping to the first of the next month and back one day, which is the
// only spelling that gets February right in a leap year without a table.
func lastDayOfMonth(t time.Time) time.Time {
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	return first.AddDate(0, 1, 0).AddDate(0, 0, -1)
}

// MonthEndOnOrAfter returns the end of the fiscal month that contains the
// given date.
func (b Basis) MonthEndOnOrAfter(t time.Time) time.Time {
	t = day(t)
	if b.MonthBasis == CalendarMonth {
		return lastDayOfMonth(t)
	}
	// A fiscal month ending on the Nth runs from the (N+1)th of one month
	// to the Nth of the next, so a date on or before the Nth is still
	// inside this month's period.
	thisMonth := time.Date(t.Year(), t.Month(), b.MonthEndDay, 0, 0, 0, 0, time.UTC)
	if !t.After(thisMonth) {
		return thisMonth
	}
	return thisMonth.AddDate(0, 1, 0)
}

// MonthStartFor returns the first day of the fiscal month that ends on the
// given fiscal month end.
func (b Basis) MonthStartFor(monthEnd time.Time) time.Time {
	monthEnd = day(monthEnd)
	if b.MonthBasis == CalendarMonth {
		return time.Date(monthEnd.Year(), monthEnd.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return monthEnd.AddDate(0, -1, 0).AddDate(0, 0, 1)
}

// NextMonthEnd returns the end of the fiscal month following the one that
// ends on monthEnd.
func (b Basis) NextMonthEnd(monthEnd time.Time) time.Time {
	return b.MonthEndOnOrAfter(day(monthEnd).AddDate(0, 0, 1))
}

// DueDate returns the day the return for a period ending on periodEnd must
// be filed: the last day of the fiscal month following the reporting
// period (EDM3-1-1 ¶50).
//
// Note it is the fiscal month *following the period*, not "one month
// later". For a semi-annual filer whose period ends 30 June, the return is
// due 31 July, not the following January — the frequency changes how long
// the period is, not how long there is to file it.
func (b Basis) DueDate(periodEnd time.Time) time.Time {
	return b.NextMonthEnd(b.MonthEndOnOrAfter(periodEnd))
}

// Period is one reporting period and the day its return falls due.
type Period struct {
	Start time.Time
	End   time.Time
	DueOn time.Time
}

// PeriodContaining returns the reporting period that the given date falls
// in, aligned to the licensee's fiscal calendar and frequency.
//
// Semi-annual periods are anchored on the calendar year: the first runs
// from the start of the fiscal month containing 1 January, the second from
// the seventh. Anchoring them on "six months back from today" instead
// would give a different answer every time it was asked, which is not a
// filing calendar.
func (b Basis) PeriodContaining(t time.Time) Period {
	end := b.MonthEndOnOrAfter(t)
	if b.Frequency == SemiAnnual {
		// Walk the fiscal month forward to the end of its half-year. The
		// half a fiscal month belongs to is decided by the month its
		// period ends in.
		for (int(end.Month())-1)%6 != 5 {
			end = b.NextMonthEnd(end)
		}
	}
	start := b.MonthStartFor(end)
	for i := 1; i < b.monthsPerPeriod(); i++ {
		start = b.MonthStartFor(day(start).AddDate(0, 0, -1))
	}
	return Period{Start: start, End: end, DueOn: b.DueDate(end)}
}

// NextPeriodAfter returns the reporting period that follows a period
// ending on the given date.
func (b Basis) NextPeriodAfter(periodEnd time.Time) Period {
	return b.PeriodContaining(day(periodEnd).AddDate(0, 0, 1))
}

// MatchesElection reports whether an arbitrary date range is exactly one
// reporting period on this basis, and if not, why not.
//
// Deliberately not a refusal at the call site: generating a draft over an
// odd range to look at the figures is legitimate, and a tool that refuses
// to show you a number until the dates are perfect is a tool people work
// around. It becomes a filing blocker instead — the period computes, and
// says it is not the one the licensee elected to file.
func (b Basis) MatchesElection(start, end time.Time) (bool, string) {
	want := b.PeriodContaining(end)
	if day(start).Equal(want.Start) && day(end).Equal(want.End) {
		return true, ""
	}
	freq := "monthly"
	if b.Frequency == SemiAnnual {
		freq = "semi-annual"
	}
	return false, fmt.Sprintf(
		"this period (%s → %s) is not a %s reporting period on your fiscal calendar; the period containing %s runs %s → %s",
		day(start).Format("2006-01-02"), day(end).Format("2006-01-02"), freq,
		day(end).Format("2006-01-02"),
		want.Start.Format("2006-01-02"), want.End.Format("2006-01-02"))
}
