// Package alerting turns things Stillhouse already knows into things
// somebody is told.
//
// The system could work out, at any moment, that a return is due in nine
// days, that there are four days of stamps left, that a fermentation
// stopped reporting on Thursday, or that a cask has not been gauged
// since last spring. All of it was reachable only by going to look —
// which, as PLAN put it, is not an alert. A dashboard nobody opens on a
// Tuesday tells you nothing on the Tuesday it mattered.
//
// The shape here is deliberate and worth stating, because the obvious
// alternative is worse. An alert is not a message that gets sent; it is
// a *condition with a life cycle*. It opens when the condition becomes
// true, stays open while it remains true, and resolves itself when it
// stops. Re-evaluating updates rather than duplicates. Nothing is
// dismissed by clicking: acknowledging records that a human has seen it,
// which is a different claim from the condition having gone away, and
// conflating the two is how alerting systems become things people mute.
package alerting

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/filing"
	"github.com/gallowaysoftware/stillhouse/backend/internal/money"
)

// Thresholds. Each is an operational judgement rather than a figure from
// a published source, and is named here rather than buried at its use so
// that changing one is a decision rather than an edit.
const (
	// FilingDueWithin opens a warning this far ahead of the due date.
	// Two weeks is long enough to gather what a return needs and short
	// enough that the alert still means something when it appears.
	FilingDueWithin = 14 * 24 * time.Hour

	// StampsLowDays is the cover below which stamps become a warning.
	// A week is roughly the lead time on a stamp order.
	StampsLowDays = 7.0

	// FermentStaleAfter is how long an active fermentation may go
	// without a reading. Two days covers a weekend; a ferment nobody has
	// read since before one is either finished and unrecorded or stuck.
	FermentStaleAfter = 48 * time.Hour

	// BarrelUnmeasuredAfter is how long a filled cask may go without a
	// gauge. A year is generous — this is not asking anyone to dip
	// weekly, it is asking that a balance on the books has been measured
	// within living memory.
	BarrelUnmeasuredAfter = 365 * 24 * time.Hour

	// RedistillationOpenAfter is how long spirit may be in the still with
	// no output recorded before it is worth saying so. A week covers a
	// long run and a weekend; beyond it, alcohol has left stock and not
	// come back on the books, which is the one shape of gap a period-end
	// reconciliation cannot explain.
	RedistillationOpenAfter = 7 * 24 * time.Hour

	// LicenceRenewalWindow is how far ahead a licence expiry becomes a
	// warning. CRA requires renewal 30 days before expiry (EDM2-1-1), so
	// the alert opens at 60 — a month before the deadline to act on the
	// deadline, which is the only version of this that helps.
	LicenceRenewalWindow = 60 * 24 * time.Hour
)

// Kinds is every alert kind this package evaluates. ResolveStaleAlerts
// is scoped to it, so a rule that fails cannot silently resolve another
// rule's alerts — and a kind added to the enum but not evaluated here
// simply never opens, rather than being closed the moment it does.
var Kinds = []string{
	string(sqlcgen.AlertKindFilingDue),
	string(sqlcgen.AlertKindFilingOverdue),
	string(sqlcgen.AlertKindStampsLow),
	string(sqlcgen.AlertKindFermentationStalled),
	string(sqlcgen.AlertKindBarrelUnmeasured),
	string(sqlcgen.AlertKindLicenceExpiring),
	string(sqlcgen.AlertKindLicenceExpired),
	string(sqlcgen.AlertKindLicenceSecurityExpiring),
	string(sqlcgen.AlertKindWorkOrderOverdue),
	string(sqlcgen.AlertKindRedistillationOpen),
	string(sqlcgen.AlertKindProvincialFilingDue),
	string(sqlcgen.AlertKindProvincialFilingOverdue),
	string(sqlcgen.AlertKindInvoiceOverdue),
	string(sqlcgen.AlertKindEquipmentServiceDue),
	string(sqlcgen.AlertKindEquipmentDown),
	string(sqlcgen.AlertKindMaterialLow),
}

// Alert is one condition found true, before it is written.
type Alert struct {
	Kind       sqlcgen.AlertKind
	Severity   sqlcgen.AlertSeverity
	SubjectKey string
	Title      string
	Detail     string
	EntityType string
	EntityID   uuid.NullUUID
}

