package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// PurchasingService owns suppliers, purchase orders and receiving.
//
// The receiving path is the one that matters. It is where a delivery
// becomes a material lot with a cost that everything downstream — the
// accounting journal, the bottling-run cost, the price a bottle has to
// carry — will lean on, and where freight and duty get absorbed into
// that cost rather than left in an expense account.
type PurchasingService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewPurchasingService(db *tenantdb.DB, logger *slog.Logger) *PurchasingService {
	return &PurchasingService{db: db, logger: logger}
}

func (s *PurchasingService) SaveSupplier(
	ctx context.Context,
	req *connect.Request[stillhousev1.SaveSupplierRequest],
) (*connect.Response[stillhousev1.SaveSupplierResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	name := strings.TrimSpace(in.GetName())
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	var terms pgtype.Int4
	if in.GetPaymentTermsDays() >= 0 {
		terms = pgtype.Int4{Int32: in.GetPaymentTermsDays(), Valid: true}
	}

	var row sqlcgen.Supplier
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		if in.GetId() == "" {
			row, e = q.CreateSupplier(ctx, sqlcgen.CreateSupplierParams{
				TenantID: u.TenantID, Name: name,
				AccountReference: in.GetAccountReference(), ContactName: in.GetContactName(),
				Email: in.GetEmail(), Phone: in.GetPhone(), Address: in.GetAddress(),
				PaymentTermsDays: terms, Country: in.GetCountry(), Notes: in.GetNotes(),
			})
			return e
		}
		id, pe := uuid.Parse(in.GetId())
		if pe != nil {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
		}
		var archived pgtype.Timestamptz
		if in.GetArchived() {
			archived = pgtype.Timestamptz{Valid: true, Time: time.Now().UTC()}
		}
		row, e = q.UpdateSupplier(ctx, sqlcgen.UpdateSupplierParams{
			ID: id, Name: name,
			AccountReference: in.GetAccountReference(), ContactName: in.GetContactName(),
			Email: in.GetEmail(), Phone: in.GetPhone(), Address: in.GetAddress(),
			PaymentTermsDays: terms, Country: in.GetCountry(), Notes: in.GetNotes(),
			ArchivedAt: archived,
		})
		return e
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("supplier not found"))
		}
		if ce := classifyWriteErr(err, "the tenant no longer exists"); ce != nil {
			return nil, ce
		}
		s.logger.Error("SaveSupplier", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SaveSupplierResponse{Supplier: supplierToProto(row)}), nil
}

func (s *PurchasingService) ListSuppliers(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListSuppliersRequest],
) (*connect.Response[stillhousev1.ListSuppliersResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.Supplier
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListSuppliers(ctx, req.Msg.GetIncludeArchived())
		return e
	}); err != nil {
		s.logger.Error("ListSuppliers", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.Supplier, 0, len(rows))
	for _, r := range rows {
		out = append(out, supplierToProto(r))
	}
	return connect.NewResponse(&stillhousev1.ListSuppliersResponse{Suppliers: out}), nil
}

