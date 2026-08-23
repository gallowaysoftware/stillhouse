package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type SalesService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewSalesService(db *tenantdb.DB, logger *slog.Logger) *SalesService {
	return &SalesService{db: db, logger: logger}
}

// ----- sales orders --------------------------------------------------------

func (s *SalesService) CreateSalesOrder(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateSalesOrderRequest],
) (*connect.Response[stillhousev1.CreateSalesOrderResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	customerID, err := uuid.Parse(in.GetCustomerId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("choose a customer"))
	}
	orderedOn, err := parseDateOrToday(in.GetOrderedOn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	requiredBy, err := parseOptionalDate(in.GetRequiredBy(), "required_by")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	priceListID, err := parseOptionalUUID(in.GetPriceListId(), "price_list_id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	locationID, err := parseOptionalUUID(in.GetLocationId(), "location_id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var out *stillhousev1.SalesOrder
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		cust, e := q.GetCustomer(ctx, customerID)
		if e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return connect.NewError(connect.CodeNotFound, errors.New("customer not found"))
			}
			return e
		}
		if cust.ArchivedAt.Valid {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("%s is archived — un-archive them before taking an order", cust.Name))
		}
		// The customer's own price list is the default, so a list attached
		// to an account is actually used rather than being a field somebody
		// filled in once.
		if !priceListID.Valid {
			priceListID = cust.PriceListID
		}
		if e := q.LockDocumentSequence(ctx, "sales_orders"); e != nil {
			return e
		}
		nextNo, e := q.NextSalesOrderNo(ctx)
		if e != nil {
			return e
		}
		so, e := q.CreateSalesOrder(ctx, sqlcgen.CreateSalesOrderParams{
			TenantID:          u.TenantID,
			CustomerID:        customerID,
			OrderNo:           nextNo,
			OrderedOn:         orderedOn,
			RequiredBy:        requiredBy,
			CustomerReference: in.GetCustomerReference(),
			PriceListID:       priceListID,
			LocationID:        locationID,
			Notes:             in.GetNotes(),
			CreatedBy:         u.ID,
		})
		if e != nil {
			return e
		}
		if e := audit.Write(ctx, q, u.TenantID, u.ID, "sales_order", so.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"order_no": so.OrderNo,
				"customer": cust.Name,
			}); e != nil {
			return e
		}
		out, e = hydrateSalesOrder(ctx, q, so)
		return e
	})
	if err != nil {
		return nil, s.fail("CreateSalesOrder", err)
	}
	return connect.NewResponse(&stillhousev1.CreateSalesOrderResponse{SalesOrder: out}), nil
}

func (s *SalesService) AddSalesOrderLine(
	ctx context.Context,
	req *connect.Request[stillhousev1.AddSalesOrderLineRequest],
) (*connect.Response[stillhousev1.AddSalesOrderLineResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	orderID, err := uuid.Parse(in.GetSalesOrderId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid sales_order_id"))
	}
	productID, err := uuid.Parse(in.GetProductId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("choose a product"))
	}
	if in.GetBottlesOrdered() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("bottles must be greater than zero"))
	}
	explicitPrice := strings.TrimSpace(in.GetUnitPrice())
	var price pgtype.Numeric
	if explicitPrice != "" {
		if err := price.Scan(explicitPrice); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("unit_price must be a decimal amount, e.g. 34.95"))
		}
	}

	var out *stillhousev1.SalesOrder
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		so, e := q.GetSalesOrder(ctx, orderID)
		if e != nil {
			return e
		}
		if e := assertOrderEditable(so); e != nil {
			return e
		}
		if _, e := q.GetProduct(ctx, productID); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return connect.NewError(connect.CodeNotFound, errors.New("product not found"))
			}
			return e
		}
		// An explicit price wins. A hand-agreed figure is a fact about
		// this order, not a mistake for the price list to correct.
		if explicitPrice == "" && so.PriceListID.Valid {
			entry, ee := q.GetPriceListEntryForProduct(ctx, sqlcgen.GetPriceListEntryForProductParams{
				PriceListID: so.PriceListID.UUID,
				ProductID:   productID,
			})
			switch {
			case ee == nil:
				price = entry.UnitPrice
			case errors.Is(ee, pgx.ErrNoRows):
				// A list that does not price this product prices it at
				// nothing here rather than guessing — the line is still
				// worth recording, and a zero is visible on the screen.
			default:
				return ee
			}
		}
		if !price.Valid {
			if e := price.Scan("0"); e != nil {
				return e
			}
		}
		if _, e = q.AddSalesOrderLine(ctx, sqlcgen.AddSalesOrderLineParams{
			TenantID:       u.TenantID,
			SalesOrderID:   orderID,
			ProductID:      productID,
			BottlesOrdered: in.GetBottlesOrdered(),
			UnitPrice:      price,
			Notes:          in.GetNotes(),
		}); e != nil {
			return e
		}
		out, e = hydrateSalesOrder(ctx, q, so)
		return e
	})
	if err != nil {
		return nil, s.fail("AddSalesOrderLine", err)
	}
	return connect.NewResponse(&stillhousev1.AddSalesOrderLineResponse{SalesOrder: out}), nil
}