// Evaluate runs every rule against one tenant and returns the conditions
// currently true. It reads and does not write; persisting is the
// caller's job, which keeps the rules testable without a database
// transaction and keeps the life-cycle logic in one place.
func Evaluate(
	ctx context.Context, q *sqlcgen.Queries, tenant sqlcgen.Tenant, now time.Time,
) ([]Alert, error) {
	var out []Alert

	filingAlerts, err := evaluateFiling(ctx, q, tenant, now)
	if err != nil {
		return nil, fmt.Errorf("filing: %w", err)
	}
	out = append(out, filingAlerts...)

	stampAlerts, err := evaluateStamps(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("stamps: %w", err)
	}
	out = append(out, stampAlerts...)

	fermentAlerts, err := evaluateFermentations(ctx, q, now)
	if err != nil {
		return nil, fmt.Errorf("fermentations: %w", err)
	}
	out = append(out, fermentAlerts...)

	barrelAlerts, err := evaluateBarrels(ctx, q, now)
	if err != nil {
		return nil, fmt.Errorf("barrels: %w", err)
	}
	out = append(out, barrelAlerts...)

	licenceAlerts, err := evaluateLicences(ctx, q, now)
	if err != nil {
		return nil, fmt.Errorf("licences: %w", err)
	}
	out = append(out, licenceAlerts...)

	workAlerts, err := evaluateWorkOrders(ctx, q, now)
	if err != nil {
		return nil, fmt.Errorf("work orders: %w", err)
	}
	out = append(out, workAlerts...)

	materialAlerts, err := evaluateMaterials(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("materials: %w", err)
	}
	out = append(out, materialAlerts...)

	equipmentAlerts, err := evaluateEquipment(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("equipment: %w", err)
	}
	out = append(out, equipmentAlerts...)

	invoiceAlerts, err := evaluateOverdueInvoices(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("invoices: %w", err)
	}
	out = append(out, invoiceAlerts...)

	provincialAlerts, err := evaluateProvincialFilings(ctx, q, now)
	if err != nil {
		return nil, fmt.Errorf("provincial filings: %w", err)
	}
	out = append(out, provincialAlerts...)

	redistAlerts, err := evaluateRedistillations(ctx, q, now)
	if err != nil {
		return nil, fmt.Errorf("redistillations: %w", err)
	}
	out = append(out, redistAlerts...)

	return out, nil
}