func (s *PurchasingService) CreatePurchaseOrder(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreatePurchaseOrderRequest],
) (*connect.Response[stillhousev1.CreatePurchaseOrderResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	supplierID, err := uuid.Parse(in.GetSupplierId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("choose a supplier"))
	}
	orderedOn, err := parseOptionalDate(in.GetOrderedOn(), "ordered_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	expectedOn, err := parseOptionalDate(in.GetExpectedOn(), "expected_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	currency := strings.ToUpper(strings.TrimSpace(in.GetCurrency()))
	if currency == "" {
		currency = "CAD"
	}

	var po sqlcgen.PurchaseOrder
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		// Serialise the number allocation, same shape as every other
		// document counter here — two orders raised at the same moment
		// otherwise claim the same po_no and one dies on the UNIQUE.
		if e := q.LockDocumentSequence(ctx, "purchase_orders"); e != nil {
			return e
		}
		next, e := q.NextPurchaseOrderNo(ctx)
		if e != nil {
			return e
		}
		po, e = q.CreatePurchaseOrder(ctx, sqlcgen.CreatePurchaseOrderParams{
			TenantID: u.TenantID, SupplierID: supplierID, PoNo: next,
			OrderedOn: orderedOn, ExpectedOn: expectedOn,
			Reference: in.GetReference(), Currency: currency, Notes: in.GetNotes(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "purchase_order", po.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{"po_no": po.PoNo})
	})
	if err != nil {
		if ce := classifyWriteErr(err, "that supplier no longer exists"); ce != nil {
			return nil, ce
		}
		s.logger.Error("CreatePurchaseOrder", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	full, err := s.readPO(ctx, u.TenantID, po.ID)
	if err != nil {
		s.logger.Error("CreatePurchaseOrder: reread", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.CreatePurchaseOrderResponse{PurchaseOrder: full}), nil
}

func (s *PurchasingService) AddPurchaseOrderLine(
	ctx context.Context,
	req *connect.Request[stillhousev1.AddPurchaseOrderLineRequest],
) (*connect.Response[stillhousev1.AddPurchaseOrderLineResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	poID, err := uuid.Parse(in.GetPurchaseOrderId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid purchase_order_id"))
	}
	materialID, err := uuid.Parse(in.GetMaterialId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("choose a material"))
	}
	if in.GetQuantityOrdered() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("quantity must be greater than zero"))
	}
	var price pgtype.Numeric
	if err := price.Scan(strings.TrimSpace(orDefault(in.GetUnitPrice(), "0"))); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("unit_price must be a decimal amount, e.g. 0.55"))
	}

	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		po, e := q.GetPurchaseOrder(ctx, poID)
		if e != nil {
			return e
		}
		// Lines are editable while the order is a draft. Once it has been
		// placed the supplier has a copy, and changing it here without
		// changing it there is how two parties end up with different
		// documents.
		if po.Status != sqlcgen.PurchaseOrderStatusDraft {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("this order has been placed; cancel it or raise another rather than "+
					"editing a document the supplier already has"))
		}
		_, e = q.AddPurchaseOrderLine(ctx, sqlcgen.AddPurchaseOrderLineParams{
			TenantID: u.TenantID, PurchaseOrderID: poID, MaterialID: materialID,
			QuantityOrdered: in.GetQuantityOrdered(), UnitPrice: price,
			Uom: in.GetUom(), Notes: in.GetNotes(),
		})
		return e
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("purchase order not found"))
		}
		if ce := classifyWriteErr(err, "that material no longer exists"); ce != nil {
			return nil, ce
		}
		s.logger.Error("AddPurchaseOrderLine", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	full, err := s.readPO(ctx, u.TenantID, poID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.AddPurchaseOrderLineResponse{PurchaseOrder: full}), nil
}

func (s *PurchasingService) RemovePurchaseOrderLine(
	ctx context.Context,
	req *connect.Request[stillhousev1.RemovePurchaseOrderLineRequest],
) (*connect.Response[stillhousev1.RemovePurchaseOrderLineResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	lineID, err := uuid.Parse(req.Msg.GetLineId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid line_id"))
	}
	var poID uuid.UUID
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		line, e := q.GetPurchaseOrderLineForUpdate(ctx, lineID)
		if e != nil {
			return e
		}
		poID = line.PurchaseOrderID
		po, e := q.GetPurchaseOrder(ctx, poID)
		if e != nil {
			return e
		}
		if po.Status != sqlcgen.PurchaseOrderStatusDraft {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("this order has been placed"))
		}
		if line.QuantityReceived > 0 {
			// Belt to the draft check's braces: a line that has been
			// received against is history, not a typo.
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("something has already been received against this line"))
		}
		return q.DeletePurchaseOrderLine(ctx, lineID)
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("line not found"))
		}
		s.logger.Error("RemovePurchaseOrderLine", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	full, err := s.readPO(ctx, u.TenantID, poID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.RemovePurchaseOrderLineResponse{PurchaseOrder: full}), nil
}