func (s *SalesService) RemoveSalesOrderLine(
	ctx context.Context,
	req *connect.Request[stillhousev1.RemoveSalesOrderLineRequest],
) (*connect.Response[stillhousev1.RemoveSalesOrderLineResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	lineID, err := uuid.Parse(req.Msg.GetLineId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid line_id"))
	}
	var out *stillhousev1.SalesOrder
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		line, e := q.GetSalesOrderLineForUpdate(ctx, lineID)
		if e != nil {
			return e
		}
		so, e := q.GetSalesOrder(ctx, line.SalesOrderID)
		if e != nil {
			return e
		}
		if e := assertOrderEditable(so); e != nil {
			return e
		}
		// Stock that has already left cannot be un-ordered: the removal
		// is written and the return will carry it. Deleting the line here
		// would leave the shipment pointing at nothing.
		if line.BottlesShipped > 0 {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("%d bottles on this line have already shipped — reduce the quantity instead",
					line.BottlesShipped))
		}
		if e := q.DeleteSalesOrderLine(ctx, lineID); e != nil {
			return e
		}
		out, e = hydrateSalesOrder(ctx, q, so)
		return e
	})
	if err != nil {
		return nil, s.fail("RemoveSalesOrderLine", err)
	}
	return connect.NewResponse(&stillhousev1.RemoveSalesOrderLineResponse{SalesOrder: out}), nil
}

func (s *SalesService) SetSalesOrderStatus(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetSalesOrderStatusRequest],
) (*connect.Response[stillhousev1.SetSalesOrderStatusResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	orderID, err := uuid.Parse(in.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	status, err := salesOrderStatusToDB(in.GetStatus())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if status == sqlcgen.SalesOrderStatusCancelled && strings.TrimSpace(in.GetCancelReason()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say why the order was cancelled"))
	}

	var out *stillhousev1.SalesOrder
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		before, e := q.GetSalesOrder(ctx, orderID)
		if e != nil {
			return e
		}
		// Cancelling an order that has already put stock on a truck would
		// say the shipment never happened. It did; the return says so.
		if status == sqlcgen.SalesOrderStatusCancelled {
			counts, ce := q.SalesOrderOutstanding(ctx, orderID)
			if ce != nil {
				return ce
			}
			if counts.LinesStarted > 0 {
				return connect.NewError(connect.CodeFailedPrecondition,
					errors.New("part of this order has already shipped — close it instead of cancelling it"))
			}
		}
		if status == sqlcgen.SalesOrderStatusConfirmed {
			lines, le := q.ListSalesOrderLines(ctx, orderID)
			if le != nil {
				return le
			}
			if len(lines) == 0 {
				return connect.NewError(connect.CodeFailedPrecondition,
					errors.New("add a line before confirming the order"))
			}
		}
		so, e := q.SetSalesOrderStatus(ctx, sqlcgen.SetSalesOrderStatusParams{
			ID:           orderID,
			Status:       status,
			Actor:        uuid.NullUUID{UUID: u.ID, Valid: true},
			CancelReason: in.GetCancelReason(),
		})
		if e != nil {
			return e
		}
		if e := audit.Write(ctx, q, u.TenantID, u.ID, "sales_order", so.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"order_no": so.OrderNo,
				"from":     string(before.Status),
				"to":       string(so.Status),
				"reason":   so.CancelReason,
			}); e != nil {
			return e
		}
		out, e = hydrateSalesOrder(ctx, q, so)
		return e
	})
	if err != nil {
		return nil, s.fail("SetSalesOrderStatus", err)
	}
	return connect.NewResponse(&stillhousev1.SetSalesOrderStatusResponse{SalesOrder: out}), nil
}

