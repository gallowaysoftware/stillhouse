package rpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// consignmentDutyNote is on every response. An operator sending stock out
// the door will otherwise assume it has been removed, and the return will
// not tell them otherwise.
const consignmentDutyNote = "This is not a removal. The stock is still yours and still on hand — it is simply not here, and not available to promise to anybody else. A removal is recorded when it sells through, which is when duty falls due at an at-removal duty point. If your own arrangement treats the shipment itself as the removal, record a removal when it ships and do not use consignment."

// settleOutcome is what a settlement does to a consignment.
//
// Pure, because the rule it encodes is worth reading on its own: a
// consignment closes when nothing is still out, and it closes as settled
// rather than recalled if anything at all sold. A consignment that was
// half sold and half returned is a sale that ended, not a recall.
type settleOutcome struct {
	Settled  int32
	Recalled int32
	Status   sqlcgen.ConsignmentStatus
	Closed   bool
}

func settleConsignment(c sqlcgen.Consignment, sold, recalled int32) (settleOutcome, error) {
	var out settleOutcome
	if sold < 0 || recalled < 0 {
		return out, errors.New("bottles cannot be negative")
	}
	if sold == 0 && recalled == 0 {
		return out, errors.New("say how many sold through and how many came back")
	}
	stillOut := c.Bottles - c.BottlesSettled - c.BottlesRecalled
	if sold+recalled > stillOut {
		return out, fmt.Errorf(
			"only %d bottles are still out on this consignment and %d are being accounted for",
			stillOut, sold+recalled)
	}

	out.Settled = c.BottlesSettled + sold
	out.Recalled = c.BottlesRecalled + recalled
	out.Closed = out.Settled+out.Recalled == c.Bottles
	switch {
	case !out.Closed:
		out.Status = sqlcgen.ConsignmentStatusOut
	case out.Settled > 0:
		out.Status = sqlcgen.ConsignmentStatusSettled
	default:
		out.Status = sqlcgen.ConsignmentStatusRecalled
	}
	return out, nil
}

// SendOnConsignment puts stock at a customer's premises without removing
// it. PLAN D7.
func (s *RemovalService) SendOnConsignment(
	ctx context.Context,
	req *connect.Request[stillhousev1.SendOnConsignmentRequest],
) (*connect.Response[stillhousev1.SendOnConsignmentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	lotID, err := uuid.Parse(req.Msg.GetPackagedInventoryId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid packaged_inventory_id"))
	}
	custID, err := uuid.Parse(req.Msg.GetCustomerId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("consignment stock is at a named customer's premises — say whose"))
	}
	if req.Msg.GetBottles() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bottles must be greater than zero"))
	}
	sentOn, err := parseDate(req.Msg.GetSentOn(), "sent_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var row sqlcgen.Consignment
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		lot, e := q.GetPackagedInventoryForUpdate(ctx, lotID)
		if e != nil {
			return e
		}
		// Consignment does not take stock off the shelf — it is still
		// ours — so what it must not do is send out more than exists.
		if req.Msg.GetBottles() > lot.BottlesOnHand {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"lot %s has %d bottles on hand and %d are being sent",
				lot.LotCode, lot.BottlesOnHand, req.Msg.GetBottles()))
		}
		no, e := q.NextConsignmentNo(ctx)
		if e != nil {
			return e
		}
		row, e = q.CreateConsignment(ctx, sqlcgen.CreateConsignmentParams{
			TenantID: u.TenantID, ConsignmentNo: no,
			PackagedInventoryID: lotID, CustomerID: custID,
			Bottles:   req.Msg.GetBottles(),
			SentOn:    pgtype.Date{Valid: true, Time: sentOn},
			Notes:     req.Msg.GetNotes(),
			CreatedBy: uuid.NullUUID{UUID: u.ID, Valid: true},
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "consignment", row.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"lot_code": lot.LotCode, "bottles": req.Msg.GetBottles(),
				"duty": "unchanged — a consignment is not a removal",
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("packaged lot not found"))
		}
		s.logger.Error("SendOnConsignment", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SendOnConsignmentResponse{
		Consignment: consignmentToProto(row, "", "", ""),
		DutyNote:    consignmentDutyNote,
	}), nil
}

