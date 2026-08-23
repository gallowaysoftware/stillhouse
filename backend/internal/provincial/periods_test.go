package provincial

import (
	"testing"
	"time"

	"github.com/gallowaysoftware/stillhouse/backend/internal/filing"
)

func d(y int, m time.Month, day int) time.Time {
	return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
}

func iso(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

var calendar = filing.Basis{Frequency: filing.Monthly, MonthBasis: filing.CalendarMonth}

func TestCalendarMonths(t *testing.T) {
	ps, err := Periods(Monthly, calendar, false, d(2026, time.January, 15), d(2026, time.April, 10), 20)
	if err != nil {
		t.Fatalf("Periods: %v", err)
	}
	// The January period ends on the 31st, which is at or after the 15th,
	// so it is included; April's end is after the 10th, so it is not.
	want := [][3]string{
		{"2026-01-01", "2026-01-31", "2026-02-20"},
		{"2026-02-01", "2026-02-28", "2026-03-20"},
		{"2026-03-01", "2026-03-31", "2026-04-20"},
	}
	assertPeriods(t, ps, want)
}

func TestCalendarQuartersAnchorOnTheYear(t *testing.T) {
	// Not "three months back from today" — a quarter is a quarter, and a
	// calendar that moved every time it was asked would not be one.
	ps, err := Periods(Quarterly, calendar, false, d(2026, time.February, 1), d(2026, time.December, 31), 30)
	if err != nil {
		t.Fatalf("Periods: %v", err)
	}
	want := [][3]string{
		{"2026-01-01", "2026-03-31", "2026-04-30"},
		{"2026-04-01", "2026-06-30", "2026-07-30"},
		{"2026-07-01", "2026-09-30", "2026-10-30"},
		{"2026-10-01", "2026-12-31", "2027-01-30"},
	}
	assertPeriods(t, ps, want)
}

func TestCalendarHalvesAndYears(t *testing.T) {
	ps, err := Periods(SemiAnnual, calendar, false, d(2026, time.January, 1), d(2026, time.December, 31), -1)
	if err != nil {
		t.Fatalf("Periods: %v", err)
	}
	assertPeriods(t, ps, [][3]string{
		{"2026-01-01", "2026-06-30", ""},
		{"2026-07-01", "2026-12-31", ""},
	})

	ps, err = Periods(Annual, calendar, false, d(2026, time.March, 1), d(2027, time.June, 1), 90)
	if err != nil {
		t.Fatalf("Periods: %v", err)
	}
	assertPeriods(t, ps, [][3]string{
		{"2026-01-01", "2026-12-31", "2027-03-31"},
	})
}

// The whole reason the flag exists: a province wanting calendar months
// while the licensee files excise on a fiscal month ending on the 25th is
// the ordinary case, and assuming they agree is how a period gets
// reported twice.
func TestExciseClockDiffersFromTheCalendar(t *testing.T) {
	fiscal := filing.Basis{
		Frequency: filing.Monthly, MonthBasis: filing.FixedDayOfMonth, MonthEndDay: 25,
	}
	onClock, err := Periods(Monthly, fiscal, true, d(2026, time.January, 1), d(2026, time.March, 31), 15)
	if err != nil {
		t.Fatalf("Periods: %v", err)
	}
	offClock, err := Periods(Monthly, fiscal, false, d(2026, time.January, 1), d(2026, time.March, 31), 15)
	if err != nil {
		t.Fatalf("Periods: %v", err)
	}
	if len(onClock) == 0 || len(offClock) == 0 {
		t.Fatal("no periods generated")
	}
	if iso(onClock[0].End) == iso(offClock[0].End) {
		t.Errorf("a fiscal month ending on the 25th produced the same boundary as "+
			"the calendar (%s) — the flag does nothing", iso(onClock[0].End))
	}
	if got, want := iso(onClock[0].End), "2026-01-25"; got != want {
		t.Errorf("excise-clock period end = %s, want %s", got, want)
	}
	if got, want := iso(offClock[0].End), "2026-01-31"; got != want {
		t.Errorf("calendar period end = %s, want %s", got, want)
	}
}

// Consecutive periods must abut exactly: a gap loses a day's sales and an
// overlap reports it twice.
func TestPeriodsAbut(t *testing.T) {
	for _, c := range []Cadence{Monthly, Quarterly, SemiAnnual, Annual} {
		for _, exciseClock := range []bool{false, true} {
			basis := filing.Basis{
				Frequency: filing.Monthly, MonthBasis: filing.FixedDayOfMonth, MonthEndDay: 25,
			}
			ps, err := Periods(c, basis, exciseClock, d(2025, time.January, 1), d(2028, time.December, 31), 10)
			if err != nil {
				t.Fatalf("Periods: %v", err)
			}
			if len(ps) < 2 {
				t.Fatalf("cadence %d produced %d periods", c, len(ps))
			}
			for i := 1; i < len(ps); i++ {
				want := ps[i-1].End.AddDate(0, 0, 1)
				if !ps[i].Start.Equal(want) {
					t.Errorf("cadence %d exciseClock=%v: %s follows %s — want a start of %s",
						c, exciseClock, iso(ps[i].Start), iso(ps[i-1].End), iso(want))
				}
			}
		}
	}
}

// A definition with no recorded due-days produces periods with no due
// date, which is never overdue. Inventing the deadline would be worse
// than having none, because it would look like one.
func TestNoDueDaysMeansNoDueDate(t *testing.T) {
	ps, err := Periods(Monthly, calendar, false, d(2026, time.January, 1), d(2026, time.February, 28), -1)
	if err != nil {
		t.Fatalf("Periods: %v", err)
	}
	for _, p := range ps {
		if !p.DueOn.IsZero() {
			t.Errorf("period ending %s got a due date of %s from nowhere",
				iso(p.End), iso(p.DueOn))
		}
	}
}

func TestCadencesWithNoPeriod(t *testing.T) {
	for _, c := range []Cadence{PerShipment, Other} {
		if _, err := Periods(c, calendar, false, d(2026, time.January, 1), d(2026, time.June, 30), 10); err == nil {
			t.Errorf("cadence %d produced periods it has no boundaries for", c)
		}
	}
}

func TestBackwardsRangeIsRefused(t *testing.T) {
	if _, err := Periods(Monthly, calendar, false, d(2026, time.June, 1), d(2026, time.January, 1), 10); err == nil {
		t.Error("a range ending before it starts produced periods")
	}
}

// February, and a leap year, which is where month arithmetic usually goes
// wrong.
func TestFebruary(t *testing.T) {
	ps, err := Periods(Monthly, calendar, false, d(2028, time.February, 1), d(2028, time.February, 29), 0)
	if err != nil {
		t.Fatalf("Periods: %v", err)
	}
	assertPeriods(t, ps, [][3]string{{"2028-02-01", "2028-02-29", "2028-02-29"}})
}

func assertPeriods(t *testing.T, got []Period, want [][3]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d periods, want %d: %v", len(got), len(want), format(got))
	}
	for i := range want {
		g := [3]string{iso(got[i].Start), iso(got[i].End), iso(got[i].DueOn)}
		if g != want[i] {
			t.Errorf("period %d = %v, want %v", i, g, want[i])
		}
	}
}

func format(ps []Period) [][3]string {
	out := make([][3]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, [3]string{iso(p.Start), iso(p.End), iso(p.DueOn)})
	}
	return out
}