func (s *SalesService) ListSalesOrders(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListSalesOrdersRequest],
) (*connect.Response[stillhousev1.ListSalesOrdersResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ListSalesOrdersRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListSalesOrders(ctx, req.Msg.GetOpenOnly())
		return e
	}); err != nil {
		return nil, s.fail("ListSalesOrders", err)
	}
	out := make([]*stillhousev1.SalesOrder, 0, len(rows))
	for _, r := range rows {
		so := salesOrderToProto(sqlcgen.SalesOrder{
			ID: r.ID, OrderNo: r.OrderNo, CustomerID: r.CustomerID, Status: r.Status,
			OrderedOn: r.OrderedOn, RequiredBy: r.RequiredBy,
			CustomerReference: r.CustomerReference, PriceListID: r.PriceListID,
			LocationID: r.LocationID, Notes: r.Notes, CancelReason: r.CancelReason,
			ConfirmedAt: r.ConfirmedAt, CreatedAt: r.CreatedAt,
		}, r.CustomerName)
		so.BottlesOrdered = r.BottlesOrdered
		so.BottlesShipped = r.BottlesShipped
		out = append(out, so)
	}
	return connect.NewResponse(&stillhousev1.ListSalesOrdersResponse{SalesOrders: out}), nil
}

func (s *SalesService) GetSalesOrder(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetSalesOrderRequest],
) (*connect.Response[stillhousev1.GetSalesOrderResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	var (
		out   *stillhousev1.SalesOrder
		ships []*stillhousev1.Shipment
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		so, e := q.GetSalesOrder(ctx, id)
		if e != nil {
			return e
		}
		if out, e = hydrateSalesOrder(ctx, q, so); e != nil {
			return e
		}
		rows, e := q.ListShipments(ctx, false)
		if e != nil {
			return e
		}
		for _, r := range rows {
			if !r.SalesOrderID.Valid || r.SalesOrderID.UUID != id {
				continue
			}
			sh, se := hydrateShipment(ctx, q, r.ID)
			if se != nil {
				return se
			}
			ships = append(ships, sh)
		}
		return nil
	})
	if err != nil {
		return nil, s.fail("GetSalesOrder", err)
	}
	return connect.NewResponse(&stillhousev1.GetSalesOrderResponse{
		SalesOrder: out,
		Shipments:  ships,
	}), nil
}

// ----- shipments -----------------------------------------------------------