// SettleConsignment records what sold through and what came back.
//
// The sold half becomes a removal — through recordRemoval, the same path
// a hand-keyed one takes — because that is the moment the stock actually
// leaves. The recalled half becomes nothing at all: it never sold, so
// there is no removal to reverse and no return to record.
func (s *RemovalService) SettleConsignment(
	ctx context.Context,
	req *connect.Request[stillhousev1.SettleConsignmentRequest],
) (*connect.Response[stillhousev1.SettleConsignmentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	on := time.Now().UTC()
	if v := req.Msg.GetOn(); v != "" {
		d, e := parseDate(v, "on")
		if e != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, e)
		}
		on = d
	}

	var row sqlcgen.Consignment
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		c, e := q.GetConsignmentForUpdate(ctx, id)
		if e != nil {
			return e
		}
		outcome, e := settleConsignment(c, req.Msg.GetBottlesSold(), req.Msg.GetBottlesRecalled())
		if e != nil {
			return connect.NewError(connect.CodeInvalidArgument, e)
		}

		// The sold half leaves stock now, through the same path a
		// hand-keyed removal takes.
		if n := req.Msg.GetBottlesSold(); n > 0 {
			if _, e := recordRemoval(ctx, q, u.TenantID, u.ID, removalInput{
				PackagedInventoryID: c.PackagedInventoryID,
				Bottles:             n,
				RemovalDate:         pgtype.Date{Valid: true, Time: on},
				CustomerID:          uuid.NullUUID{UUID: c.CustomerID, Valid: true},
				Reference:           fmt.Sprintf("consignment %d", c.ConsignmentNo),
			}); e != nil {
				return e
			}
		}

		var settledOn pgtype.Date
		if outcome.Closed {
			settledOn = pgtype.Date{Valid: true, Time: on}
		} else if c.SettledOn.Valid {
			settledOn = c.SettledOn
		}
		row, e = q.SettleConsignment(ctx, sqlcgen.SettleConsignmentParams{
			ID: id, Settled: outcome.Settled, Recalled: outcome.Recalled,
			Status: outcome.Status, SettledOn: settledOn,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "consignment", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"sold": req.Msg.GetBottlesSold(), "recalled": req.Msg.GetBottlesRecalled(),
				"status": string(outcome.Status),
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("consignment not found"))
		}
		s.logger.Error("SettleConsignment", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SettleConsignmentResponse{
		Consignment: consignmentToProto(row, "", "", ""),
	}), nil
}

func (s *RemovalService) ListConsignments(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListConsignmentsRequest],
) (*connect.Response[stillhousev1.ListConsignmentsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	limit := req.Msg.GetLimit()
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	out := &stillhousev1.ListConsignmentsResponse{}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		rows, e := q.ListConsignments(ctx, limit)
		if e != nil {
			return e
		}
		for _, r := range rows {
			c := consignmentToProto(sqlcgen.Consignment{
				ID: r.ID, ConsignmentNo: r.ConsignmentNo,
				PackagedInventoryID: r.PackagedInventoryID, CustomerID: r.CustomerID,
				Bottles: r.Bottles, BottlesSettled: r.BottlesSettled,
				BottlesRecalled: r.BottlesRecalled, Status: r.Status,
				SentOn: r.SentOn, SettledOn: r.SettledOn, Notes: r.Notes,
			}, r.LotCode, r.ProductName, r.CustomerName)
			c.BottlesOut = r.BottlesOut
			out.Consignments = append(out.Consignments, c)
		}
		sum, e := q.ConsignmentSummary(ctx)
		if e != nil {
			return e
		}
		out.OpenConsignments, out.BottlesOut = sum.OpenConsignments, sum.BottlesOut
		return nil
	}); err != nil {
		s.logger.Error("ListConsignments", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}

func consignmentToProto(c sqlcgen.Consignment, lot, product, customer string) *stillhousev1.Consignment {
	out := &stillhousev1.Consignment{
		Id: c.ID.String(), ConsignmentNo: c.ConsignmentNo,
		PackagedInventoryId: c.PackagedInventoryID.String(),
		LotCode:             lot, ProductName: product,
		CustomerId: c.CustomerID.String(), CustomerName: customer,
		Bottles: c.Bottles, BottlesSettled: c.BottlesSettled,
		BottlesRecalled: c.BottlesRecalled,
		BottlesOut:      c.Bottles - c.BottlesSettled - c.BottlesRecalled,
		Status:          consignmentStatusToProto(c.Status),
		SentOn:          c.SentOn.Time.Format("2006-01-02"),
		Notes:           c.Notes,
	}
	if c.SettledOn.Valid {
		out.SettledOn = c.SettledOn.Time.Format("2006-01-02")
	}
	return out
}

func consignmentStatusToProto(s sqlcgen.ConsignmentStatus) stillhousev1.ConsignmentStatus {
	switch s {
	case sqlcgen.ConsignmentStatusOut:
		return stillhousev1.ConsignmentStatus_CONSIGNMENT_STATUS_OUT
	case sqlcgen.ConsignmentStatusSettled:
		return stillhousev1.ConsignmentStatus_CONSIGNMENT_STATUS_SETTLED
	case sqlcgen.ConsignmentStatusRecalled:
		return stillhousev1.ConsignmentStatus_CONSIGNMENT_STATUS_RECALLED
	}
	return stillhousev1.ConsignmentStatus_CONSIGNMENT_STATUS_UNSPECIFIED
}
