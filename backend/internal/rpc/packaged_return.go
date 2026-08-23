package rpc

import (
	"context"
	"errors"
	"strconv"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// dutyOnReturnNote is on every response rather than in the
// documentation, because the operator recording a return is exactly the
// person who will otherwise assume the duty came back with the bottles.
const dutyOnReturnNote = "Duty is unchanged. It crystallised when these goods were packaged or removed and does not un-crystallise because they came back — recovering it is a refund claim under s.181/s.182 with a B256 behind it, which Stillhouse does not yet prepare. The stock is back; the duty is not."

// RecordPackagedReturn puts returned stock back and raises the credit.
// PLAN D7.
//
// The one thing it must not do is reduce duty. See dutyOnReturnNote: a
// return that quietly relieved duty would understate a filed return,
// which is the single failure this product exists to prevent.
func (s *RemovalService) RecordPackagedReturn(
	ctx context.Context,
	req *connect.Request[stillhousev1.RecordPackagedReturnRequest],
) (*connect.Response[stillhousev1.RecordPackagedReturnResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	lotID, err := uuid.Parse(in.GetPackagedInventoryId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid packaged_inventory_id"))
	}
	if in.GetBottles() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bottles must be greater than zero"))
	}
	cond, err := returnConditionFromProto(in.GetCondition())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	on, err := parseDate(in.GetReturnedOn(), "returned_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var customerID, removalID uuid.NullUUID
	if v := in.GetCustomerId(); v != "" {
		id, e := uuid.Parse(v)
		if e != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid customer_id"))
		}
		customerID = uuid.NullUUID{UUID: id, Valid: true}
	}
	if v := in.GetRemovalId(); v != "" {
		id, e := uuid.Parse(v)
		if e != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid removal_id"))
		}
		removalID = uuid.NullUUID{UUID: id, Valid: true}
	}

	var credit pgtype.Numeric
	if in.GetCreditAmountSet() {
		if e := credit.Scan(strconv.FormatFloat(in.GetCreditAmountCad(), 'f', 2, 64)); e != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid credit_amount_cad"))
		}
	}

	var row sqlcgen.PackagedReturn
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		lot, e := q.GetPackagedInventoryForUpdate(ctx, lotID)
		if e != nil {
			return e
		}
		// You cannot get back more than went out. Without this a
		// mistyped count restocks stock that never existed, and the lot's
		// own arithmetic stops tying out.
		if in.GetBottles() > lot.BottlesRemoved {
			return connect.NewError(connect.CodeInvalidArgument, errors.New(
				"more bottles are being returned than this lot has ever had removed ("+
					strconv.Itoa(int(in.GetBottles()))+" against "+
					strconv.Itoa(int(lot.BottlesRemoved))+")"))
		}

		no, e := q.NextPackagedReturnNo(ctx)
		if e != nil {
			return e
		}
		// The duty that was paid on these bottles, carried on the row so
		// "duty was paid and remains paid" is evidenced rather than
		// asserted. Nothing computes from it.
		var dutyPaid pgtype.Numeric
		if removalID.Valid {
			if rem, e := q.GetRemoval(ctx, removalID.UUID); e == nil && rem.BottlesRemoved > 0 {
				per := rem.DutyAmountCad / float64(rem.BottlesRemoved)
				_ = dutyPaid.Scan(strconv.FormatFloat(per*float64(in.GetBottles()), 'f', 2, 64))
			}
		}

		row, e = q.CreatePackagedReturn(ctx, sqlcgen.CreatePackagedReturnParams{
			TenantID:            u.TenantID,
			ReturnNo:            no,
			PackagedInventoryID: lotID,
			CustomerID:          customerID,
			RemovalID:           removalID,
			Bottles:             in.GetBottles(),
			Condition:           cond,
			ReturnedOn:          pgtype.Date{Valid: true, Time: on},
			Reason:              in.GetReason(),
			CreditAmountCad:     credit,
			CreditNoteNo:        in.GetCreditNoteNo(),
			DutyPaidCad:         dutyPaid,
			Notes:               in.GetNotes(),
			CreatedBy:           uuid.NullUUID{UUID: u.ID, Valid: true},
		})
		if e != nil {
			return e
		}

		// Only saleable stock goes back on the shelf. Unsaleable stock
		// came back and stays off it — restocking it would put something
		// that cannot be sold into a figure that says it can.
		if cond == sqlcgen.PackagedReturnConditionSaleable {
			if _, e := q.RestockFromReturn(ctx, sqlcgen.RestockFromReturnParams{
				PackagedInventoryID: lotID, Bottles: in.GetBottles(),
			}); e != nil {
				return e
			}
		}

		return audit.Write(ctx, q, u.TenantID, u.ID, "packaged_return", row.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"lot_code":  lot.LotCode,
				"bottles":   in.GetBottles(),
				"condition": string(cond),
				"duty":      "unchanged — see dutyOnReturnNote",
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
		s.logger.Error("RecordPackagedReturn", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	return connect.NewResponse(&stillhousev1.RecordPackagedReturnResponse{
		PackagedReturn: packagedReturnToProto(row, "", "", ""),
		DutyNote:       dutyOnReturnNote,
	}), nil
}

func (s *RemovalService) ListPackagedReturns(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListPackagedReturnsRequest],
) (*connect.Response[stillhousev1.ListPackagedReturnsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	limit := req.Msg.GetLimit()
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	out := &stillhousev1.ListPackagedReturnsResponse{}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		rows, e := q.ListPackagedReturns(ctx, limit)
		if e != nil {
			return e
		}
		for _, r := range rows {
			out.Returns = append(out.Returns, packagedReturnToProto(sqlcgen.PackagedReturn{
				ID: r.ID, ReturnNo: r.ReturnNo, PackagedInventoryID: r.PackagedInventoryID,
				CustomerID: r.CustomerID, RemovalID: r.RemovalID, Bottles: r.Bottles,
				Condition: r.Condition, ReturnedOn: r.ReturnedOn, Reason: r.Reason,
				CreditAmountCad: r.CreditAmountCad, CreditNoteNo: r.CreditNoteNo,
				DutyPaidCad: r.DutyPaidCad, Notes: r.Notes, VoidedAt: r.VoidedAt,
				VoidReason: r.VoidReason,
			}, r.LotCode, r.ProductName, r.CustomerName))
		}
		return nil
	}); err != nil {
		s.logger.Error("ListPackagedReturns", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}

// VoidPackagedReturn reverses a return that should not have been
// recorded. Voiding rather than deleting, as everything else here does:
// the row and its reason stay, and the stock it put back comes off again.
func (s *RemovalService) VoidPackagedReturn(
	ctx context.Context,
	req *connect.Request[stillhousev1.VoidPackagedReturnRequest],
) (*connect.Response[stillhousev1.VoidPackagedReturnResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	if req.Msg.GetReason() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("a void needs a reason — it is the only record of why the row is there"))
	}

	var row sqlcgen.PackagedReturn
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		r, e := q.VoidPackagedReturn(ctx, sqlcgen.VoidPackagedReturnParams{
			ID: id, VoidedBy: uuid.NullUUID{UUID: u.ID, Valid: true}, VoidReason: req.Msg.GetReason(),
		})
		if e != nil {
			return e
		}
		row = r
		// Take the stock back off, but only what was actually put on.
		if r.Condition == sqlcgen.PackagedReturnConditionSaleable {
			if _, e := q.RestockFromReturn(ctx, sqlcgen.RestockFromReturnParams{
				PackagedInventoryID: r.PackagedInventoryID, Bottles: -r.Bottles,
			}); e != nil {
				return e
			}
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "packaged_return", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{"reason": req.Msg.GetReason()})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound,
				errors.New("return not found, or already voided"))
		}
		s.logger.Error("VoidPackagedReturn", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.VoidPackagedReturnResponse{
		PackagedReturn: packagedReturnToProto(row, "", "", ""),
	}), nil
}