func (s *SalesService) CreateShipment(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateShipmentRequest],
) (*connect.Response[stillhousev1.CreateShipmentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	orderID, err := parseOptionalUUID(in.GetSalesOrderId(), "sales_order_id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	locationID, err := parseOptionalUUID(in.GetLocationId(), "location_id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	shipDate, err := parseOptionalDate(in.GetShipDate(), "ship_date")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var customerID uuid.UUID
	if v := strings.TrimSpace(in.GetCustomerId()); v != "" {
		if customerID, err = uuid.Parse(v); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid customer_id"))
		}
	} else if !orderID.Valid {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("name a customer, or a sales order to take one from"))
	}

	var out *stillhousev1.Shipment
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if orderID.Valid {
			so, e := q.GetSalesOrder(ctx, orderID.UUID)
			if e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					return connect.NewError(connect.CodeNotFound, errors.New("sales order not found"))
				}
				return e
			}
			if so.Status == sqlcgen.SalesOrderStatusCancelled {
				return connect.NewError(connect.CodeFailedPrecondition,
					errors.New("that order was cancelled"))
			}
			// The order names the customer, and the customer decides
			// whether the removals this shipment writes carry duty. Taking
			// it from the request instead would let a pick be classified
			// differently from the order it satisfies.
			customerID = so.CustomerID
			if !locationID.Valid {
				locationID = so.LocationID
			}
		}
		cust, e := q.GetCustomer(ctx, customerID)
		if e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return connect.NewError(connect.CodeNotFound, errors.New("customer not found"))
			}
			return e
		}
		if cust.ArchivedAt.Valid {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("%s is archived", cust.Name))
		}
		if e := q.LockDocumentSequence(ctx, "shipments"); e != nil {
			return e
		}
		nextNo, e := q.NextShipmentNo(ctx)
		if e != nil {
			return e
		}
		sh, e := q.CreateShipment(ctx, sqlcgen.CreateShipmentParams{
			TenantID:     u.TenantID,
			SalesOrderID: orderID,
			CustomerID:   customerID,
			ShipmentNo:   nextNo,
			LocationID:   locationID,
			ShipDate:     shipDate,
			Carrier:      in.GetCarrier(),
			TrackingRef:  in.GetTrackingRef(),
			BolReference: in.GetBolReference(),
			Notes:        in.GetNotes(),
			CreatedBy:    u.ID,
		})
		if e != nil {
			return e
		}
		if e := audit.Write(ctx, q, u.TenantID, u.ID, "shipment", sh.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"shipment_no": sh.ShipmentNo,
				"customer":    cust.Name,
			}); e != nil {
			return e
		}
		out, e = hydrateShipment(ctx, q, sh.ID)
		return e
	})
	if err != nil {
		return nil, s.fail("CreateShipment", err)
	}
	return connect.NewResponse(&stillhousev1.CreateShipmentResponse{Shipment: out}), nil
}

func (s *SalesService) AddShipmentLine(
	ctx context.Context,
	req *connect.Request[stillhousev1.AddShipmentLineRequest],
) (*connect.Response[stillhousev1.AddShipmentLineResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	shipmentID, err := uuid.Parse(in.GetShipmentId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid shipment_id"))
	}
	piID, err := uuid.Parse(in.GetPackagedInventoryId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("choose a lot to pick from"))
	}
	if in.GetBottles() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("bottles must be greater than zero"))
	}
	orderLineID, err := parseOptionalUUID(in.GetSalesOrderLineId(), "sales_order_line_id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var out *stillhousev1.Shipment
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		sh, e := q.GetShipment(ctx, shipmentID)
		if e != nil {
			return e
		}
		if sh.Status != sqlcgen.ShipmentStatusPicking {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("this shipment has already gone — start another one"))
		}
		// Locked, so two pickers cannot both read the same unpicked count
		// and both pass the check below.
		lot, e := q.GetPackagedInventoryForUpdate(ctx, piID)
		if e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return connect.NewError(connect.CodeNotFound, errors.New("lot not found"))
			}
			return e
		}
		// Picked but not yet shipped, so the arithmetic has to count what
		// is already on this shipment and every other open one. The lot is
		// not decremented until it ships — see the migration header — so
		// on-hand alone would let two pickers promise the same bottles.
		picked, e := q.BottlesPickedFromLot(ctx, piID)
		if e != nil {
			return e
		}
		if lot.BottlesOnHand-picked < in.GetBottles() {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("only %d bottles of %s are unpicked (%d on hand, %d already on open shipments)",
					lot.BottlesOnHand-picked, lot.LotCode, lot.BottlesOnHand, picked))
		}
		// A hold is honoured here as well as at the removal, so a picker
		// finds out while the pallet is still in the warehouse rather than
		// when they try to close the shipment out.
		if lot.HeldAt.Valid {
			reason := lot.HoldReason
			if reason == "" {
				reason = "no reason recorded"
			}
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("%s is on hold: %s", lot.LotCode, reason))
		}
		if orderLineID.Valid {
			line, le := q.GetSalesOrderLineForUpdate(ctx, orderLineID.UUID)
			if le != nil {
				if errors.Is(le, pgx.ErrNoRows) {
					return connect.NewError(connect.CodeNotFound, errors.New("order line not found"))
				}
				return le
			}
			if !sh.SalesOrderID.Valid || line.SalesOrderID != sh.SalesOrderID.UUID {
				return connect.NewError(connect.CodeInvalidArgument,
					errors.New("that order line belongs to a different order"))
			}
			if line.ProductID != lot.ProductID {
				return connect.NewError(connect.CodeInvalidArgument,
					errors.New("that lot is a different product from the line it would satisfy"))
			}
		}
		if _, e = q.AddShipmentLine(ctx, sqlcgen.AddShipmentLineParams{
			TenantID:            u.TenantID,
			ShipmentID:          shipmentID,
			SalesOrderLineID:    orderLineID,
			PackagedInventoryID: piID,
			Bottles:             in.GetBottles(),
			Notes:               in.GetNotes(),
		}); e != nil {
			return e
		}
		out, e = hydrateShipment(ctx, q, shipmentID)
		return e
	})
	if err != nil {
		return nil, s.fail("AddShipmentLine", err)
	}
	return connect.NewResponse(&stillhousev1.AddShipmentLineResponse{Shipment: out}), nil
}

