package rpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/zone"
)

// assertDateNotInLockedPeriod returns a FailedPrecondition error if d falls
// inside a submitted B266 period. Mutations that would change a filed
// month's totals must be blocked — the snapshot is what was filed with CRA
// and the live tables need to keep agreeing with it.
//
// Pass the date the action happens on (bottling_date, removal_date, gauge
// occurred_at, etc.). NULL/invalid dates pass through; the caller is
// expected to validate those separately.
func assertDateNotInLockedPeriod(ctx context.Context, q *sqlcgen.Queries, d pgtype.Date) error {
	if !d.Valid {
		return nil
	}
	// A movement dated ahead is on hand now and on no return until then.
	if today := zone.Day(zone.From(ctx), time.Now()); zone.Day(time.UTC, d.Time).After(today) {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"%s has not happened yet; today is %s", d.Time.Format("2006-01-02"), today.Format("2006-01-02")))
	}
	if err := q.LockFilingShared(ctx); err != nil {
		return err
	}
	period, err := q.B266PeriodCoveringDate(ctx, d)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	return connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("date %s falls in submitted B266 period %s → %s; reopen the period or correct via a forward-dated entry",
			d.Time.Format("2006-01-02"),
			period.PeriodStart.Time.Format("2006-01-02"),
			period.PeriodEnd.Time.Format("2006-01-02")))
}