// evaluateRedistillations raises spirit that went into the still and has
// no output recorded.
//
// This is the gap A8 was about. The withdrawal is on the return either
// way — it is a reportable movement — so the alcohol has left stock. If
// nothing records what came back, the difference is not a loss anybody
// has classified, it is just a number that got smaller between two
// periods.
func evaluateRedistillations(ctx context.Context, q *sqlcgen.Queries, now time.Time) ([]Alert, error) {
	rows, err := q.AlertOpenRedistillations(ctx, pgtype.Date{
		Valid: true, Time: now.Add(-RedistillationOpenAfter),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Alert, 0, len(rows))
	for _, r := range rows {
		days := int(now.Sub(r.TakenOn.Time).Hours() / 24)
		out = append(out, Alert{
			Kind:       sqlcgen.AlertKindRedistillationOpen,
			Severity:   sqlcgen.AlertSeverityWarning,
			SubjectKey: r.ID.String(),
			Title: fmt.Sprintf("%.1f LAA from %s has been in the still %d days",
				r.LaaTaken, r.SourceContainerName, days),
			Detail: "It left stock as a reportable withdrawal and nothing records what " +
				"came back. Until it does, the alcohol is off the books and the " +
				"difference is not a loss anyone has ruled on.",
			EntityType: "redistillation",
			EntityID:   uuid.NullUUID{UUID: r.ID, Valid: true},
		})
	}
	return out, nil
}

// evaluateWorkOrders raises open work whose *due* date has passed.
//
// Deliberately not "scheduled for a day in the past". A job scheduled
// Monday and done Tuesday is an ordinary week, and a system that shouts
// about it is a system people mute — which would cost more than the
// alert is worth, because the same channel carries the return deadline.
// A missed due date is a commitment broken and is worth one line.
func evaluateWorkOrders(ctx context.Context, q *sqlcgen.Queries, now time.Time) ([]Alert, error) {
	rows, err := q.AlertOverdueWorkOrders(ctx, pgtype.Date{Valid: true, Time: now})
	if err != nil {
		return nil, err
	}
	out := make([]Alert, 0, len(rows))
	for _, r := range rows {
		days := int(now.Sub(r.DueOn.Time).Hours() / 24)
		who := "nobody is assigned"
		if r.AssignedToName != "" {
			who = r.AssignedToName
		}
		out = append(out, Alert{
			Kind:       sqlcgen.AlertKindWorkOrderOverdue,
			Severity:   sqlcgen.AlertSeverityWarning,
			SubjectKey: r.ID.String(),
			Title:      fmt.Sprintf("Work order %d is overdue: %s", r.WorkOrderNo, r.Title),
			Detail: fmt.Sprintf("Due %s, %d day%s ago — %s.",
				r.DueOn.Time.Format("2006-01-02"), days, plural(days), who),
			EntityType: "work_order",
			EntityID:   uuid.NullUUID{UUID: r.ID, Valid: true},
		})
	}
	return out, nil
}

// evaluateLicences is the rule the licence register exists to make
// possible. A licence lapses because nobody was told a date was coming,
// and the consequence is not a warning letter — it is that the licensee
// is no longer licensed, with every movement after that date to explain.
//
// A licence with no recorded expiry raises nothing. Every CRA licence
// expires, so a missing date means nobody entered it; inventing a
// two-year window from an effective date that may itself be a backfill
// would produce a reminder for the wrong day, and a reminder for the
// wrong day is worse than none because it gets believed. The register
// screen says how many are missing a date instead.
func evaluateLicences(ctx context.Context, q *sqlcgen.Queries, now time.Time) ([]Alert, error) {
	rows, err := q.ListLicencesForRenewalAlert(ctx)
	if err != nil {
		return nil, err
	}
	today := now.Truncate(24 * time.Hour)
	var out []Alert
	for _, l := range rows {
		label := licenceKindLabel(l.Kind) + " licence " + l.LicenceNumber

		if l.ExpiresOn.Valid {
			expiry := l.ExpiresOn.Time
			switch {
			case expiry.Before(today):
				days := int(today.Sub(expiry).Hours() / 24)
				out = append(out, Alert{
					Kind:       sqlcgen.AlertKindLicenceExpired,
					Severity:   sqlcgen.AlertSeverityCritical,
					SubjectKey: l.ID.String(),
					Title:      label + " has expired",
					Detail: fmt.Sprintf(
						"It expired on %s, %d day%s ago. Anything done under it since then "+
							"is unlicensed activity.",
						expiry.Format("2006-01-02"), days, plural(days)),
					EntityType: "excise_licence",
					EntityID:   uuid.NullUUID{UUID: l.ID, Valid: true},
				})
			case expiry.Sub(today) <= LicenceRenewalWindow:
				days := int(expiry.Sub(today).Hours() / 24)
				sev := sqlcgen.AlertSeverityWarning
				if days <= 30 {
					// Past the point where CRA asks for the renewal, so
					// this is no longer a heads-up.
					sev = sqlcgen.AlertSeverityCritical
				}
				out = append(out, Alert{
					Kind:       sqlcgen.AlertKindLicenceExpiring,
					Severity:   sev,
					SubjectKey: l.ID.String(),
					Title:      label + " expires " + expiry.Format("2006-01-02"),
					Detail: fmt.Sprintf(
						"%d day%s away. CRA wants the renewal 30 days before expiry.",
						days, plural(days)),
					EntityType: "excise_licence",
					EntityID:   uuid.NullUUID{UUID: l.ID, Valid: true},
				})
			}
		}

		// Security lapsing has the same shape and a different remedy, so
		// it is its own alert rather than a sentence inside the one above.
		if l.SecurityExpiresOn.Valid {
			expiry := l.SecurityExpiresOn.Time
			if expiry.Sub(today) <= LicenceRenewalWindow {
				days := int(expiry.Sub(today).Hours() / 24)
				when := fmt.Sprintf("in %d day%s", days, plural(days))
				sev := sqlcgen.AlertSeverityWarning
				if expiry.Before(today) {
					when = fmt.Sprintf("%d day%s ago", -days, plural(-days))
					sev = sqlcgen.AlertSeverityCritical
				}
				out = append(out, Alert{
					Kind:       sqlcgen.AlertKindLicenceSecurityExpiring,
					Severity:   sev,
					SubjectKey: l.ID.String(),
					Title:      "Security for " + label + " expires " + expiry.Format("2006-01-02"),
					Detail: "The security posted under s.23 lapses " + when +
						". A spirits licence requires it to be in force.",
					EntityType: "excise_licence",
					EntityID:   uuid.NullUUID{UUID: l.ID, Valid: true},
				})
			}
		}
	}
	return out, nil
}

func licenceKindLabel(k sqlcgen.ExciseLicenceKind) string {
	switch k {
	case sqlcgen.ExciseLicenceKindSpirits:
		return "Spirits"
	case sqlcgen.ExciseLicenceKindExciseWarehouse:
		return "Excise warehouse"
	case sqlcgen.ExciseLicenceKindUsers:
		return "User's"
	case sqlcgen.ExciseLicenceKindWine:
		return "Wine"
	}
	return "Excise"
}

// evaluateFiling is the rule H5 was blocked on until stage 148 gave the
// model a due date. It answers the question an operator otherwise has to
// work out by hand: which period am I supposed to have filed, and by
// when.
func evaluateFiling(
	ctx context.Context, q *sqlcgen.Queries, tenant sqlcgen.Tenant, now time.Time,
) ([]Alert, error) {
	basis := basisFor(tenant)
	if err := basis.Validate(); err != nil {
		// A calendar that doesn't validate is a settings problem, not an
		// alerting one, and the settings screen already says so. Firing
		// a filing alert off a basis we can't trust would be inventing a
		// due date.
		return nil, nil
	}
	// The period that has *ended* — the one there is something to file
	// for. The period still running has no final figures and nobody
	// files it.
	current := basis.PeriodContaining(now)
	period := basis.PeriodContaining(current.Start.AddDate(0, 0, -1))
	due := basis.DueDate(period.End)

	periods, err := q.ListB266Periods(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range periods {
		if !p.PeriodStart.Valid || !p.PeriodEnd.Valid {
			continue
		}
		if !sameDay(p.PeriodStart.Time, period.Start) || !sameDay(p.PeriodEnd.Time, period.End) {
			continue
		}
		if p.Status == sqlcgen.B266StatusSubmitted {
			return nil, nil // already filed; nothing to say
		}
		break
	}

	label := period.Start.Format("2006-01-02") + " → " + period.End.Format("2006-01-02")
	subject := period.End.Format("2006-01-02")
	switch {
	case now.After(due):
		overdueBy := int(now.Sub(due).Hours() / 24)
		return []Alert{{
			Kind:       sqlcgen.AlertKindFilingOverdue,
			Severity:   sqlcgen.AlertSeverityCritical,
			SubjectKey: subject,
			Title:      "B266 for " + label + " is overdue",
			Detail: fmt.Sprintf(
				"It was due on %s, %d day%s ago, and has not been submitted.",
				due.Format("2006-01-02"), overdueBy, plural(overdueBy)),
			EntityType: "b266_period",
		}}, nil
	case due.Sub(now) <= FilingDueWithin:
		daysLeft := int(due.Sub(now).Hours() / 24)
		return []Alert{{
			Kind:       sqlcgen.AlertKindFilingDue,
			Severity:   sqlcgen.AlertSeverityWarning,
			SubjectKey: subject,
			Title:      "B266 for " + label + " is due soon",
			Detail: fmt.Sprintf("Due %s — %d day%s from now.",
				due.Format("2006-01-02"), daysLeft, plural(daysLeft)),
			EntityType: "b266_period",
		}}, nil
	}
	return nil, nil
}

// evaluateStamps converts a stamp count into the only form that means
// anything operationally: how many days of bottling it covers. It reads
// the same two queries the stamp panel does, so the alert and the screen
// cannot disagree about the number.
func evaluateStamps(ctx context.Context, q *sqlcgen.Queries) ([]Alert, error) {
	inventory, err := q.SumStampInventory(ctx)
	if err != nil {
		return nil, err
	}
	rates, err := q.Bottling30DayRatePerJurisdiction(ctx)
	if err != nil {
		return nil, err
	}
	perDay := make(map[string]float64, len(rates))
	for _, r := range rates {
		perDay[r.Jurisdiction] = r.BottlesPerDay30d
	}

	var out []Alert
	for _, inv := range inventory {
		rate := perDay[inv.Jurisdiction]
		if rate <= 0 {
			// Nothing bottled into this jurisdiction in a month. There is
			// no cover to compute and no shortage to report — a stock of
			// zero stamps for a product nobody is making is not news.
			continue
		}
		days := float64(inv.TotalOnHand) / rate
		if days >= StampsLowDays {
			continue
		}
		sev := sqlcgen.AlertSeverityWarning
		if days < 2 {
			sev = sqlcgen.AlertSeverityCritical
		}
		out = append(out, Alert{
			Kind:       sqlcgen.AlertKindStampsLow,
			Severity:   sev,
			SubjectKey: inv.Jurisdiction,
			Title:      fmt.Sprintf("%s excise stamps: %.1f days of cover", inv.Jurisdiction, days),
			Detail: fmt.Sprintf(
				"%d on hand against %.0f bottles a day over the last 30 days. "+
					"Stamps are Crown-controlled and ordering them is not same-day.",
				inv.TotalOnHand, rate),
			EntityType: "excise_stamp_order",
		})
	}
	return out, nil
}

func evaluateFermentations(ctx context.Context, q *sqlcgen.Queries, now time.Time) ([]Alert, error) {
	rows, err := q.AlertStaleFermentations(ctx, pgtype.Timestamptz{
		Valid: true, Time: now.Add(-FermentStaleAfter),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Alert, 0, len(rows))
	for _, r := range rows {
		last := r.PitchAt.Time
		what := "pitched"
		if r.LastReadingAt.Valid {
			last = r.LastReadingAt.Time
			what = "last read"
		}
		hours := int(now.Sub(last).Hours())
		out = append(out, Alert{
			Kind:       sqlcgen.AlertKindFermentationStalled,
			Severity:   sqlcgen.AlertSeverityWarning,
			SubjectKey: r.ID.String(),
			Title:      "Fermentation in " + r.FermenterLabel + " has gone quiet",
			Detail: fmt.Sprintf(
				"Still marked fermenting, %s %d hours ago. Either it finished and nobody "+
					"recorded it, or it is stuck.", what, hours),
			EntityType: "fermentation_run",
			EntityID:   uuid.NullUUID{UUID: r.ID, Valid: true},
		})
	}
	return out, nil
}

func evaluateBarrels(ctx context.Context, q *sqlcgen.Queries, now time.Time) ([]Alert, error) {
	rows, err := q.AlertUnmeasuredBarrels(ctx, pgtype.Timestamptz{
		Valid: true, Time: now.Add(-BarrelUnmeasuredAfter),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Alert, 0, len(rows))
	for _, r := range rows {
		detail := "No fill or regauge on record at all, though it holds spirit."
		if r.LastMeasuredAt.Valid {
			months := int(now.Sub(r.LastMeasuredAt.Time).Hours() / 24 / 30)
			detail = fmt.Sprintf(
				"Last gauged %s, %d months ago, and still shows %.1f LAA. A balance that "+
					"has not been measured is a balance you cannot evidence.",
				r.LastMeasuredAt.Time.Format("2006-01-02"), months, r.CurrentLaa)
		}
		out = append(out, Alert{
			Kind:       sqlcgen.AlertKindBarrelUnmeasured,
			Severity:   sqlcgen.AlertSeverityInfo,
			SubjectKey: r.ID.String(),
			Title:      "Cask " + r.Name + " has not been gauged in over a year",
			Detail:     detail,
			EntityType: "barrel",
			EntityID:   uuid.NullUUID{UUID: r.ID, Valid: true},
		})
	}
	return out, nil
}

func basisFor(t sqlcgen.Tenant) filing.Basis {
	b := filing.Basis{}
	if t.FilingFrequency == sqlcgen.FilingFrequencySemiAnnual {
		b.Frequency = filing.SemiAnnual
	}
	if t.FiscalMonthBasis == sqlcgen.FiscalMonthBasisFixedDayOfMonth {
		b.MonthBasis = filing.FixedDayOfMonth
		b.MonthEndDay = int(t.FiscalMonthEndDay.Int32)
	}
	return b
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// evaluateProvincialFilings raises on unfiled provincial reports whose due
// date is near or past.
//
// A missed provincial deadline is a delisting rather than an assessment,
// which is a worse outcome than a late B266, so this is not gentler than
// the federal rule.
//
// Only periods with a recorded due date are considered. A definition
// where the licensee never recorded how many days after period end the
// report is owed produces periods with no due date, and one of those can
// never be overdue — raising on it would mean inventing the deadline,
// which is the thing this whole track refuses to do.
func evaluateProvincialFilings(
	ctx context.Context, q *sqlcgen.Queries, now time.Time,
) ([]Alert, error) {
	const noticeDays = 14
	horizon := now.AddDate(0, 0, noticeDays)
	rows, err := q.ProvincialPeriodsDueBefore(ctx, pgtype.Date{Valid: true, Time: horizon})
	if err != nil {
		return nil, err
	}
	out := make([]Alert, 0, len(rows))
	for _, p := range rows {
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		days := int(p.DueOn.Time.Sub(today).Hours() / 24)
		where := p.BoardName
		if where == "" {
			where = p.Jurisdiction
		}
		if days < 0 {
			out = append(out, Alert{
				Kind:       sqlcgen.AlertKindProvincialFilingOverdue,
				Severity:   sqlcgen.AlertSeverityCritical,
				SubjectKey: p.ID.String(),
				Title: fmt.Sprintf("%s — %s was due %s",
					where, p.DefinitionName, p.DueOn.Time.Format("2006-01-02")),
				Detail: fmt.Sprintf(
					"%d day%s late, covering %s to %s. A missed provincial deadline "+
						"is a delisting, not an assessment.",
					-days, plural(-days),
					p.PeriodStart.Time.Format("2006-01-02"),
					p.PeriodEnd.Time.Format("2006-01-02")),
				EntityType: "provincial_report_period",
				EntityID:   uuid.NullUUID{UUID: p.ID, Valid: true},
			})
			continue
		}
		out = append(out, Alert{
			Kind:       sqlcgen.AlertKindProvincialFilingDue,
			Severity:   sqlcgen.AlertSeverityWarning,
			SubjectKey: p.ID.String(),
			Title: fmt.Sprintf("%s — %s due %s",
				where, p.DefinitionName, p.DueOn.Time.Format("2006-01-02")),
			Detail: fmt.Sprintf("%d day%s away, covering %s to %s.",
				days, plural(days),
				p.PeriodStart.Time.Format("2006-01-02"),
				p.PeriodEnd.Time.Format("2006-01-02")),
			EntityType: "provincial_report_period",
			EntityID:   uuid.NullUUID{UUID: p.ID, Valid: true},
		})
	}
	return out, nil
}

// evaluateOverdueInvoices raises on issued invoices past their due date
// with money still on them.
//
// Warning rather than critical: an invoice a week late is a phone call,
// not an emergency, and a distillery whose alert list is full of things
// that are not emergencies stops reading it. It escalates once the money
// has been outstanding for two months, which is the point at which it
// stops being an oversight.
func evaluateOverdueInvoices(ctx context.Context, q *sqlcgen.Queries) ([]Alert, error) {
	rows, err := q.OverdueInvoices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Alert, 0, len(rows))
	for _, inv := range rows {
		outstanding := money.FromNumeric(inv.Outstanding)
		// A fully paid invoice can still be listed here if its status was
		// never advanced; the balance is the fact, so it decides.
		if outstanding.Sign() <= 0 {
			continue
		}
		days := int(time.Since(inv.DueDate.Time).Hours() / 24)
		sev := sqlcgen.AlertSeverityWarning
		if days > 60 {
			sev = sqlcgen.AlertSeverityCritical
		}
		out = append(out, Alert{
			Kind:       sqlcgen.AlertKindInvoiceOverdue,
			Severity:   sev,
			SubjectKey: inv.ID.String(),
			Title: fmt.Sprintf("Invoice %d — %s owes $%s",
				inv.InvoiceNo, inv.CustomerName, outstanding.String(2)),
			Detail: fmt.Sprintf("%d day%s past due (%s).",
				days, plural(days), inv.DueDate.Time.Format("2006-01-02")),
			EntityType: "invoice",
			EntityID:   uuid.NullUUID{UUID: inv.ID, Valid: true},
		})
	}
	return out, nil
}

// evaluateEquipment raises on plant that is down, and on plant whose
// recorded service interval has elapsed.
//
// Only items with an interval recorded. One without is never due: a
// service schedule Stillhouse invented is one nobody agreed to, and the
// register already shows plainly that no interval is set.
func evaluateEquipment(ctx context.Context, q *sqlcgen.Queries) ([]Alert, error) {
	due, err := q.EquipmentServiceDue(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Alert, 0, len(due))
	for _, e := range due {
		detail := fmt.Sprintf("%d days since the last service; the interval is %d.",
			e.DaysSince, e.ServiceIntervalDays.Int32)
		if !e.LastServicedOn.Valid {
			detail = fmt.Sprintf("Never serviced, and %d days since it was "+
				"commissioned; the interval is %d.",
				e.DaysSince, e.ServiceIntervalDays.Int32)
		}
		out = append(out, Alert{
			Kind:       sqlcgen.AlertKindEquipmentServiceDue,
			Severity:   sqlcgen.AlertSeverityWarning,
			SubjectKey: e.ID.String(),
			Title:      e.Name + " is due for service",
			Detail:     detail,
			EntityType: "equipment",
			EntityID:   uuid.NullUUID{UUID: e.ID, Valid: true},
		})
	}

	down, err := q.EquipmentDown(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range down {
		out = append(out, Alert{
			Kind:       sqlcgen.AlertKindEquipmentDown,
			Severity:   sqlcgen.AlertSeverityCritical,
			SubjectKey: e.ID.String(),
			Title:      e.Name + " is down",
			Detail: "Nothing can be scheduled on it until it is back in service. " +
				"Record the repair against it so the register shows what happened.",
			EntityType: "equipment",
			EntityID:   uuid.NullUUID{UUID: e.ID, Valid: true},
		})
	}
	return out, nil
}

// evaluateMaterials raises on materials at or below the reorder point the
// licensee recorded, counting what is already on order.
//
// Only materials with a reorder point set. One Stillhouse guessed would
// fire at a level nobody chose, and an alert people did not choose is an
// alert they learn to dismiss — which costs more than having none.
func evaluateMaterials(ctx context.Context, q *sqlcgen.Queries) ([]Alert, error) {
	rows, err := q.MaterialsBelowReorderPoint(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Alert, 0, len(rows))
	for _, m := range rows {
		detail := fmt.Sprintf("%.2f %s on hand against a reorder point of %.2f.",
			m.OnHand, m.Uom, m.ReorderPoint.Float64)
		if m.OnOrder > 0 {
			detail = fmt.Sprintf("%.2f %s on hand and %.2f on order, against a "+
				"reorder point of %.2f.", m.OnHand, m.Uom, m.OnOrder,
				m.ReorderPoint.Float64)
		}
		if m.LeadTimeDays.Valid {
			detail += fmt.Sprintf(" Their lead time is %d days.", m.LeadTimeDays.Int32)
		}
		sev := sqlcgen.AlertSeverityWarning
		if m.OnHand+m.OnOrder <= 0 {
			sev = sqlcgen.AlertSeverityCritical
		}
		out = append(out, Alert{
			Kind:       sqlcgen.AlertKindMaterialLow,
			Severity:   sev,
			SubjectKey: m.ID.String(),
			Title:      m.Name + " is at its reorder point",
			Detail:     detail,
			EntityType: "material",
			EntityID:   uuid.NullUUID{UUID: m.ID, Valid: true},
		})
	}
	return out, nil
}