func (s *SalesService) RemoveShipmentLine(
	ctx context.Context,
	req *connect.Request[stillhousev1.RemoveShipmentLineRequest],
) (*connect.Response[stillhousev1.RemoveShipmentLineResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	lineID, err := uuid.Parse(req.Msg.GetLineId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid line_id"))
	}
	var out *stillhousev1.Shipment
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		line, e := q.GetShipmentLine(ctx, lineID)
		if e != nil {
			return e
		}
		sh, e := q.GetShipment(ctx, line.ShipmentID)
		if e != nil {
			return e
		}
		if sh.Status != sqlcgen.ShipmentStatusPicking {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("this shipment has already gone — void the removal instead"))
		}
		if e := q.DeleteShipmentLine(ctx, lineID); e != nil {
			return e
		}
		out, e = hydrateShipment(ctx, q, sh.ID)
		return e
	})
	if err != nil {
		return nil, s.fail("RemoveShipmentLine", err)
	}
	return connect.NewResponse(&stillhousev1.RemoveShipmentLineResponse{Shipment: out}), nil
}

// ShipShipment is the whole point of the track: the pick becomes the
// removals, in one transaction, at the moment the stock actually leaves.
//
// Everything a hand-recorded removal does happens here too, because it is
// literally the same code — the period lock, the release gate, the duty
// decision, the stock decrement, the audit entry. What is added is the
// link back: each shipment line records the removal it became, and each
// removal records the shipment it came from, so the return and the
// warehouse are two readings of one set of rows rather than two accounts
// that have to be reconciled.
func (s *SalesService) ShipShipment(
	ctx context.Context,
	req *connect.Request[stillhousev1.ShipShipmentRequest],
) (*connect.Response[stillhousev1.ShipShipmentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	shipmentID, err := uuid.Parse(in.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	reqDate, err := parseOptionalDate(in.GetShipDate(), "ship_date")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var (
		out      *stillhousev1.Shipment
		removals []*stillhousev1.PackagingRemoval
		dutyCAD  float64
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		// Locked before anything is read off it. Two clicks on Ship would
		// otherwise write two sets of removals against one pallet, and the
		// return would carry the duty twice.
		sh, e := q.GetShipmentForUpdate(ctx, shipmentID)
		if e != nil {
			return e
		}
		if sh.Status != sqlcgen.ShipmentStatusPicking {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("shipment %d is already %s", sh.ShipmentNo, sh.Status))
		}
		lines, e := q.ListShipmentLines(ctx, shipmentID)
		if e != nil {
			return e
		}
		if len(lines) == 0 {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("pick something before shipping"))
		}

		shipDate := reqDate
		if !shipDate.Valid {
			shipDate = sh.ShipDate
		}
		if !shipDate.Valid {
			shipDate, e = parseDateOrToday("")
			if e != nil {
				return e
			}
		}
		if _, e = q.SetShipmentShipDate(ctx, sqlcgen.SetShipmentShipDateParams{
			ID: shipmentID, ShipDate: shipDate,
		}); e != nil {
			return e
		}

		customerID := uuid.NullUUID{UUID: sh.CustomerID, Valid: true}
		reference := strings.TrimSpace(in.GetReference())
		if reference == "" {
			reference = orDefault(sh.BolReference, fmt.Sprintf("Shipment %d", sh.ShipmentNo))
		}
		for _, l := range lines {
			res, re := recordRemoval(ctx, q, u.TenantID, u.ID, removalInput{
				PackagedInventoryID: l.PackagedInventoryID,
				Bottles:             l.Bottles,
				RemovalDate:         shipDate,
				CustomerID:          customerID,
				Reference:           reference,
				Notes:               l.Notes,
			})
			if re != nil {
				return re
			}
			if e := q.LinkShipmentLineRemoval(ctx, sqlcgen.LinkShipmentLineRemovalParams{
				ID:                 l.ID,
				PackagingRemovalID: uuid.NullUUID{UUID: res.Removal.ID, Valid: true},
			}); e != nil {
				return e
			}
			if e := q.SetRemovalShipment(ctx, sqlcgen.SetRemovalShipmentParams{
				ID:         res.Removal.ID,
				ShipmentID: uuid.NullUUID{UUID: shipmentID, Valid: true},
			}); e != nil {
				return e
			}
			if l.SalesOrderLineID.Valid {
				if _, e := q.IncrementSalesOrderLineShipped(ctx,
					sqlcgen.IncrementSalesOrderLineShippedParams{
						ID:             l.SalesOrderLineID.UUID,
						BottlesShipped: l.Bottles,
					}); e != nil {
					return e
				}
			}
			dutyCAD += res.Removal.DutyAmountCad
			removals = append(removals,
				packagingRemovalToProto(res.Removal, res.Product, res.Package.LotCode, res.Package.Jurisdiction))
		}

		shipped, e := q.MarkShipmentShipped(ctx, sqlcgen.MarkShipmentShippedParams{
			ID:        shipmentID,
			ShippedBy: uuid.NullUUID{UUID: u.ID, Valid: true},
		})
		if errors.Is(e, pgx.ErrNoRows) {
			// Unreachable while the lock above is held.
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("this shipment has already gone"))
		}
		if e != nil {
			return e
		}
		// The order follows the stock. Nobody should have to remember to
		// close out an order whose last case just went on a truck.
		if err := advanceOrderAfterShipment(ctx, q, sh.SalesOrderID); err != nil {
			return err
		}
		if e := audit.Write(ctx, q, u.TenantID, u.ID, "shipment", shipped.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"shipment_no": shipped.ShipmentNo,
				"ship_date":   shipDate.Time.Format("2006-01-02"),
				"lines":       len(lines),
				"removals":    len(removals),
				"duty_cad":    dutyCAD,
			}); e != nil {
			return e
		}
		out, e = hydrateShipment(ctx, q, shipmentID)
		return e
	})
	if err != nil {
		return nil, s.fail("ShipShipment", err)
	}
	return connect.NewResponse(&stillhousev1.ShipShipmentResponse{
		Shipment: out,
		Removals: removals,
		DutyCad:  dutyCAD,
	}), nil
}

