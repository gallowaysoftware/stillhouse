package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/excise"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type B266Service struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewB266Service(db *tenantdb.DB, logger *slog.Logger) *B266Service {
	return &B266Service{db: db, logger: logger}
}

func (s *B266Service) GenerateB266(
	ctx context.Context,
	req *connect.Request[stillhousev1.GenerateB266Request],
) (*connect.Response[stillhousev1.GenerateB266Response], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	pStart, err := time.Parse("2006-01-02", req.Msg.GetPeriodStart())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("period_start must be YYYY-MM-DD"))
	}
	pEnd, err := time.Parse("2006-01-02", req.Msg.GetPeriodEnd())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("period_end must be YYYY-MM-DD"))
	}
	if pEnd.Before(pStart) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("period_end must be >= period_start"))
	}
	// end-exclusive bound for queries
	queryEnd := pEnd.AddDate(0, 0, 1)

	var (
		period sqlcgen.B266Period
		report *stillhousev1.B266Report
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		// Upsert draft period.
		var e error
		period, e = q.UpsertB266PeriodDraft(ctx, sqlcgen.UpsertB266PeriodDraftParams{
			TenantID:    u.TenantID,
			PeriodStart: pgtype.Date{Valid: true, Time: pStart},
			PeriodEnd:   pgtype.Date{Valid: true, Time: pEnd},
		})
		if e != nil {
			return e
		}

		// Compute report.
		report, e = computeB266Report(ctx, q, pStart, pEnd, queryEnd)
		return e
	})
	if err != nil {
		s.logger.Error("GenerateB266", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.GenerateB266Response{
		Period: b266PeriodToProto(period),
		Report: report,
	}), nil
}

func (s *B266Service) SubmitB266(
	ctx context.Context,
	req *connect.Request[stillhousev1.SubmitB266Request],
) (*connect.Response[stillhousev1.SubmitB266Response], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetPeriodId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid period_id"))
	}

	var (
		period sqlcgen.B266Period
		report *stillhousev1.B266Report
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		existing, e := q.GetB266Period(ctx, id)
		if e != nil {
			return e
		}
		if existing.Status == sqlcgen.B266StatusSubmitted {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("period already submitted"))
		}
		queryEnd := existing.PeriodEnd.Time.AddDate(0, 0, 1)
		report, e = computeB266Report(ctx, q, existing.PeriodStart.Time, existing.PeriodEnd.Time, queryEnd)
		if e != nil {
			return e
		}
		snapshot, e := protojson.Marshal(report)
		if e != nil {
			return e
		}
		period, e = q.SubmitB266Period(ctx, sqlcgen.SubmitB266PeriodParams{
			ID:          id,
			Snapshot:    snapshot,
			SubmittedBy: uuid.NullUUID{UUID: u.ID, Valid: true},
		})
		return e
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("period not found"))
		}
		s.logger.Error("SubmitB266", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SubmitB266Response{
		Period:   b266PeriodToProto(period),
		Snapshot: report,
	}), nil
}

func (s *B266Service) ListB266Periods(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListB266PeriodsRequest],
) (*connect.Response[stillhousev1.ListB266PeriodsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.B266Period
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListB266Periods(ctx)
		return e
	})
	if err != nil {
		s.logger.Error("ListB266Periods", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.B266Period, 0, len(rows))
	for _, p := range rows {
		out = append(out, b266PeriodToProto(p))
	}
	return connect.NewResponse(&stillhousev1.ListB266PeriodsResponse{Periods: out}), nil
}

