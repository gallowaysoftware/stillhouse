package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/gallowaysoftware/stillhouse/backend/internal/excise"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// PLAN A6, at the handler. The calendar arithmetic itself is covered in
// internal/filing without a database; what is tested here is that a
// licensee's election reaches the return.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

func newCalendarFixture(t *testing.T) (*dutyFixture, *TenantService, *B266Service) {
	t.Helper()
	f := newDutyFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	return f,
		NewTenantService(f.pool, f.q, log),
		NewB266Service(f.db, log)
}

// The default is what every tenant was implicitly on, and it must not
// change under them.
func TestNewTenantsFileMonthlyOnCalendarMonths(t *testing.T) {
	f, tenants, _ := newCalendarFixture(t)
	got, err := tenants.GetTenant(f.ctx, connect.NewRequest(&stillhousev1.GetTenantRequest{}))
	if err != nil {
		t.Fatalf("GetTenant: %v", err)
	}
	tn := got.Msg.GetTenant()
	if tn.GetFilingFrequency() != stillhousev1.FilingFrequency_FILING_FREQUENCY_MONTHLY {
		t.Errorf("filing frequency: got %v, want monthly", tn.GetFilingFrequency())
	}
	if tn.GetFiscalMonthBasis() != stillhousev1.FiscalMonthBasis_FISCAL_MONTH_BASIS_CALENDAR_MONTH {
		t.Errorf("fiscal month basis: got %v, want calendar month", tn.GetFiscalMonthBasis())
	}
}

// The due date is on the return and on the period row, and it is the last
// day of the fiscal month following the period (EDM3-1-1 ¶50).
func TestTheReturnCarriesItsDueDate(t *testing.T) {
	f, _, b266 := newCalendarFixture(t)

	resp, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	if got, want := resp.Msg.GetReport().GetDueOn(), "2026-07-31"; got != want {
		t.Errorf("due on the report: got %q, want %q", got, want)
	}
	if got, want := resp.Msg.GetPeriod().GetDueOn(), "2026-07-31"; got != want {
		t.Errorf("due on the period row: got %q, want %q", got, want)
	}
	// The period was months ago, so the return is overdue and says so
	// rather than counting up.
	if got := resp.Msg.GetReport().GetDaysUntilDue(); got >= 0 {
		t.Errorf("days until due: got %d, want negative for a period due in July 2026", got)
	}
}

// A fixed fiscal month reaches the return: a licensee who notified CRA
// that their months end on the 25th gets a due date on the 25th.
func TestAFixedFiscalMonthReachesTheReturn(t *testing.T) {
	f, tenants, b266 := newCalendarFixture(t)

	if _, err := tenants.UpdateFilingCalendar(f.ctx, connect.NewRequest(&stillhousev1.UpdateFilingCalendarRequest{
		FilingFrequency:            stillhousev1.FilingFrequency_FILING_FREQUENCY_MONTHLY,
		FiscalMonthBasis:           stillhousev1.FiscalMonthBasis_FISCAL_MONTH_BASIS_FIXED_DAY_OF_MONTH,
		FiscalMonthEndDay:          25,
		FiscalMonthNotificationRef: "B268 filed 2026-01-14",
	})); err != nil {
		t.Fatalf("UpdateFilingCalendar: %v", err)
	}

	resp, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-05-26", PeriodEnd: "2026-06-25",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	if got, want := resp.Msg.GetReport().GetDueOn(), "2026-07-25"; got != want {
		t.Errorf("due on: got %q, want %q", got, want)
	}
	// And a period matching the election raises no blocker about it.
	for _, b := range resp.Msg.GetReport().GetFilingBlockers() {
		if strings.Contains(b, "reporting period") {
			t.Errorf("a period matching the election was flagged: %q", b)
		}
	}
}

// A range that is not the elected period still computes — a draft over an
// odd range to look at the figures is legitimate — but says so.
func TestAPeriodOffTheElectionComputesAndSaysSo(t *testing.T) {
	f, _, b266 := newCalendarFixture(t)

	resp, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-06-01", PeriodEnd: "2026-06-15",
	}))
	if err != nil {
		t.Fatalf("half a month was refused rather than flagged: %v", err)
	}
	blockers := strings.Join(resp.Msg.GetReport().GetFilingBlockers(), " | ")
	if !strings.Contains(blockers, "reporting period") {
		t.Errorf("half a month raised no election blocker: %q", blockers)
	}
	// The message names the period they should have used, not merely "no".
	if !strings.Contains(blockers, "2026-06-30") {
		t.Errorf("the blocker does not name the right period: %q", blockers)
	}
}