func (s *SalesService) CancelShipment(
	ctx context.Context,
	req *connect.Request[stillhousev1.CancelShipmentRequest],
) (*connect.Response[stillhousev1.CancelShipmentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	reason := strings.TrimSpace(req.Msg.GetReason())
	if reason == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say why the shipment was cancelled"))
	}
	var out *stillhousev1.Shipment
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		sh, e := q.CancelShipment(ctx, sqlcgen.CancelShipmentParams{ID: id, CancelReason: reason})
		if errors.Is(e, pgx.ErrNoRows) {
			// Only a picking shipment can be cancelled: once it has gone,
			// the removals exist and the way back is to void those.
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("that shipment has already gone — void its removals instead"))
		}
		if e != nil {
			return e
		}
		if e := audit.Write(ctx, q, u.TenantID, u.ID, "shipment", sh.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"shipment_no": sh.ShipmentNo,
				"cancelled":   true,
				"reason":      reason,
			}); e != nil {
			return e
		}
		out, e = hydrateShipment(ctx, q, sh.ID)
		return e
	})
	if err != nil {
		return nil, s.fail("CancelShipment", err)
	}
	return connect.NewResponse(&stillhousev1.CancelShipmentResponse{Shipment: out}), nil
}

func (s *SalesService) ListShipments(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListShipmentsRequest],
) (*connect.Response[stillhousev1.ListShipmentsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ListShipmentsRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListShipments(ctx, req.Msg.GetOpenOnly())
		return e
	}); err != nil {
		return nil, s.fail("ListShipments", err)
	}
	out := make([]*stillhousev1.Shipment, 0, len(rows))
	for _, r := range rows {
		sh := shipmentToProto(sqlcgen.Shipment{
			ID: r.ID, ShipmentNo: r.ShipmentNo, SalesOrderID: r.SalesOrderID,
			CustomerID: r.CustomerID, Status: r.Status, LocationID: r.LocationID,
			ShipDate: r.ShipDate, Carrier: r.Carrier, TrackingRef: r.TrackingRef,
			BolReference: r.BolReference, Notes: r.Notes, CancelReason: r.CancelReason,
			ShippedAt: r.ShippedAt, CreatedAt: r.CreatedAt,
		}, r.CustomerName, r.OrderNo)
		sh.Bottles = r.Bottles
		out = append(out, sh)
	}
	return connect.NewResponse(&stillhousev1.ListShipmentsResponse{Shipments: out}), nil
}