// computeB266Report walks the underlying tables for the period and projects
// every B266 line. Opening balances are derived by reverse-walking from the
// current running totals (current = opening + receipts - withdrawals →
// opening = current - receipts + withdrawals). That works cleanly when the
// report is generated promptly after period close.
func computeB266Report(
	ctx context.Context,
	q *sqlcgen.Queries,
	periodStart, periodEnd, queryEnd time.Time,
) (*stillhousev1.B266Report, error) {
	pStartTS := pgtype.Timestamptz{Valid: true, Time: periodStart}
	pEndTS := pgtype.Timestamptz{Valid: true, Time: queryEnd}

	reasonSums, err := q.SumBulkMovementsByReason(ctx, sqlcgen.SumBulkMovementsByReasonParams{
		OccurredAt:   pStartTS,
		OccurredAt_2: pEndTS,
	})
	if err != nil {
		return nil, err
	}
	byReason := map[string]float64{}
	for _, r := range reasonSums {
		byReason[r.Reason] = r.TotalLaa
	}

	currentBulk, err := q.SumBulkOnHandAsOfDate(ctx)
	if err != nil {
		return nil, err
	}
	currentPackagedRow, err := q.SumPackagedOnHandLAA(ctx)
	if err != nil {
		return nil, err
	}

	pStartDate := pgtype.Date{Valid: true, Time: periodStart}
	pEndDate := pgtype.Date{Valid: true, Time: queryEnd}
	bottlingTotals, err := q.SumBottlingRunsInPeriod(ctx, sqlcgen.SumBottlingRunsInPeriodParams{
		BottlingDate:   pStartDate,
		BottlingDate_2: pEndDate,
	})
	if err != nil {
		return nil, err
	}
	removalTotals, err := q.SumRemovalsInPeriod(ctx, sqlcgen.SumRemovalsInPeriodParams{
		RemovalDate:   pStartDate,
		RemovalDate_2: pEndDate,
	})
	if err != nil {
		return nil, err
	}

	report := &stillhousev1.B266Report{
		PeriodStart: periodStart.Format("2006-01-02"),
		PeriodEnd:   periodEnd.Format("2006-01-02"),

		BulkProductionLaa:               byReason["production_gauge"],
		BulkReceivedInBondLaa:           byReason["transfer_in_bond"],
		BulkBlendInLaa:                  byReason["blend"],
		BulkTransferredToPackagingLaa:   byReason["transfer_to_packaging"],
		BulkTransferredOutInBondLaa:     byReason["transfer_out_in_bond"],
		BulkLossesLaa:                   byReason["loss_evaporation"] + byReason["loss_unaccounted"],
		BulkDestroyedLaa:                byReason["destruction"],
		BulkClosingLaa:                  round4(currentBulk),

		PackagedPackagedLaa:           round4(bottlingTotals.TotalLaa),
		PackagedPackagedBottles:       bottlingTotals.TotalBottles,
		PackagedRemovedDutyPaidLaa:    round4(removalTotals.TotalLaa),
		PackagedRemovedDutyPaidBottles: removalTotals.TotalBottles,
		PackagedClosingLaa:            round4(currentPackagedRow.TotalLaa),
		PackagedClosingBottles:        currentPackagedRow.TotalBottles,

		DutyRatePerLaa: excise.DutyRatePerLAAOver7Pct,
		DutyPayableCad: round2cents(removalTotals.TotalDuty),
		GeneratedAt:    timestamppb.New(time.Now()),
	}

	// Reverse-walk opening balances.
	bulkReceipts := report.BulkProductionLaa + report.BulkReceivedInBondLaa + report.BulkBlendInLaa
	bulkWithdrawals := report.BulkTransferredToPackagingLaa + report.BulkTransferredOutInBondLaa + report.BulkLossesLaa + report.BulkDestroyedLaa
	report.BulkOpeningLaa = round4(report.BulkClosingLaa - bulkReceipts + bulkWithdrawals)

	report.PackagedOpeningLaa = round4(report.PackagedClosingLaa - report.PackagedPackagedLaa + report.PackagedRemovedDutyPaidLaa)

	// Validate the snapshot is JSON-serializable as a sanity check.
	if _, err := json.Marshal(report); err != nil {
		return nil, err
	}
	return report, nil
}

func b266PeriodToProto(p sqlcgen.B266Period) *stillhousev1.B266Period {
	out := &stillhousev1.B266Period{
		Id:          p.ID.String(),
		PeriodStart: formatDate(p.PeriodStart),
		PeriodEnd:   formatDate(p.PeriodEnd),
		Status:      b266StatusToProto(p.Status),
		Notes:       p.Notes,
		CreatedAt:   timestamppb.New(p.CreatedAt.Time),
		UpdatedAt:   timestamppb.New(p.UpdatedAt.Time),
	}
	if p.SubmittedAt.Valid {
		out.SubmittedAt = timestamppb.New(p.SubmittedAt.Time)
	}
	if p.SubmittedBy.Valid {
		out.SubmittedBy = p.SubmittedBy.UUID.String()
	}
	return out
}

func b266StatusToProto(s sqlcgen.B266Status) stillhousev1.B266Status {
	switch s {
	case sqlcgen.B266StatusDraft:
		return stillhousev1.B266Status_B266_STATUS_DRAFT
	case sqlcgen.B266StatusSubmitted:
		return stillhousev1.B266Status_B266_STATUS_SUBMITTED
	}
	return stillhousev1.B266Status_B266_STATUS_UNSPECIFIED
}

func round2cents(x float64) float64 {
	return float64(int(x*100+0.5)) / 100
}

func round4(x float64) float64 {
	return float64(int(x*10000+0.5)) / 10000
}