// Electing semi-annual filing changes the period, not the time to file.
func TestSemiAnnualFilingChangesThePeriodNotTheDeadline(t *testing.T) {
	f, tenants, b266 := newCalendarFixture(t)

	if _, err := tenants.UpdateFilingCalendar(f.ctx, connect.NewRequest(&stillhousev1.UpdateFilingCalendarRequest{
		FilingFrequency:                 stillhousev1.FilingFrequency_FILING_FREQUENCY_SEMI_ANNUAL,
		FiscalMonthBasis:                stillhousev1.FiscalMonthBasis_FISCAL_MONTH_BASIS_CALENDAR_MONTH,
		FilingFrequencyAuthorizationRef: "B284 authorized 2026-02-02",
	})); err != nil {
		t.Fatalf("UpdateFilingCalendar: %v", err)
	}

	// The second half of 2026, which sits entirely inside the one rate
	// band the shipped table holds. The first half would span the
	// 2026-04-01 indexation AND start before the table begins — see
	// TestRateChangeNoteFor for that case, which cannot be built end to
	// end until the EDN history is seeded (PLAN A2).
	resp, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-07-01", PeriodEnd: "2026-12-31",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	// Six months of period, still one fiscal month to file.
	if got, want := resp.Msg.GetReport().GetDueOn(), "2027-01-31"; got != want {
		t.Errorf("due on: got %q, want %q", got, want)
	}
	for _, b := range resp.Msg.GetReport().GetFilingBlockers() {
		if strings.Contains(b, "reporting period") {
			t.Errorf("a half-year was flagged for a semi-annual filer: %q", b)
		}
	}

	// And a single month now IS off the election for this licensee.
	// January 2027, which neither overlaps the half-year above (stage 134
	// refuses that) nor leaves the one rate band the table holds.
	single, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2027-01-01", PeriodEnd: "2027-01-31",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	if !strings.Contains(strings.Join(single.Msg.GetReport().GetFilingBlockers(), " | "), "semi-annual") {
		t.Error("a single month was not flagged for a semi-annual filer")
	}
}

// The suggestion is the period that just closed, not the one running: a
// licensee in the first week of July is filing June, and suggesting July
// would offer a period whose figures are not final.
func TestSuggestGivesTheClosedPeriodNotTheRunningOne(t *testing.T) {
	f, _, b266 := newCalendarFixture(t)

	got, err := b266.SuggestB266Period(f.ctx, connect.NewRequest(&stillhousev1.SuggestB266PeriodRequest{
		On: "2026-07-04",
	}))
	if err != nil {
		t.Fatalf("SuggestB266Period: %v", err)
	}
	if s, e := got.Msg.GetPeriodStart(), got.Msg.GetPeriodEnd(); s != "2026-06-01" || e != "2026-06-30" {
		t.Errorf("suggested %s → %s on 4 July, want June", s, e)
	}
	if got, want := got.Msg.GetDueOn(), "2026-07-31"; got != want {
		t.Errorf("due on: got %q, want %q", got, want)
	}
	// Days remaining counts from the date asked about, so an operator can
	// see how long they have.
	if d := got.Msg.GetDaysUntilDue(); d != 27 {
		t.Errorf("days until due on 4 July for a 31 July deadline: got %d, want 27", d)
	}
	// And the period before it, for someone catching up.
	if s, e := got.Msg.GetPreviousPeriodStart(), got.Msg.GetPreviousPeriodEnd(); s != "2026-05-01" || e != "2026-05-31" {
		t.Errorf("previous period: got %s → %s, want May", s, e)
	}
}

// A fiscal month cannot end on a day that some months do not have.
func TestFilingCalendarRefusesAnImpossibleFiscalMonth(t *testing.T) {
	f, tenants, _ := newCalendarFixture(t)

	for _, day := range []int32{0, 29, 31} {
		_, err := tenants.UpdateFilingCalendar(f.ctx, connect.NewRequest(&stillhousev1.UpdateFilingCalendarRequest{
			FilingFrequency:   stillhousev1.FilingFrequency_FILING_FREQUENCY_MONTHLY,
			FiscalMonthBasis:  stillhousev1.FiscalMonthBasis_FISCAL_MONTH_BASIS_FIXED_DAY_OF_MONTH,
			FiscalMonthEndDay: day,
		}))
		if err == nil {
			t.Errorf("day %d was accepted as a fiscal month end", day)
			continue
		}
		if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
			t.Errorf("day %d: code = %v, want invalid_argument", day, got)
		}
	}
}

