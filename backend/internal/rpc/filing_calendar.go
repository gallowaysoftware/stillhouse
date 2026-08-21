package rpc

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/filing"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The reporting calendar.
//
// Stillhouse assumed a calendar month and derived nothing from it. Under
// EDM3-1-1 ¶50 a fiscal month is set by notification (form B268) rather
// than assumed, an authorized licensee may file semi-annually (form B284),
// and the return is due by the last day of the fiscal month following the
// period — a date that existed nowhere in the model, which is why H5's
// filing notifications had nothing to fire on.

// tenantFilingBasis reads the licensee's reporting calendar.
func tenantFilingBasis(t sqlcgen.Tenant) filing.Basis {
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

func (s *TenantService) UpdateFilingCalendar(
	ctx context.Context,
	req *connect.Request[stillhousev1.UpdateFilingCalendarRequest],
) (*connect.Response[stillhousev1.UpdateFilingCalendarResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	freq, err := filingFrequencyToDB(in.GetFilingFrequency())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	basis, err := fiscalMonthBasisToDB(in.GetFiscalMonthBasis())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var endDay pgtype.Int4
	if basis == sqlcgen.FiscalMonthBasisFixedDayOfMonth {
		endDay = pgtype.Int4{Int32: in.GetFiscalMonthEndDay(), Valid: true}
	}
	// Validated through the domain type rather than re-stated here, so the
	// rule an operator meets is the same one the arithmetic obeys.
	if err := (filing.Basis{
		MonthBasis:  monthBasisToDomain(basis),
		MonthEndDay: int(endDay.Int32),
	}).Validate(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var t sqlcgen.Tenant
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		t, e = q.UpdateFilingCalendar(ctx, sqlcgen.UpdateFilingCalendarParams{
			ID:                              u.TenantID,
			FilingFrequency:                 freq,
			FiscalMonthBasis:                basis,
			FiscalMonthEndDay:               endDay,
			FiscalMonthNotificationRef:      strings.TrimSpace(in.GetFiscalMonthNotificationRef()),
			FilingFrequencyAuthorizationRef: strings.TrimSpace(in.GetFilingFrequencyAuthorizationRef()),
		})
		if e != nil {
			return e
		}
		// Both settings are CRA elections with paperwork behind them, so
		// the change is worth its own audit entry rather than a generic
		// tenant update.
		return audit.Write(ctx, q, u.TenantID, u.ID, "tenant", u.TenantID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event":                "filing_calendar_changed",
				"filing_frequency":     string(freq),
				"fiscal_month_basis":   string(basis),
				"fiscal_month_end_day": endDay.Int32,
				"b268_notification":    t.FiscalMonthNotificationRef,
				"b284_authorization":   t.FilingFrequencyAuthorizationRef,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("tenant not found"))
		}
		if ce := classifyWriteErr(err, "tenant not found"); ce != nil {
			return nil, ce
		}
		s.logger.Error("UpdateFilingCalendar", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.UpdateFilingCalendarResponse{
		Tenant: tenantToProto(t),
	}), nil
}

// SuggestB266Period answers "which period am I supposed to be filing?" on
// the licensee's own fiscal calendar, so the dates are not something an
// operator works out by hand every month.
func (s *B266Service) SuggestB266Period(
	ctx context.Context,
	req *connect.Request[stillhousev1.SuggestB266PeriodRequest],
) (*connect.Response[stillhousev1.SuggestB266PeriodResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	on := time.Now().UTC()
	if v := req.Msg.GetOn(); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("on must be YYYY-MM-DD"))
		}
		on = t
	}

	var basis filing.Basis
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		t, e := q.GetTenantByID(ctx, u.TenantID)
		if e != nil {
			return e
		}
		basis = tenantFilingBasis(t)
		return nil
	}); err != nil {
		s.logger.Error("SuggestB266Period", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	// The period being filed is normally the one that just closed, not the
	// one running: a licensee in the first week of July is filing June.
	// Asking for the period containing today would suggest a period whose
	// figures are not final.
	current := basis.PeriodContaining(on)
	p := current
	if !dayOf(on).After(current.End) {
		p = basis.PeriodContaining(dayOf(current.Start).AddDate(0, 0, -1))
	}
	prev := basis.PeriodContaining(dayOf(p.Start).AddDate(0, 0, -1))

	return connect.NewResponse(&stillhousev1.SuggestB266PeriodResponse{
		PeriodStart:         p.Start.Format("2006-01-02"),
		PeriodEnd:           p.End.Format("2006-01-02"),
		DueOn:               p.DueOn.Format("2006-01-02"),
		DaysUntilDue:        daysUntil(p.DueOn, on),
		PreviousPeriodStart: prev.Start.Format("2006-01-02"),
		PreviousPeriodEnd:   prev.End.Format("2006-01-02"),
	}), nil
}

// daysUntil counts whole days from `from` to `due`, negative once the due
// date has passed.
func daysUntil(due, from time.Time) int32 {
	return int32(dayOf(due).Sub(dayOf(from)).Hours() / 24)
}

func filingFrequencyToDB(f stillhousev1.FilingFrequency) (sqlcgen.FilingFrequency, error) {
	switch f {
	case stillhousev1.FilingFrequency_FILING_FREQUENCY_MONTHLY:
		return sqlcgen.FilingFrequencyMonthly, nil
	case stillhousev1.FilingFrequency_FILING_FREQUENCY_SEMI_ANNUAL:
		return sqlcgen.FilingFrequencySemiAnnual, nil
	}
	return "", errors.New("filing_frequency is required")
}

func filingFrequencyToProto(f sqlcgen.FilingFrequency) stillhousev1.FilingFrequency {
	if f == sqlcgen.FilingFrequencySemiAnnual {
		return stillhousev1.FilingFrequency_FILING_FREQUENCY_SEMI_ANNUAL
	}
	return stillhousev1.FilingFrequency_FILING_FREQUENCY_MONTHLY
}

func fiscalMonthBasisToDB(b stillhousev1.FiscalMonthBasis) (sqlcgen.FiscalMonthBasis, error) {
	switch b {
	case stillhousev1.FiscalMonthBasis_FISCAL_MONTH_BASIS_CALENDAR_MONTH:
		return sqlcgen.FiscalMonthBasisCalendarMonth, nil
	case stillhousev1.FiscalMonthBasis_FISCAL_MONTH_BASIS_FIXED_DAY_OF_MONTH:
		return sqlcgen.FiscalMonthBasisFixedDayOfMonth, nil
	}
	return "", errors.New("fiscal_month_basis is required")
}

func fiscalMonthBasisToProto(b sqlcgen.FiscalMonthBasis) stillhousev1.FiscalMonthBasis {
	if b == sqlcgen.FiscalMonthBasisFixedDayOfMonth {
		return stillhousev1.FiscalMonthBasis_FISCAL_MONTH_BASIS_FIXED_DAY_OF_MONTH
	}
	return stillhousev1.FiscalMonthBasis_FISCAL_MONTH_BASIS_CALENDAR_MONTH
}

func monthBasisToDomain(b sqlcgen.FiscalMonthBasis) filing.MonthBasis {
	if b == sqlcgen.FiscalMonthBasisFixedDayOfMonth {
		return filing.FixedDayOfMonth
	}
	return filing.CalendarMonth
}
