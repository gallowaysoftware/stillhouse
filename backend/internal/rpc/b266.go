package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
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
		// Two returns covering the same day would report the same alcohol
		// twice. An exact re-generate of the same range is fine — that's
		// how a draft gets refreshed — but any other overlap is a filing
		// error, and cheaper to refuse here than to unpick later.
		overlaps, e := q.B266PeriodsOverlapping(ctx, sqlcgen.B266PeriodsOverlappingParams{
			RangeStart: pgtype.Date{Valid: true, Time: pStart},
			RangeEnd:   pgtype.Date{Valid: true, Time: pEnd},
		})
		if e != nil {
			return e
		}
		if len(overlaps) > 0 {
			o := overlaps[0]
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"period %s → %s overlaps the existing %s period %s → %s; B266 periods must not share days",
				pStart.Format("2006-01-02"), pEnd.Format("2006-01-02"),
				o.Status, o.PeriodStart.Time.Format("2006-01-02"), o.PeriodEnd.Time.Format("2006-01-02")))
		}

		// Upsert draft period.
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
		// Errors raised deliberately inside the transaction carry the
		// code and the sentence the operator needs — "period already
		// submitted", "periods must not share days". Collapsing every
		// one of them to "internal error" threw that away and told the
		// caller Stillhouse was broken.
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
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
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "b266_period", id.String(),
			sqlcgen.AuditActionSign, map[string]any{
				"period_start":     existing.PeriodStart.Time.Format("2006-01-02"),
				"period_end":       existing.PeriodEnd.Time.Format("2006-01-02"),
				"duty_payable_cad": report.DutyPayableCad,
			})
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

// ReopenB266Period flips a submitted period back to draft so backdated
// corrections can pass the period-lock guard (stage 66). The snapshot
// stays for audit so reviewers can compare frozen-at-submit vs. live.
// Owner-only; reason is mandatory.
func (s *B266Service) ReopenB266Period(
	ctx context.Context,
	req *connect.Request[stillhousev1.ReopenB266PeriodRequest],
) (*connect.Response[stillhousev1.ReopenB266PeriodResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if u.Role != sqlcgen.UserRoleOwner {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("owner role required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	reason := req.Msg.GetReason()
	if reason == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("reason is required"))
	}
	var period sqlcgen.B266Period
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		period, e = q.ReopenB266Period(ctx, id)
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "b266_period", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event":        "reopened",
				"period_start": period.PeriodStart.Time.Format("2006-01-02"),
				"period_end":   period.PeriodEnd.Time.Format("2006-01-02"),
				"reason":       reason,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("period not found or not submitted"))
		}
		s.logger.Error("ReopenB266Period", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.ReopenB266PeriodResponse{
		Period: b266PeriodToProto(period),
	}), nil
}

