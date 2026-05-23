package rpc

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
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
