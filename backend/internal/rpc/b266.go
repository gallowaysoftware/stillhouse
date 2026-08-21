package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
		// The due date is frozen on first generation: a later change of
		// fiscal-month election must not silently restate when a past
		// return was due.
		tenant, e := q.GetTenantByID(ctx, u.TenantID)
		if e != nil {
			return e
		}
		period, e = q.UpsertB266PeriodDraft(ctx, sqlcgen.UpsertB266PeriodDraftParams{
			TenantID:    u.TenantID,
			PeriodStart: pgtype.Date{Valid: true, Time: pStart},
			PeriodEnd:   pgtype.Date{Valid: true, Time: pEnd},
			DueOn: pgtype.Date{
				Valid: true,
				Time:  tenantFilingBasis(tenant).DueDate(pEnd),
			},
		})
		if e != nil {
			return e
		}

		// Compute report.
		report, e = computeB266Report(ctx, q, u.TenantID, pStart, pEnd, queryEnd)
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
	// The step between "here are your figures" and a filed return. Checked
	// before anything is written, so a missing confirmation costs nothing
	// but the round trip.
	if err := checkFilingAcknowledgement(req.Msg.GetAcknowledgement()); err != nil {
		return nil, err
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
		report, e = computeB266Report(ctx, q, u.TenantID, existing.PeriodStart.Time, existing.PeriodEnd.Time, queryEnd)
		if e != nil {
			return e
		}
		snapshot, e := protojson.Marshal(report)
		if e != nil {
			return e
		}
		period, e = q.SubmitB266Period(ctx, sqlcgen.SubmitB266PeriodParams{
			ID:                    id,
			Snapshot:              snapshot,
			SubmittedBy:           uuid.NullUUID{UUID: u.ID, Valid: true},
			FilingAcknowledgement: req.Msg.GetAcknowledgement(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "b266_period", id.String(),
			sqlcgen.AuditActionSign, map[string]any{
				"period_start":     existing.PeriodStart.Time.Format("2006-01-02"),
				"period_end":       existing.PeriodEnd.Time.Format("2006-01-02"),
				"duty_payable_cad": report.DutyPayableCad,
				// The wording, not a flag: this row is read years later.
				"acknowledgement": req.Msg.GetAcknowledgement(),
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
	var (
		period         sqlcgen.B266Period
		acknowledgedBy string
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		row, e := q.GetB266PeriodWithAcknowledger(ctx, id)
		if e != nil {
			return e
		}
		acknowledgedBy = row.AcknowledgedByName
		period = sqlcgen.B266Period{
			ID: row.ID, TenantID: row.TenantID,
			PeriodStart: row.PeriodStart, PeriodEnd: row.PeriodEnd,
			Status: row.Status, Snapshot: row.Snapshot,
			SubmittedAt: row.SubmittedAt, SubmittedBy: row.SubmittedBy,
			Notes: row.Notes, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			DueOn:                 row.DueOn,
			FilingAcknowledgedAt:  row.FilingAcknowledgedAt,
			FilingAcknowledgedBy:  row.FilingAcknowledgedBy,
			FilingAcknowledgement: row.FilingAcknowledgement,
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("period not found"))
		}
		s.logger.Error("GetB266Period", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	periodProto := b266PeriodToProto(period)
	periodProto.FilingAcknowledgedByName = acknowledgedBy
	out := &stillhousev1.GetB266PeriodResponse{
		Period: periodProto,
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

// computeB266Report gathers the period's totals from the database and
// hands them to projectB266, which does the arithmetic. Everything below
// is I/O; every line of the return itself is computed in
// b266_projection.go, without a database, and tested there.
func computeB266Report(
	ctx context.Context,
	q *sqlcgen.Queries,
	tenantID uuid.UUID,
	periodStart, periodEnd, queryEnd time.Time,
) (*stillhousev1.B266Report, error) {
	totals, err := gatherB266Totals(ctx, q, tenantID, periodStart, periodEnd, queryEnd)
	if err != nil {
		return nil, err
	}
	report := projectB266(totals, periodStart, periodEnd, time.Now())

	// Validate the snapshot is JSON-serializable as a sanity check.
	if _, err := json.Marshal(report); err != nil {
		return nil, err
	}
	return report, nil
}

// gatherB266Totals runs the five aggregation queries behind the return.
// Nothing here decides anything — it reads, and the projection judges.
//
// Everything is bounded by the period, closing balances included: they are
// walked back from the running totals over whatever moved after the period
// closed. Filing late, or amending a prior period, therefore reports the
// balance that was actually on hand at period end.
func gatherB266Totals(
	ctx context.Context,
	q *sqlcgen.Queries,
	tenantID uuid.UUID,
	periodStart, periodEnd, queryEnd time.Time,
) (b266Totals, error) {
	var t b266Totals

	// The rates the period is charged at, and whether one set of them
	// covers the whole period.
	//
	// A period CAN span an indexation and it is not an error: CRA indexes
	// on 1 April, and a semi-annual filer's January-to-June period contains
	// it by construction. Refusing one — which stage 142 did, reasoning
	// from monthly periods only — made semi-annual filing impossible.
	//
	// The duty figures survive it. Every removal and every bottling run
	// stores the rate in force on its own date (stages 142 and 143), and
	// losses are charged the same way below, so duty_payable_cad is right
	// either way. What cannot survive is the single rate the form asks to
	// be quoted in a box, so the period says so and lets the operator
	// decide what to write there.
	startBand, err := excise.RateOn(periodStart)
	if err != nil {
		return t, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	endBand, err := excise.RateOn(periodEnd)
	if err != nil {
		return t, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	// The band in force at the close is the one quoted: it is the rate
	// most of a spanning period's activity was charged at, and the one an
	// operator has in front of them at filing time.
	t.dutyBand = endBand
	t.rateChangeNote = rateChangeNoteFor(startBand, endBand)

	reasonSums, err := q.SumBulkMovementsByReason(ctx, sqlcgen.SumBulkMovementsByReasonParams{
		OccurredAt:   pgtype.Timestamptz{Valid: true, Time: periodStart},
		OccurredAt_2: pgtype.Timestamptz{Valid: true, Time: queryEnd},
	})
	if err != nil {
		return t, err
	}
	t.byReason = make(map[string]float64, len(reasonSums))
	for _, r := range reasonSums {
		t.byReason[r.Reason] = r.TotalLaa
	}

	// Closing balances as of the period end, not as of now. queryEnd is the
	// exclusive bound — the day after the last day of the period — so a
	// movement dated on the period's final day is inside it.
	if t.bulkClosingLAA, err = q.SumBulkOnHandAsOf(ctx,
		pgtype.Timestamptz{Valid: true, Time: queryEnd}); err != nil {
		return t, err
	}
	packaged, err := q.SumPackagedOnHandAsOf(ctx, pgtype.Date{Valid: true, Time: queryEnd})
	if err != nil {
		return t, err
	}
	t.packagedClosingLAA = packaged.TotalLaa
	t.packagedClosingBottle = packaged.TotalBottles

	pStartDate := pgtype.Date{Valid: true, Time: periodStart}
	pEndDate := pgtype.Date{Valid: true, Time: queryEnd}

	bottling, err := q.SumBottlingRunsInPeriod(ctx, sqlcgen.SumBottlingRunsInPeriodParams{
		BottlingDate:   pStartDate,
		BottlingDate_2: pEndDate,
	})
	if err != nil {
		return t, err
	}
	t.bottlingDrawnLAA = bottling.TotalLaa
	t.bottlingPackagedLAA = bottling.PackagedLaa
	t.bottlingLossLAA = bottling.LossLaa
	t.bottlingBottles = bottling.TotalBottles

	removals, err := q.SumRemovalsInPeriod(ctx, sqlcgen.SumRemovalsInPeriodParams{
		RemovalDate:   pStartDate,
		RemovalDate_2: pEndDate,
	})
	if err != nil {
		return t, err
	}
	t.removedLAA = removals.TotalLaa
	t.removedBottles = removals.TotalBottles
	t.removedDutyCAD = removals.TotalDuty
	t.removedOver7LAA = removals.Over7Laa
	t.removedOver7DutyCAD = removals.Over7Duty
	t.removedOver7Bottles = removals.Over7Bottles
	t.removedUnder7Litres = removals.Under7Litres
	t.removedUnder7DutyCAD = removals.Under7Duty
	t.removedUnder7Bottles = removals.Under7Bottles

	// Duty crystallised at packaging during the period. Zero for an
	// at-removal tenant; the whole duty figure for an at-packaging one.
	packagingDuty, err := q.SumBottlingDutyInPeriod(ctx, sqlcgen.SumBottlingDutyInPeriodParams{
		BottlingDate:   pStartDate,
		BottlingDate_2: pEndDate,
	})
	if err != nil {
		return t, err
	}
	t.packagedDutyCAD = packagingDuty.TotalDuty
	t.packagedDutyOver7LAA = packagingDuty.Over7Laa
	t.packagedDutyOver7CAD = packagingDuty.Over7Duty
	t.packagedDutyUnder7Litres = packagingDuty.Under7Litres
	t.packagedDutyUnder7CAD = packagingDuty.Under7Duty
	t.packagedDutyPaidLAA = packagingDuty.DutyPaidLaa
	t.packagedDutyPaidBottles = packagingDuty.DutyPaidBottles

	// Line D. Read from the adjustment rows rather than from the movement
	// reasons, because the adjustment row is the one that carries the
	// signed delta, the reason code and the author — the movement is only
	// its effect on the ledger.
	adj, err := q.SumInventoryAdjustmentsInPeriod(ctx, sqlcgen.SumInventoryAdjustmentsInPeriodParams{
		OccurredAt:   pgtype.Timestamptz{Valid: true, Time: periodStart},
		OccurredAt_2: pgtype.Timestamptz{Valid: true, Time: queryEnd},
	})
	if err != nil {
		return t, err
	}
	t.adjustmentsNetLAA = adj.NetLaa
	t.adjustmentsIncreaseLAA = adj.IncreaseLaa
	t.adjustmentsDecreaseLAA = adj.DecreaseLaa
	t.adjustmentsCount = adj.AdjustmentCount

	// Losses by duty treatment (EDM3-4-1), and the duty the dutiable ones
	// attract at the period's rate.
	losses, err := q.SumLossesByTreatmentInPeriod(ctx, sqlcgen.SumLossesByTreatmentInPeriodParams{
		OccurredAt:   pgtype.Timestamptz{Valid: true, Time: periodStart},
		OccurredAt_2: pgtype.Timestamptz{Valid: true, Time: queryEnd},
	})
	if err != nil {
		return t, err
	}
	t.lossesRelievedLAA = losses.RelievedLaa
	t.lossesDutiableLAA = losses.DutiableLaa
	t.lossesUnclassifiedLAA = losses.UnclassifiedLaa
	t.lossesUnclassifiedCount = losses.UnclassifiedCount

	destroyed, err := q.SumDestructionsByTreatmentInPeriod(ctx, sqlcgen.SumDestructionsByTreatmentInPeriodParams{
		OccurredAt:   pgtype.Timestamptz{Valid: true, Time: periodStart},
		OccurredAt_2: pgtype.Timestamptz{Valid: true, Time: queryEnd},
	})
	if err != nil {
		return t, err
	}
	t.destroyedUnclassifiedLAA = destroyed.UnclassifiedLaa
	t.destroyedUnclassifiedN = destroyed.UnclassifiedCount

	// Each dutiable loss is charged at the rate in force on the day it
	// happened, not at the period's. Across an indexation — which a
	// semi-annual period always spans — a period rate would charge half
	// the losses at the wrong one.
	dutiable, err := q.ListDutiableLossesInPeriod(ctx, sqlcgen.ListDutiableLossesInPeriodParams{
		OccurredAt:   pgtype.Timestamptz{Valid: true, Time: periodStart},
		OccurredAt_2: pgtype.Timestamptz{Valid: true, Time: queryEnd},
	})
	if err != nil {
		return t, err
	}
	for _, l := range dutiable {
		_, duty, e := excise.DutyOnLAA(l.OccurredAt.Time, l.Laa)
		if e != nil {
			// Unreachable while the period itself resolved a rate at both
			// ends, but a loss dated outside the table would otherwise be
			// charged nothing at all.
			return t, connect.NewError(connect.CodeFailedPrecondition, e)
		}
		t.dutyOnLossesCAD += duty
	}

	// The basis the period was computed on, carried onto the return: the
	// figures cannot be checked without knowing which event crystallised
	// them.
	tenant, err := q.GetTenantByID(ctx, tenantID)
	if err != nil {
		return t, err
	}
	t.dutyPoint = tenant.DutyPoint
	t.dutyPointFrom = tenant.DutyPointEffectiveFrom.Time

	// When this return falls due, on the licensee's own fiscal calendar,
	// and whether these dates are the period they elected to file.
	basis := tenantFilingBasis(tenant)
	t.dueOn = basis.DueDate(periodEnd)
	if ok, why := basis.MatchesElection(periodStart, periodEnd); !ok {
		t.electionMismatch = why
	}

	return t, nil
}

func b266PeriodToProto(p sqlcgen.B266Period) *stillhousev1.B266Period {
	out := &stillhousev1.B266Period{
		Id:                    p.ID.String(),
		PeriodStart:           formatDate(p.PeriodStart),
		PeriodEnd:             formatDate(p.PeriodEnd),
		Status:                b266StatusToProto(p.Status),
		Notes:                 p.Notes,
		CreatedAt:             timestamppb.New(p.CreatedAt.Time),
		UpdatedAt:             timestamppb.New(p.UpdatedAt.Time),
		DueOn:                 formatDate(p.DueOn),
		FilingAcknowledgement: p.FilingAcknowledgement,
	}
	if p.FilingAcknowledgedAt.Valid {
		out.FilingAcknowledgedAt = timestamppb.New(p.FilingAcknowledgedAt.Time)
	}
	if p.FilingAcknowledgedBy.Valid {
		out.FilingAcknowledgedBy = p.FilingAcknowledgedBy.UUID.String()
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

// rateChangeNoteFor returns the sentence to put on a period that spans an
// excise indexation, or "" when one set of rates covers it.
//
// Split out so it can be exercised without a database and without a
// second band in the shipped rate table — which, until the EDN history is
// seeded (PLAN A2), is the only reason the end-to-end case cannot be
// built.
func rateChangeNoteFor(start, end excise.Band) string {
	if start.EffectiveFrom.Equal(end.EffectiveFrom) {
		return ""
	}
	return fmt.Sprintf(
		"this period spans the excise rate change on %s (%s → %s). Every line is charged at the rate in force on its own date, so the totals are right — but the single rate quoted below is %s's and will not multiply out against them.",
		end.EffectiveFrom.Format("2006-01-02"), start.Source, end.Source, end.Source)
}