func (s *PurchasingService) SetPurchaseOrderStatus(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetPurchaseOrderStatusRequest],
) (*connect.Response[stillhousev1.SetPurchaseOrderStatusResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	status, err := poStatusToDB(req.Msg.GetStatus())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// The received states follow the lines, not a person. Letting
	// somebody declare an order received while it is short would make
	// the outstanding view a matter of opinion.
	if status == sqlcgen.PurchaseOrderStatusPartiallyReceived ||
		status == sqlcgen.PurchaseOrderStatusReceived {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("receiving states are set by receiving, not by hand"))
	}
	if status == sqlcgen.PurchaseOrderStatusCancelled &&
		strings.TrimSpace(req.Msg.GetCancelReason()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say why it is being cancelled"))
	}

	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		row, e := q.SetPurchaseOrderStatus(ctx, sqlcgen.SetPurchaseOrderStatusParams{
			ID: id, Status: status,
			PlacedBy:     uuid.NullUUID{UUID: u.ID, Valid: status == sqlcgen.PurchaseOrderStatusPlaced},
			CancelReason: req.Msg.GetCancelReason(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "purchase_order", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"po_no": row.PoNo, "status": string(row.Status),
				"cancel_reason": row.CancelReason,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("purchase order not found"))
		}
		s.logger.Error("SetPurchaseOrderStatus", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	full, err := s.readPO(ctx, u.TenantID, id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SetPurchaseOrderStatusResponse{PurchaseOrder: full}), nil
}

func (s *PurchasingService) ListPurchaseOrders(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListPurchaseOrdersRequest],
) (*connect.Response[stillhousev1.ListPurchaseOrdersResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ListPurchaseOrdersRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListPurchaseOrders(ctx, req.Msg.GetOpenOnly())
		return e
	}); err != nil {
		s.logger.Error("ListPurchaseOrders", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.PurchaseOrder, 0, len(rows))
	for _, r := range rows {
		po := poToProto(sqlcgen.PurchaseOrder{
			ID: r.ID, PoNo: r.PoNo, SupplierID: r.SupplierID, Status: r.Status,
			OrderedOn: r.OrderedOn, ExpectedOn: r.ExpectedOn, Reference: r.Reference,
			Currency: r.Currency, Notes: r.Notes, CancelReason: r.CancelReason,
			PlacedAt: r.PlacedAt,
		}, r.SupplierName, nil)
		po.TotalValue = numericToDecimalString(r.TotalValue)
		out = append(out, po)
	}
	return connect.NewResponse(&stillhousev1.ListPurchaseOrdersResponse{PurchaseOrders: out}), nil
}

func (s *PurchasingService) GetPurchaseOrder(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetPurchaseOrderRequest],
) (*connect.Response[stillhousev1.GetPurchaseOrderResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	full, err := s.readPO(ctx, u.TenantID, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("purchase order not found"))
		}
		s.logger.Error("GetPurchaseOrder", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.GetPurchaseOrderResponse{PurchaseOrder: full}), nil
}

func (s *PurchasingService) readPO(
	ctx context.Context, tenantID, id uuid.UUID,
) (*stillhousev1.PurchaseOrder, error) {
	var (
		po       sqlcgen.PurchaseOrder
		supplier sqlcgen.Supplier
		lines    []sqlcgen.ListPurchaseOrderLinesRow
	)
	err := s.db.WithTenantTx(ctx, tenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		po, e = q.GetPurchaseOrder(ctx, id)
		if e != nil {
			return e
		}
		supplier, e = q.GetSupplier(ctx, po.SupplierID)
		if e != nil {
			return e
		}
		lines, e = q.ListPurchaseOrderLines(ctx, id)
		return e
	})
	if err != nil {
		return nil, err
	}
	return poToProto(po, supplier.Name, lines), nil
}