func (s *SalesService) GetShipment(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetShipmentRequest],
) (*connect.Response[stillhousev1.GetShipmentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	var out *stillhousev1.Shipment
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		out, e = hydrateShipment(ctx, q, id)
		return e
	})
	if err != nil {
		return nil, s.fail("GetShipment", err)
	}
	return connect.NewResponse(&stillhousev1.GetShipmentResponse{Shipment: out}), nil
}

// ListStockCommitments answers "can I promise this?" — on hand, spoken
// for, already picked, and what is genuinely free.
func (s *SalesService) ListStockCommitments(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListStockCommitmentsRequest],
) (*connect.Response[stillhousev1.ListStockCommitmentsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.StockCommitmentsRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.StockCommitments(ctx)
		return e
	}); err != nil {
		return nil, s.fail("ListStockCommitments", err)
	}
	out := make([]*stillhousev1.StockCommitment, 0, len(rows))
	for _, r := range rows {
		out = append(out, &stillhousev1.StockCommitment{
			ProductId:       r.ProductID.String(),
			ProductName:     r.ProductName,
			BottleSizeMl:    r.BottleSizeMl,
			BottleAbvPct:    r.TargetAbvPct,
			BottlesOnHand:   r.BottlesOnHand,
			BottlesReserved: r.BottlesReserved,
			BottlesPicked:   r.BottlesPicked,
			// Picked stock is committed to a specific shipment and is what
			// actually leaves; reservations are not subtracted because they
			// have not moved anything. Oversold says the difference out
			// loud instead of hiding it in an availability figure.
			BottlesAvailable: r.BottlesOnHand - r.BottlesPicked,
			Oversold:         r.BottlesReserved+r.BottlesPicked > r.BottlesOnHand,
		})
	}
	return connect.NewResponse(&stillhousev1.ListStockCommitmentsResponse{Commitments: out}), nil
}