func packagedReturnToProto(r sqlcgen.PackagedReturn, lot, product, customer string) *stillhousev1.PackagedReturn {
	out := &stillhousev1.PackagedReturn{
		Id:                  r.ID.String(),
		ReturnNo:            r.ReturnNo,
		PackagedInventoryId: r.PackagedInventoryID.String(),
		LotCode:             lot,
		ProductName:         product,
		CustomerName:        customer,
		Bottles:             r.Bottles,
		Condition:           returnConditionToProto(r.Condition),
		ReturnedOn:          r.ReturnedOn.Time.Format("2006-01-02"),
		Reason:              r.Reason,
		CreditNoteNo:        r.CreditNoteNo,
		Notes:               r.Notes,
		Voided:              r.VoidedAt.Valid,
		VoidReason:          r.VoidReason,
	}
	if r.CustomerID.Valid {
		out.CustomerId = r.CustomerID.UUID.String()
	}
	if r.RemovalID.Valid {
		out.RemovalId = r.RemovalID.UUID.String()
	}
	if r.CreditAmountCad.Valid {
		out.CreditAmountCad = numericToFloat(r.CreditAmountCad)
		out.CreditAmountSet = true
	}
	if r.DutyPaidCad.Valid {
		out.DutyPaidCad = numericToFloat(r.DutyPaidCad)
		out.DutyPaidSet = true
	}
	return out
}

func returnConditionFromProto(c stillhousev1.PackagedReturnCondition) (sqlcgen.PackagedReturnCondition, error) {
	switch c {
	case stillhousev1.PackagedReturnCondition_PACKAGED_RETURN_CONDITION_SALEABLE:
		return sqlcgen.PackagedReturnConditionSaleable, nil
	case stillhousev1.PackagedReturnCondition_PACKAGED_RETURN_CONDITION_UNSALEABLE:
		return sqlcgen.PackagedReturnConditionUnsaleable, nil
	}
	// Not defaulted: whether returned stock can be sold again is the
	// decision this whole record exists to capture.
	return "", errors.New("say whether the returned stock is saleable or not")
}

func returnConditionToProto(c sqlcgen.PackagedReturnCondition) stillhousev1.PackagedReturnCondition {
	if c == sqlcgen.PackagedReturnConditionSaleable {
		return stillhousev1.PackagedReturnCondition_PACKAGED_RETURN_CONDITION_SALEABLE
	}
	return stillhousev1.PackagedReturnCondition_PACKAGED_RETURN_CONDITION_UNSALEABLE
}