func supplierToProto(s sqlcgen.Supplier) *stillhousev1.Supplier {
	out := &stillhousev1.Supplier{
		Id:               s.ID.String(),
		Name:             s.Name,
		AccountReference: s.AccountReference,
		ContactName:      s.ContactName,
		Email:            s.Email,
		Phone:            s.Phone,
		Address:          s.Address,
		// -1 rather than 0: "no terms recorded" and "due on receipt" are
		// different statements and a zero would collapse them.
		PaymentTermsDays: -1,
		Country:          s.Country,
		Notes:            s.Notes,
	}
	if s.PaymentTermsDays.Valid {
		out.PaymentTermsDays = s.PaymentTermsDays.Int32
	}
	if s.ArchivedAt.Valid {
		out.ArchivedAt = timestamppb.New(s.ArchivedAt.Time)
	}
	return out
}

func poToProto(
	po sqlcgen.PurchaseOrder, supplierName string, lines []sqlcgen.ListPurchaseOrderLinesRow,
) *stillhousev1.PurchaseOrder {
	out := &stillhousev1.PurchaseOrder{
		Id:           po.ID.String(),
		PoNo:         po.PoNo,
		SupplierId:   po.SupplierID.String(),
		SupplierName: supplierName,
		Status:       poStatusToProto(po.Status),
		OrderedOn:    formatDate(po.OrderedOn),
		ExpectedOn:   formatDate(po.ExpectedOn),
		Reference:    po.Reference,
		Currency:     po.Currency,
		Notes:        po.Notes,
		CancelReason: po.CancelReason,
	}
	if po.PlacedAt.Valid {
		out.PlacedAt = timestamppb.New(po.PlacedAt.Time)
	}
	total := 0.0
	for _, l := range lines {
		price := numericToDecimalString(l.UnitPrice)
		out.Lines = append(out.Lines, &stillhousev1.PurchaseOrderLine{
			Id:               l.ID.String(),
			MaterialId:       l.MaterialID.String(),
			MaterialName:     l.MaterialName,
			QuantityOrdered:  l.QuantityOrdered,
			QuantityReceived: l.QuantityReceived,
			UnitPrice:        price,
			Uom:              orDefault(l.Uom, l.MaterialUom),
			Notes:            l.Notes,
		})
		total += l.QuantityOrdered * numericToFloat(l.UnitPrice)
	}
	if lines != nil {
		out.TotalValue = fmt.Sprintf("%.2f", total)
	}
	return out
}

func poStatusToDB(s stillhousev1.PurchaseOrderStatus) (sqlcgen.PurchaseOrderStatus, error) {
	switch s {
	case stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_DRAFT:
		return sqlcgen.PurchaseOrderStatusDraft, nil
	case stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_PLACED:
		return sqlcgen.PurchaseOrderStatusPlaced, nil
	case stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_PARTIALLY_RECEIVED:
		return sqlcgen.PurchaseOrderStatusPartiallyReceived, nil
	case stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_RECEIVED:
		return sqlcgen.PurchaseOrderStatusReceived, nil
	case stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_CLOSED:
		return sqlcgen.PurchaseOrderStatusClosed, nil
	case stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_CANCELLED:
		return sqlcgen.PurchaseOrderStatusCancelled, nil
	}
	return "", errors.New("invalid purchase order status")
}

func poStatusToProto(s sqlcgen.PurchaseOrderStatus) stillhousev1.PurchaseOrderStatus {
	switch s {
	case sqlcgen.PurchaseOrderStatusDraft:
		return stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_DRAFT
	case sqlcgen.PurchaseOrderStatusPlaced:
		return stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_PLACED
	case sqlcgen.PurchaseOrderStatusPartiallyReceived:
		return stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_PARTIALLY_RECEIVED
	case sqlcgen.PurchaseOrderStatusReceived:
		return stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_RECEIVED
	case sqlcgen.PurchaseOrderStatusClosed:
		return stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_CLOSED
	case sqlcgen.PurchaseOrderStatusCancelled:
		return stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_CANCELLED
	}
	return stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_UNSPECIFIED
}

// numericToFloat is for arithmetic on a total that is then rendered to
// two places. The stored values stay NUMERIC; this is a display path,
// not a place a cent can go missing into a ledger.
func numericToFloat(n pgtype.Numeric) float64 {
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
