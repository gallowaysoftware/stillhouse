package rpc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/stamps"
)

// RecordStampDisposition records stamps that left the count without
// reaching a bottle, with a reason.
//
// This replaces VoidStamps as the way stamps leave — VoidStamps stays
// for compatibility and records a 'spoiled' disposition underneath, so
// no caller breaks and no void goes unexplained. The distinction matters
// because spoilage, loss and a return to CRA are the same arithmetic and
// completely different events, and only one of them is something the
// licensee has to report.
func (s *ExciseStampService) RecordStampDisposition(
	ctx context.Context,
	req *connect.Request[stillhousev1.RecordStampDispositionRequest],
) (*connect.Response[stillhousev1.RecordStampDispositionResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	orderID, err := uuid.Parse(in.GetStampOrderId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid stamp_order_id"))
	}
	kind, err := dispositionKindToDB(in.GetKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if in.GetQuantity() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("quantity must be > 0"))
	}
	explanation := strings.TrimSpace(in.GetExplanation())
	if explanation == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("an explanation is required — a reason code alone does not answer "+
				"an auditor asking what happened"))
	}
	occurredOn, err := parseDateOrToday(in.GetOccurredOn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("occurred_on must be YYYY-MM-DD"))
	}

	var row sqlcgen.ExciseStampDisposition
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		order, e := q.GetStampOrder(ctx, orderID)
		if e != nil {
			return e
		}
		available := order.QuantityReceived - order.QuantityApplied - order.QuantityVoided
		if in.GetQuantity() > available {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"only %d stamps on this order are unaccounted for", available))
		}
		row, e = q.CreateStampDisposition(ctx, sqlcgen.CreateStampDispositionParams{
			TenantID: u.TenantID, StampOrderID: orderID, Kind: kind,
			Quantity:    in.GetQuantity(),
			SerialStart: strings.TrimSpace(in.GetSerialStart()),
			SerialEnd:   strings.TrimSpace(in.GetSerialEnd()),
			OccurredOn:  occurredOn, Explanation: explanation,
			ReportedRef: strings.TrimSpace(in.GetReportedRef()),
			RecordedBy:  u.ID,
		})
		if e != nil {
			return e
		}
		// The order's counter moves too, so "how many are left" stays
		// correct without the disposition table having to be summed
		// every time somebody asks.
		if _, e := q.IncrementStampOrderVoided(ctx, sqlcgen.IncrementStampOrderVoidedParams{
			ID: orderID, QuantityVoided: in.GetQuantity(),
		}); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "excise_stamp_order", orderID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event":        "stamp_disposition",
				"kind":         string(kind),
				"quantity":     in.GetQuantity(),
				"serial_start": row.SerialStart,
				"serial_end":   row.SerialEnd,
				"explanation":  explanation,
				"reported_ref": row.ReportedRef,
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("stamp order not found"))
		}
		s.logger.Error("RecordStampDisposition", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.RecordStampDispositionResponse{
		Disposition: dispositionToProto(row, "", u.DisplayName),
	}), nil
}

func (s *ExciseStampService) ListStampDispositions(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListStampDispositionsRequest],
) (*connect.Response[stillhousev1.ListStampDispositionsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	filter := ""
	if k := req.Msg.GetKind(); k != stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_UNSPECIFIED {
		dbKind, err := dispositionKindToDB(k)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		filter = string(dbKind)
	}
	var rows []sqlcgen.ListStampDispositionsRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListStampDispositions(ctx, filter)
		return e
	}); err != nil {
		s.logger.Error("ListStampDispositions", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.StampDisposition, 0, len(rows))
	for _, r := range rows {
		out = append(out, dispositionToProto(sqlcgen.ExciseStampDisposition{
			ID: r.ID, TenantID: r.TenantID, StampOrderID: r.StampOrderID, Kind: r.Kind,
			Quantity: r.Quantity, SerialStart: r.SerialStart, SerialEnd: r.SerialEnd,
			OccurredOn: r.OccurredOn, Explanation: r.Explanation, ReportedRef: r.ReportedRef,
		}, r.Jurisdiction, r.RecordedByName))
	}
	return connect.NewResponse(&stillhousev1.ListStampDispositionsResponse{Dispositions: out}), nil
}