func (s *B266Service) GetB266Period(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetB266PeriodRequest],
) (*connect.Response[stillhousev1.GetB266PeriodResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	var period sqlcgen.B266Period
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		period, e = q.GetB266Period(ctx, id)
		return e
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("period not found"))
		}
		s.logger.Error("GetB266Period", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := &stillhousev1.GetB266PeriodResponse{
		Period: b266PeriodToProto(period),
	}
	if len(period.Snapshot) > 0 {
		var snap stillhousev1.B266Report
		if e := protojson.Unmarshal(period.Snapshot, &snap); e == nil {
			out.Snapshot = &snap
		} else {
			s.logger.Warn("GetB266Period: snapshot unmarshal", "err", e)
		}
	}
	return connect.NewResponse(out), nil
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
		// Errors raised deliberately inside the transaction carry the
		// code and the sentence the operator needs — "period already
		// submitted", "periods must not share days". Collapsing every
		// one of them to "internal error" threw that away and told the
		// caller Stillhouse was broken.
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
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

		BulkProductionLaa:             byReason["production_gauge"],
		BulkReceivedInBondLaa:         byReason["transfer_in_bond"],
		BulkBlendInLaa:                byReason["blend"],
		BulkTransferredToPackagingLaa: byReason["transfer_to_packaging"],
		BulkTransferredOutInBondLaa:   byReason["transfer_out_in_bond"],
		BulkLossesLaa:                 byReason["loss_evaporation"] + byReason["loss_unaccounted"],
		BulkDestroyedLaa:              byReason["destruction"],
		BulkClosingLaa:                round4(currentBulk),
		// Adopted stock is reported but deliberately NOT counted among the
		// receipts below, so the reverse-walk puts it in the opening
		// balance. It was in the warehouse before the period; only the
		// bookkeeping is new. Counting it as a receipt would overstate what
		// the distillery made, on a return CRA reads.
		BulkOpeningInventoryAdoptedLaa: round4(byReason["opening_inventory"]),

		PackagedPackagedLaa:            round4(bottlingTotals.PackagedLaa),
		PackagedPackagingLossLaa:       round4(bottlingTotals.LossLaa),
		PackagedPackagedBottles:        bottlingTotals.TotalBottles,
		PackagedRemovedDutyPaidLaa:     round4(removalTotals.TotalLaa),
		PackagedRemovedDutyPaidBottles: removalTotals.TotalBottles,
		PackagedClosingLaa:             round4(currentPackagedRow.TotalLaa),
		PackagedClosingBottles:         currentPackagedRow.TotalBottles,

		PackagedRemovedOver7Laa:      round4(removalTotals.Over7Laa),
		PackagedRemovedOver7DutyCad:  round2cents(removalTotals.Over7Duty),
		PackagedRemovedOver7Bottles:  removalTotals.Over7Bottles,
		PackagedRemovedUnder7Litres:  round4(removalTotals.Under7Litres),
		PackagedRemovedUnder7DutyCad: round2cents(removalTotals.Under7Duty),
		PackagedRemovedUnder7Bottles: removalTotals.Under7Bottles,

		// Both rates travel with the return so each band's line can be
		// checked against the quantity it is charged on. Quoting only the
		// per-LAA rate beside a blended LAA total made the arithmetic fail
		// for any period holding both bands.
		DutyRatePerLaa:         excise.DutyRatePerLAAOver7Pct,
		DutyRatePerLitreUnder7: excise.DutyRatePerLAtOrUnder7,
		DutyPayableCad:         round2cents(removalTotals.TotalDuty),
		GeneratedAt:            timestamppb.New(time.Now()),
	}

	// Reverse-walk opening balances.
	bulkReceipts := report.BulkProductionLaa + report.BulkReceivedInBondLaa + report.BulkBlendInLaa
	bulkWithdrawals := report.BulkTransferredToPackagingLaa + report.BulkTransferredOutInBondLaa + report.BulkLossesLaa + report.BulkDestroyedLaa
	report.BulkOpeningLaa = round4(report.BulkClosingLaa - bulkReceipts + bulkWithdrawals)

	// Packaged inventory only ever received what became bottles. Walking
	// back with the tank-gauge figure instead — which includes what was
	// spilled on the way — subtracts alcohol that never arrived, and a
	// first-ever return came out with a negative opening balance.
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

// The `int(x*N + 0.5)` idiom these used to spell rounds correctly only for
// positive numbers: int() truncates toward zero, so on a negative the +0.5
// bias pushes the result the wrong way. round4(-0.12345) came out -0.1234
// against +0.1235 for the positive, and round4(-1.5) came out -1.4999.
//
// Negatives do reach here — a cask's strength drift is negative in a cool,
// humid warehouse, which is the normal Canadian case. math.Round rounds
// half away from zero symmetrically and is identical for positives, so
// nothing already recorded moves.
func round2cents(x float64) float64 {
	return math.Round(x*100) / 100
}

func round4(x float64) float64 {
	return math.Round(x*10000) / 10000
}
