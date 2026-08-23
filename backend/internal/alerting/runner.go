package alerting

import (
	"context"
	"log/slog"
	"time"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// Notifier is the slice of the mailer this package needs. Narrow, so the
// runner can be tested without a mail provider and so nothing here has
// to know what Resend is.
type Notifier interface {
	SendAlert(ctx context.Context, to, displayName, title, detail, url string) error
}

// Runner evaluates the rules on a timer and keeps the alerts table in
// step with what is true.
type Runner struct {
	db       *tenantdb.DB
	queries  *sqlcgen.Queries
	mailer   Notifier
	baseURL  string
	logger   *slog.Logger
	interval time.Duration
}

func NewRunner(
	db *tenantdb.DB, q *sqlcgen.Queries, mailer Notifier,
	baseURL string, interval time.Duration, logger *slog.Logger,
) *Runner {
	return &Runner{db: db, queries: q, mailer: mailer, baseURL: baseURL, interval: interval, logger: logger}
}

// Start runs the loop until ctx is cancelled. It evaluates once
// immediately, because the first useful moment for an operator who has
// just restarted the stack is now, not in fifteen minutes.
func (r *Runner) Start(ctx context.Context) {
	r.RunOnce(ctx)
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.RunOnce(ctx)
		}
	}
}

// RunOnce evaluates every tenant. A tenant that errors is logged and
// skipped rather than aborting the sweep: one distillery's bad filing
// calendar must not stop another's stamp alert.
func (r *Runner) RunOnce(ctx context.Context) {
	tenants, err := r.queries.ListAllTenants(ctx)
	if err != nil {
		r.logger.Error("alerting: list tenants", "err", err)
		return
	}
	for _, t := range tenants {
		if err := r.RunTenant(ctx, t, time.Now().UTC()); err != nil {
			r.logger.Error("alerting: evaluate tenant", "err", err, "tenant_id", t.ID)
		}
	}
}

// RunTenant evaluates one tenant and reconciles the alerts table.
//
// The reconciliation is the interesting part. Everything Evaluate found
// is upserted, which bumps last_seen_at; anything of an evaluated kind
// that was NOT touched is older than this sweep and therefore no longer
// true, so it resolves. That is what makes an alert a condition rather
// than a message: nobody has to remember to close one.
func (r *Runner) RunTenant(ctx context.Context, tenant sqlcgen.Tenant, now time.Time) error {
	var (
		opened   []sqlcgen.Alert
		resolved []sqlcgen.Alert
	)
	err := r.db.WithTenantTx(ctx, tenant.ID, func(ctx context.Context, q *sqlcgen.Queries) error {
		found, err := Evaluate(ctx, q, tenant, now)
		if err != nil {
			return err
		}
		for _, a := range found {
			row, err := q.UpsertAlert(ctx, sqlcgen.UpsertAlertParams{
				TenantID:   tenant.ID,
				Kind:       a.Kind,
				Severity:   a.Severity,
				SubjectKey: a.SubjectKey,
				Title:      a.Title,
				Detail:     a.Detail,
				EntityType: a.EntityType,
				EntityID:   a.EntityID,
			})
			if err != nil {
				return err
			}
			opened = append(opened, row)
		}
		// Same transaction as the upserts above, which is what makes
		// "not touched by this sweep" exact — see the query.
		resolved, err = q.ResolveStaleAlerts(ctx, Kinds)
		return err
	})
	if err != nil {
		return err
	}
	for _, a := range resolved {
		r.logger.Info("alert resolved", "tenant_id", tenant.ID, "kind", a.Kind, "subject", a.SubjectKey)
	}
	_ = opened
	return r.notify(ctx, tenant)
}

// notify mails the alerts that have not been mailed yet.
//
// notified_at is set per alert, so a restart does not re-send and an
// alert that stays open for a week is mailed once rather than every
// fifteen minutes. Info-level alerts never mail — they are for the
// dashboard, and a system that emails about everything is a system
// people filter.
func (r *Runner) notify(ctx context.Context, tenant sqlcgen.Tenant) error {
	if r.mailer == nil {
		return nil
	}
	var (
		pending    []sqlcgen.Alert
		recipients []sqlcgen.User
	)
	if err := r.db.WithTenantTx(ctx, tenant.ID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var err error
		pending, err = q.ListAlertsNeedingNotification(ctx)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		recipients, err = q.ListAlertEmailRecipients(ctx)
		return err
	}); err != nil {
		return err
	}
	if len(pending) == 0 || len(recipients) == 0 {
		return nil
	}

	for _, a := range pending {
		sent := false
		for _, u := range recipients {
			if u.TenantID != tenant.ID {
				continue // ListAlertEmailRecipients is RLS-scoped; belt and braces
			}
			if err := r.mailer.SendAlert(ctx, u.Email, u.DisplayName, a.Title, a.Detail, r.baseURL); err != nil {
				r.logger.Warn("alert email failed", "err", err, "to", u.Email, "alert", a.ID)
				continue
			}
			sent = true
		}
		if !sent {
			// Leave notified_at unset so the next sweep tries again. A
			// mail provider having a bad minute should not silently
			// swallow the one alert that mattered.
			continue
		}
		if err := r.db.WithTenantTx(ctx, tenant.ID, func(ctx context.Context, q *sqlcgen.Queries) error {
			return q.MarkAlertNotified(ctx, a.ID)
		}); err != nil {
			r.logger.Warn("alert mark notified", "err", err, "alert", a.ID)
		}
	}
	return nil
}
