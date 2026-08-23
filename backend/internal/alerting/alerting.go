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
