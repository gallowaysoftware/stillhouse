package filing

import (
	"testing"
	"time"
)

// PLAN A6. Stillhouse assumed a calendar month and derived nothing from
// it. Under EDM3-1-1 ¶50 a fiscal month is set by notification (B268)
// rather than assumed, an authorized licensee may file semi-annually
// (B284), and the return is due by the last day of the fiscal month
// following the period — a date that existed nowhere in the model.

func d(y int, m time.Month, day int) time.Time {
	return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
}

var (
	monthly    = Basis{Frequency: Monthly, MonthBasis: CalendarMonth}
	semiAnnual = Basis{Frequency: SemiAnnual, MonthBasis: CalendarMonth}
	fixed25    = Basis{Frequency: Monthly, MonthBasis: FixedDayOfMonth, MonthEndDay: 25}
)

func TestMonthEndOnOrAfter(t *testing.T) {
	for _, tc := range []struct {
		name  string
		basis Basis
		in    time.Time
		want  time.Time
	}{
		{"mid calendar month", monthly, d(2026, 6, 12), d(2026, 6, 30)},
		{"the last day is its own month end", monthly, d(2026, 6, 30), d(2026, 6, 30)},
		{"the first day", monthly, d(2026, 6, 1), d(2026, 6, 30)},
		// February in a leap year is the case a hand-written table gets
		// wrong. 2028 is one.
		{"February, ordinary year", monthly, d(2026, 2, 10), d(2026, 2, 28)},
		{"February, leap year", monthly, d(2028, 2, 10), d(2028, 2, 29)},

		{"before a fixed day end", fixed25, d(2026, 6, 12), d(2026, 6, 25)},
		{"on a fixed day end", fixed25, d(2026, 6, 25), d(2026, 6, 25)},
		// The day after the 25th is inside the NEXT fiscal month, which is
		// the whole point of electing one.
		{"after a fixed day end", fixed25, d(2026, 6, 26), d(2026, 7, 25)},
		{"the last day of a calendar month, fixed basis", fixed25, d(2026, 6, 30), d(2026, 7, 25)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.basis.MonthEndOnOrAfter(tc.in); !got.Equal(tc.want) {
				t.Errorf("got %s, want %s", got.Format("2006-01-02"), tc.want.Format("2006-01-02"))
			}
		})
	}
}

// The due date is the last day of the fiscal month FOLLOWING the period —
// not "one month later", and not a function of how often the licensee
// files. The frequency changes how long the period is, not how long there
// is to file it.
func TestDueDate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		basis     Basis
		periodEnd time.Time
		want      time.Time
	}{
		{"a calendar month", monthly, d(2026, 6, 30), d(2026, 7, 31)},
		{"December rolls the year", monthly, d(2026, 12, 31), d(2027, 1, 31)},
		{"January is due at the end of February", monthly, d(2026, 1, 31), d(2026, 2, 28)},
		{"January in a leap year", monthly, d(2028, 1, 31), d(2028, 2, 29)},
		// A semi-annual filer's period is six months long and still has one
		// fiscal month to file.
		{"semi-annual, first half", semiAnnual, d(2026, 6, 30), d(2026, 7, 31)},
		{"semi-annual, second half", semiAnnual, d(2026, 12, 31), d(2027, 1, 31)},
		{"a fixed fiscal month", fixed25, d(2026, 6, 25), d(2026, 7, 25)},
		{"a fixed fiscal month across the year", fixed25, d(2026, 12, 25), d(2027, 1, 25)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.basis.DueDate(tc.periodEnd); !got.Equal(tc.want) {
				t.Errorf("got %s, want %s", got.Format("2006-01-02"), tc.want.Format("2006-01-02"))
			}
		})
	}
}

func TestPeriodContaining(t *testing.T) {
	for _, tc := range []struct {
		name            string
		basis           Basis
		in              time.Time
		start, end, due time.Time
	}{
		{"a calendar month", monthly, d(2026, 6, 12),
			d(2026, 6, 1), d(2026, 6, 30), d(2026, 7, 31)},
		{"a fixed fiscal month runs 26th to 25th", fixed25, d(2026, 6, 12),
			d(2026, 5, 26), d(2026, 6, 25), d(2026, 7, 25)},
		{"the day after a fixed month end starts the next", fixed25, d(2026, 6, 26),
			d(2026, 6, 26), d(2026, 7, 25), d(2026, 8, 25)},
		// Semi-annual periods are anchored on the calendar year, so the
		// answer does not depend on the day it was asked.
		{"semi-annual, first half", semiAnnual, d(2026, 3, 4),
			d(2026, 1, 1), d(2026, 6, 30), d(2026, 7, 31)},
		{"semi-annual, second half", semiAnnual, d(2026, 9, 17),
			d(2026, 7, 1), d(2026, 12, 31), d(2027, 1, 31)},
		{"semi-annual, the last day of a half", semiAnnual, d(2026, 6, 30),
			d(2026, 1, 1), d(2026, 6, 30), d(2026, 7, 31)},
		{"semi-annual, the first day of a half", semiAnnual, d(2026, 7, 1),
			d(2026, 7, 1), d(2026, 12, 31), d(2027, 1, 31)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.basis.PeriodContaining(tc.in)
			if !got.Start.Equal(tc.start) || !got.End.Equal(tc.end) || !got.DueOn.Equal(tc.due) {
				t.Errorf("got %s → %s due %s, want %s → %s due %s",
					got.Start.Format("2006-01-02"), got.End.Format("2006-01-02"), got.DueOn.Format("2006-01-02"),
					tc.start.Format("2006-01-02"), tc.end.Format("2006-01-02"), tc.due.Format("2006-01-02"))
			}
		})
	}
}