// A change of election must not restate when a past return was due. The
// date is frozen on the period row at first generation.
func TestChangingTheElectionDoesNotRestateAPastDueDate(t *testing.T) {
	f, tenants, b266 := newCalendarFixture(t)

	first, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	was := first.Msg.GetPeriod().GetDueOn()

	if _, err := tenants.UpdateFilingCalendar(f.ctx, connect.NewRequest(&stillhousev1.UpdateFilingCalendarRequest{
		FilingFrequency:   stillhousev1.FilingFrequency_FILING_FREQUENCY_MONTHLY,
		FiscalMonthBasis:  stillhousev1.FiscalMonthBasis_FISCAL_MONTH_BASIS_FIXED_DAY_OF_MONTH,
		FiscalMonthEndDay: 25,
	})); err != nil {
		t.Fatalf("UpdateFilingCalendar: %v", err)
	}

	again, err := b266.GetB266Period(f.ctx, connect.NewRequest(&stillhousev1.GetB266PeriodRequest{
		Id: first.Msg.GetPeriod().GetId(),
	}))
	if err != nil {
		t.Fatalf("GetB266Period: %v", err)
	}
	if got := again.Msg.GetPeriod().GetDueOn(); got != was {
		t.Errorf("the due date on an existing period moved from %q to %q after a change of election", was, got)
	}
}

// Sanity: the handler's notion of "today" and the domain's agree, so a
// suggestion made with no date is the same one made with today's date.
func TestSuggestWithNoDateMatchesToday(t *testing.T) {
	f, _, b266 := newCalendarFixture(t)
	today := time.Now().UTC().Format("2006-01-02")

	bare, err := b266.SuggestB266Period(f.ctx, connect.NewRequest(&stillhousev1.SuggestB266PeriodRequest{}))
	if err != nil {
		t.Fatalf("SuggestB266Period: %v", err)
	}
	dated, err := b266.SuggestB266Period(f.ctx, connect.NewRequest(&stillhousev1.SuggestB266PeriodRequest{On: today}))
	if err != nil {
		t.Fatalf("SuggestB266Period: %v", err)
	}
	if bare.Msg.GetPeriodStart() != dated.Msg.GetPeriodStart() ||
		bare.Msg.GetPeriodEnd() != dated.Msg.GetPeriodEnd() {
		t.Errorf("no date gave %s → %s but today gave %s → %s",
			bare.Msg.GetPeriodStart(), bare.Msg.GetPeriodEnd(),
			dated.Msg.GetPeriodStart(), dated.Msg.GetPeriodEnd())
	}
}

// A period spanning an excise indexation is not an error. Stage 142
// refused one, reasoning that CRA indexes on 1 April and a fiscal-month
// boundary would never be crossed — true for a monthly filer, and false
// for the semi-annual filer whose January-to-June period contains 1 April
// by construction. Refusing it made semi-annual filing impossible.
//
// The duty figures survive it, because every removal, bottling run and
// loss is charged at the rate in force on its own date. What cannot
// survive is the single rate the form asks to be quoted in a box, so the
// period says so.
//
// Unit-tested rather than driven end to end: the shipped rate table holds
// one band, so a period spanning two known bands cannot be constructed
// until the EDN history is seeded (PLAN A2).
func TestRateChangeNoteFor(t *testing.T) {
	band := func(year int, source string) excise.Band {
		return excise.Band{
			EffectiveFrom: time.Date(year, 4, 1, 0, 0, 0, 0, time.UTC),
			Source:        source,
		}
	}
	if got := rateChangeNoteFor(band(2026, "EDN104"), band(2026, "EDN104")); got != "" {
		t.Errorf("one band covering the whole period raised a note: %q", got)
	}
	got := rateChangeNoteFor(band(2026, "EDN104"), band(2027, "EDN110"))
	if got == "" {
		t.Fatal("a period spanning two bands raised no note")
	}
	// The note has to name the date, both notices, and say that the totals
	// are still right — an operator reading only "rate change" would
	// reasonably assume the figures are wrong.
	for _, want := range []string{"2027-04-01", "EDN104", "EDN110", "totals are right"} {
		if !strings.Contains(got, want) {
			t.Errorf("note %q does not mention %q", got, want)
		}
	}
}

// The note reaches the return as a filing blocker rather than being
// swallowed.
func TestRateChangeNoteBecomesAFilingBlocker(t *testing.T) {
	rep := projectB266(b266Totals{rateChangeNote: "spans a rate change"},
		testPeriodStart, testPeriodEnd, testGeneratedAt)
	if len(rep.GetFilingBlockers()) != 1 || rep.GetFilingBlockers()[0] != "spans a rate change" {
		t.Errorf("filing blockers: got %v, want the rate change note", rep.GetFilingBlockers())
	}
}
