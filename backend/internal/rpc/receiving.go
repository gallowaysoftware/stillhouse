package rpc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// ReceiveAgainstPO turns a delivery into a material lot.
//
// This is the load-bearing part of the purchasing track, because the
// cost it writes is the one every downstream figure leans on: the
// accounting journal's inventory line, the bottling run's material cost,
// the price a bottle has to carry.
//
// Two things it does that the old standalone receipt could not.
//
// It matches. The line records what was ordered, so a short delivery is
// arithmetic rather than a discrepancy nobody spots — and the order's
// status follows the lines instead of being asserted by a person.
//
// And it absorbs. Freight, import duty and handling go into the lot's
// landed cost rather than into an expense account, spread across the
// quantity received. Without that, inventory value and cost of sales are
// understated by exactly what it cost to get the goods to the door,
// which for a distillery importing casks or barley is not a rounding
// error.
func (s *PurchasingService) ReceiveAgainstPO(
	ctx context.Context,
	req *connect.Request[stillhousev1.ReceiveAgainstPORequest],
) (*connect.Response[stillhousev1.ReceiveAgainstPOResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	lineID, err := uuid.Parse(in.GetPurchaseOrderLineId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid purchase_order_line_id"))
	}
	if in.GetQuantityReceived() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("quantity_received must be greater than zero"))
	}
	for name, v := range map[string]float64{
		"freight_cad": in.GetFreightCad(), "import_duty_cad": in.GetImportDutyCad(),
		"handling_cad": in.GetHandlingCad(),
	} {
		if v < 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("%s cannot be negative", name))
		}
	}
	receivedOn, err := parseDateOrToday(in.GetReceivedOn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("received_on must be YYYY-MM-DD"))
	}

	var (
		lot         sqlcgen.MaterialLot
		outstanding float64
		poID        uuid.UUID
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		// Locked before the outstanding quantity is read. Two receipts
		// against one line otherwise both see room and both increment —
		// the lost update fixed for bulk containers in stage 131.
		line, e := q.GetPurchaseOrderLineForUpdate(ctx, lineID)
		if e != nil {
			return e
		}
		poID = line.PurchaseOrderID
		po, e := q.GetPurchaseOrder(ctx, poID)
		if e != nil {
			return e
		}
		switch po.Status {
		case sqlcgen.PurchaseOrderStatusCancelled:
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("this order was cancelled"))
		case sqlcgen.PurchaseOrderStatusDraft:
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("this order has not been placed yet"))
		}

		// Over-receipt is allowed and reported rather than refused. A
		// supplier shipping 1,020 kg against a 1,000 kg line is an
		// ordinary Tuesday, and refusing it would mean the grain in the
		// yard is not in the system.
		remaining := line.QuantityOrdered - line.QuantityReceived
		outstanding = remaining - in.GetQuantityReceived()

		unitCost := pgtype.Float8{}
		if v := strings.TrimSpace(in.GetUnitPrice()); v != "" {
			var n pgtype.Numeric
			if err := n.Scan(v); err != nil {
				return connect.NewError(connect.CodeInvalidArgument,
					errors.New("unit_price must be a decimal amount"))
			}
			unitCost = pgtype.Float8{Float64: numericToFloat(n), Valid: true}
		} else if f := numericToFloat(line.UnitPrice); f > 0 {
			// The agreed price, unless the delivery came at a different
			// one. A line priced at zero stays unpriced rather than
			// becoming a free lot.
			unitCost = pgtype.Float8{Float64: f, Valid: true}
		}

		lot, e = q.CreateMaterialLotFromReceipt(ctx, sqlcgen.CreateMaterialLotFromReceiptParams{
			TenantID: u.TenantID, MaterialID: line.MaterialID,
			SupplierLot:         in.GetSupplierLot(),
			QuantityReceived:    in.GetQuantityReceived(),
			ReceivedAt:          pgtype.Timestamptz{Valid: true, Time: receivedOn.Time},
			Notes:               in.GetNotes(),
			UnitCostCad:         unitCost,
			PurchaseOrderLineID: uuid.NullUUID{UUID: lineID, Valid: true},
			SupplierID:          uuid.NullUUID{UUID: po.SupplierID, Valid: true},
			FreightCad:          in.GetFreightCad(),
			ImportDutyCad:       in.GetImportDutyCad(),
			HandlingCad:         in.GetHandlingCad(),
			InvoiceReference:    in.GetInvoiceReference(),
		})
		if e != nil {
			return e
		}
		if _, e := q.IncrementPurchaseOrderLineReceived(ctx,
			sqlcgen.IncrementPurchaseOrderLineReceivedParams{
				ID: lineID, QuantityReceived: in.GetQuantityReceived(),
			}); e != nil {
			return e
		}

		// The status follows the lines. Letting somebody declare an order
		// received while it is short would make the outstanding view a
		// matter of opinion.
		counts, e := q.PurchaseOrderOutstanding(ctx, poID)
		if e != nil {
			return e
		}
		next := sqlcgen.PurchaseOrderStatusPartiallyReceived
		if counts.LinesOutstanding == 0 {
			next = sqlcgen.PurchaseOrderStatusReceived
		}
		if _, e := q.SetPurchaseOrderStatus(ctx, sqlcgen.SetPurchaseOrderStatusParams{
			ID: poID, Status: next,
		}); e != nil {
			return e
		}

		return audit.Write(ctx, q, u.TenantID, u.ID, "material_lot", lot.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"event":                "received_against_po",
				"po_no":                po.PoNo,
				"quantity":             in.GetQuantityReceived(),
				"unit_cost_cad":        unitCost.Float64,
				"freight_cad":          in.GetFreightCad(),
				"duty_cad":             in.GetImportDutyCad(),
				"handling_cad":         in.GetHandlingCad(),
				"landed_unit_cost_cad": lot.LandedUnitCostCad.Float64,
				// Negative means more arrived than was ordered.
				"quantity_outstanding": outstanding,
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("purchase order line not found"))
		}
		if ce := classifyWriteErr(err, "that line or material no longer exists"); ce != nil {
			return nil, ce
		}
		s.logger.Error("ReceiveAgainstPO", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	full, err := s.readPO(ctx, u.TenantID, poID)
	if err != nil {
		s.logger.Error("ReceiveAgainstPO: reread", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.ReceiveAgainstPOResponse{
		MaterialLotId:       lot.ID.String(),
		LandedUnitCostCad:   lot.LandedUnitCostCad.Float64,
		LandedUnitCostKnown: lot.LandedUnitCostCad.Valid,
		QuantityOutstanding: outstanding,
		PurchaseOrder:       full,
	}), nil
}

// SetLandedCharges updates the charges on a lot already received.
//
// Charges routinely arrive after the goods — a freight invoice a week
// later, a customs entry after that. Updating the lot is correct rather
// than a fudge: what it cost to get here did not change, only when we
// learned it. Everything computed from the landed cost recomputes,
// because it is a generated column rather than a figure somebody
// maintains.
func (s *PurchasingService) SetLandedCharges(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetLandedChargesRequest],
) (*connect.Response[stillhousev1.SetLandedChargesResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	id, err := uuid.Parse(in.GetMaterialLotId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid material_lot_id"))
	}
	for name, v := range map[string]float64{
		"freight_cad": in.GetFreightCad(), "import_duty_cad": in.GetImportDutyCad(),
		"handling_cad": in.GetHandlingCad(),
	} {
		if v < 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("%s cannot be negative", name))
		}
	}

	var lot sqlcgen.MaterialLot
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		lot, e = q.SetMaterialLotLandedCharges(ctx, sqlcgen.SetMaterialLotLandedChargesParams{
			ID: id, FreightCad: in.GetFreightCad(),
			ImportDutyCad: in.GetImportDutyCad(), HandlingCad: in.GetHandlingCad(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "material_lot", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event": "landed_charges", "freight_cad": in.GetFreightCad(),
				"duty_cad": in.GetImportDutyCad(), "handling_cad": in.GetHandlingCad(),
				"landed_unit_cost_cad": lot.LandedUnitCostCad.Float64,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("material lot not found"))
		}
		s.logger.Error("SetLandedCharges", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SetLandedChargesResponse{
		MaterialLotId:       id.String(),
		LandedUnitCostCad:   lot.LandedUnitCostCad.Float64,
		LandedUnitCostKnown: lot.LandedUnitCostCad.Valid,
	}), nil
}