// ReconcileStampOrder walks an order's issued serial range end to end.
//
// The answer is not "how many are left" — three counters already said
// that. It is "where did stamp ABC00457 go", which is the question CRA
// asks and which counters cannot answer.
func (s *ExciseStampService) ReconcileStampOrder(
	ctx context.Context,
	req *connect.Request[stillhousev1.ReconcileStampOrderRequest],
) (*connect.Response[stillhousev1.ReconcileStampOrderResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	orderID, err := uuid.Parse(req.Msg.GetStampOrderId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid stamp_order_id"))
	}

	var in stamps.Input
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		order, e := q.GetStampOrder(ctx, orderID)
		if e != nil {
			return e
		}
		in.QuantityReceived = int64(order.QuantityReceived)
		if order.SerialStart.Valid {
			in.SerialStart = order.SerialStart.String
		}
		if order.SerialEnd.Valid {
			in.SerialEnd = order.SerialEnd.String
		}

		usage, e := q.ListStampUsageForOrder(ctx, orderID)
		if e != nil {
			return e
		}
		for _, us := range usage {
			purpose := fmt.Sprintf("run %d — %s", us.RunNo, us.ProductName)
			if us.RunVoided {
				// The run was voided; the stamps were still applied to
				// bottles and voiding does not un-apply them.
				purpose += " (run voided)"
			}
			in.Applications = append(in.Applications, stamps.Claim{
				SerialStart: us.SerialStart, SerialEnd: us.SerialEnd,
				Count: int64(us.BottleCount), Purpose: purpose,
			})
		}

		dispositions, e := q.ListStampDispositionsForOrder(ctx, orderID)
		if e != nil {
			return e
		}
		for _, d := range dispositions {
			in.Dispositions = append(in.Dispositions, stamps.Claim{
				SerialStart: d.SerialStart, SerialEnd: d.SerialEnd,
				Count: int64(d.Quantity), Purpose: string(d.Kind),
			})
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("stamp order not found"))
		}
		s.logger.Error("ReconcileStampOrder", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	rec := stamps.Reconcile(in)
	out := &stillhousev1.ReconcileStampOrderResponse{
		SerialStart:      rec.Issued.Start,
		SerialEnd:        rec.Issued.End,
		SerialRangeKnown: rec.IssuedKnown,
		ReceivedCount:    rec.ReceivedCount,
		AppliedCount:     rec.AppliedCount,
		DisposedCount:    rec.DisposedCount,
		UnaccountedCount: rec.UnaccountedCount,
		Discrepancies:    rec.Discrepancies,
	}
	for _, a := range rec.Allocations {
		out.Allocations = append(out.Allocations, &stillhousev1.StampAllocation{
			SerialStart: a.Start, SerialEnd: a.End, Count: a.Count,
			Kind: string(a.Kind), Purpose: a.Purpose, Unplaced: a.Unplaced,
		})
	}
	return connect.NewResponse(out), nil
}

func dispositionToProto(
	d sqlcgen.ExciseStampDisposition, jurisdiction, recordedByName string,
) *stillhousev1.StampDisposition {
	return &stillhousev1.StampDisposition{
		Id:             d.ID.String(),
		StampOrderId:   d.StampOrderID.String(),
		Jurisdiction:   jurisdiction,
		Kind:           dispositionKindToProto(d.Kind),
		Quantity:       d.Quantity,
		SerialStart:    d.SerialStart,
		SerialEnd:      d.SerialEnd,
		OccurredOn:     formatDate(d.OccurredOn),
		Explanation:    d.Explanation,
		ReportedRef:    d.ReportedRef,
		RecordedByName: recordedByName,
	}
}

func dispositionKindToDB(k stillhousev1.StampDispositionKind) (sqlcgen.StampDispositionKind, error) {
	switch k {
	case stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_SPOILED:
		return sqlcgen.StampDispositionKindSpoiled, nil
	case stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_DAMAGED:
		return sqlcgen.StampDispositionKindDamaged, nil
	case stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_LOST:
		return sqlcgen.StampDispositionKindLost, nil
	case stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_STOLEN:
		return sqlcgen.StampDispositionKindStolen, nil
	case stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_DESTROYED:
		return sqlcgen.StampDispositionKindDestroyed, nil
	case stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_RETURNED:
		return sqlcgen.StampDispositionKindReturned, nil
	}
	return "", errors.New("a disposition kind is required")
}

func dispositionKindToProto(k sqlcgen.StampDispositionKind) stillhousev1.StampDispositionKind {
	switch k {
	case sqlcgen.StampDispositionKindSpoiled:
		return stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_SPOILED
	case sqlcgen.StampDispositionKindDamaged:
		return stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_DAMAGED
	case sqlcgen.StampDispositionKindLost:
		return stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_LOST
	case sqlcgen.StampDispositionKindStolen:
		return stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_STOLEN
	case sqlcgen.StampDispositionKindDestroyed:
		return stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_DESTROYED
	case sqlcgen.StampDispositionKindReturned:
		return stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_RETURNED
	}
	return stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_UNSPECIFIED
}