// Periods must abut exactly and never overlap: a day that falls in two
// periods is reported twice, which is the defect stage 134 fixed at the
// database level. Here it is the calendar's job.
func TestConsecutivePeriodsAbutWithNoGapOrOverlap(t *testing.T) {
	for _, basis := range []Basis{monthly, semiAnnual, fixed25} {
		p := basis.PeriodContaining(d(2026, 1, 15))
		for i := 0; i < 30; i++ {
			next := basis.NextPeriodAfter(p.End)
			if want := p.End.AddDate(0, 0, 1); !next.Start.Equal(want) {
				t.Fatalf("%+v: period after %s starts %s, want %s — a gap or an overlap",
					basis, p.End.Format("2006-01-02"),
					next.Start.Format("2006-01-02"), want.Format("2006-01-02"))
			}
			if !next.End.After(next.Start) {
				t.Fatalf("%+v: period %s → %s does not move forward",
					basis, next.Start.Format("2006-01-02"), next.End.Format("2006-01-02"))
			}
			// Every day in the period must resolve back to it, or the
			// calendar disagrees with itself.
			for _, probe := range []time.Time{next.Start, next.End,
				next.Start.AddDate(0, 0, (int(next.End.Sub(next.Start).Hours()/24))/2)} {
				back := basis.PeriodContaining(probe)
				if !back.Start.Equal(next.Start) || !back.End.Equal(next.End) {
					t.Fatalf("%+v: %s resolves to %s → %s but sits inside %s → %s",
						basis, probe.Format("2006-01-02"),
						back.Start.Format("2006-01-02"), back.End.Format("2006-01-02"),
						next.Start.Format("2006-01-02"), next.End.Format("2006-01-02"))
				}
			}
			p = next
		}
	}
}

// A range that is not the licensee's elected period still computes — a
// draft over an odd range is legitimate — but says so.
func TestMatchesElection(t *testing.T) {
	if ok, why := monthly.MatchesElection(d(2026, 6, 1), d(2026, 6, 30)); !ok {
		t.Errorf("a calendar month was rejected for a monthly filer: %s", why)
	}
	ok, why := monthly.MatchesElection(d(2026, 6, 1), d(2026, 6, 15))
	if ok {
		t.Error("half a month was accepted as a monthly reporting period")
	}
	if why == "" {
		t.Error("a mismatch gave no explanation")
	}
	// The message has to name the period the licensee should have used,
	// not merely say no.
	for _, want := range []string{"2026-06-01", "2026-06-30", "monthly"} {
		if !contains(why, want) {
			t.Errorf("explanation %q does not mention %q", why, want)
		}
	}
	// A calendar month is NOT a semi-annual period.
	if ok, _ := semiAnnual.MatchesElection(d(2026, 6, 1), d(2026, 6, 30)); ok {
		t.Error("a single month was accepted as a semi-annual reporting period")
	}
	if ok, _ := semiAnnual.MatchesElection(d(2026, 1, 1), d(2026, 6, 30)); !ok {
		t.Error("a half-year was rejected for a semi-annual filer")
	}
}

// A fiscal month cannot end on a day that some months do not have.
func TestValidate(t *testing.T) {
	if err := monthly.Validate(); err != nil {
		t.Errorf("the calendar-month default is invalid: %v", err)
	}
	if err := fixed25.Validate(); err != nil {
		t.Errorf("a 25th-of-the-month election is invalid: %v", err)
	}
	for _, bad := range []int{0, 29, 30, 31} {
		b := Basis{MonthBasis: FixedDayOfMonth, MonthEndDay: bad}
		if err := b.Validate(); err == nil {
			t.Errorf("day %d was accepted as a fiscal month end", bad)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return len(sub) == 0
}