func (s *PurchasingService) MarkLotInvoiced(
	ctx context.Context,
	req *connect.Request[stillhousev1.MarkLotInvoicedRequest],
) (*connect.Response[stillhousev1.MarkLotInvoicedResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetMaterialLotId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid material_lot_id"))
	}
	ref := strings.TrimSpace(req.Msg.GetInvoiceReference())
	if ref == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("the invoice reference is the point — without it nothing links the "+
				"receipt to the bill"))
	}
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if _, e := q.MarkMaterialLotInvoiced(ctx, sqlcgen.MarkMaterialLotInvoicedParams{
			ID: id, InvoiceReference: ref,
		}); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "material_lot", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event": "invoiced", "invoice_reference": ref,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("material lot not found"))
		}
		s.logger.Error("MarkLotInvoiced", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.MarkLotInvoicedResponse{
		MaterialLotId: id.String(),
	}), nil
}

// ListGRNI is what has arrived and not yet been billed — the one report
// a monthly close actually needs out of receiving.
func (s *PurchasingService) ListGRNI(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListGRNIRequest],
) (*connect.Response[stillhousev1.ListGRNIResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ListGoodsReceivedNotInvoicedRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListGoodsReceivedNotInvoiced(ctx)
		return e
	}); err != nil {
		s.logger.Error("ListGRNI", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := &stillhousev1.ListGRNIResponse{}
	for _, r := range rows {
		landed := r.UnitCostCad.Float64
		if r.LandedUnitCostCad.Valid {
			landed = r.LandedUnitCostCad.Float64
		}
		value := landed * r.QuantityReceived
		out.TotalValueCad += value
		out.Lines = append(out.Lines, &stillhousev1.GRNILine{
			MaterialLotId:     r.ID.String(),
			MaterialName:      r.MaterialName,
			SupplierName:      r.SupplierName,
			SupplierLot:       r.SupplierLot,
			Quantity:          r.QuantityReceived,
			UnitCostCad:       r.UnitCostCad.Float64,
			LandedUnitCostCad: landed,
			ValueCad:          value,
			ReceivedOn:        r.ReceivedAt.Time.Format("2006-01-02"),
		})
	}
	return connect.NewResponse(out), nil
}
