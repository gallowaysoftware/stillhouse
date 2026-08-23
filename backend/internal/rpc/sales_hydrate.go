package rpc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// fail maps a transaction error onto a wire error, preserving anything the
// transaction already phrased for an operator and hiding anything it did not.
func (s *SalesService) fail(op string, err error) error {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}
	s.logger.Error(op, "err", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}

// assertOrderEditable refuses edits to an order that is no longer a
// proposal. Once it is confirmed the customer has a copy; once anything
// has shipped the return has one.
func assertOrderEditable(so sqlcgen.SalesOrder) error {
	switch so.Status {
	case sqlcgen.SalesOrderStatusDraft, sqlcgen.SalesOrderStatusConfirmed:
		return nil
	default:
		return connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("order %d is %s and can no longer be edited", so.OrderNo, so.Status))
	}
}

// advanceOrderAfterShipment moves an order to partially shipped or shipped
// according to what is still owed on its lines. It never moves an order
// backwards and never touches one that has been cancelled or closed by hand.
func advanceOrderAfterShipment(ctx context.Context, q *sqlcgen.Queries, orderID uuid.NullUUID) error {
	if !orderID.Valid {
		return nil
	}
	so, err := q.GetSalesOrder(ctx, orderID.UUID)
	if err != nil {
		return err
	}
	switch so.Status {
	case sqlcgen.SalesOrderStatusDraft, sqlcgen.SalesOrderStatusConfirmed,
		sqlcgen.SalesOrderStatusPartiallyShipped:
	default:
		return nil
	}
	counts, err := q.SalesOrderOutstanding(ctx, orderID.UUID)
	if err != nil {
		return err
	}
	next := sqlcgen.SalesOrderStatusPartiallyShipped
	if counts.LinesOutstanding == 0 {
		next = sqlcgen.SalesOrderStatusShipped
	}
	if next == so.Status {
		return nil
	}
	_, err = q.SetSalesOrderStatus(ctx, sqlcgen.SetSalesOrderStatusParams{
		ID:     orderID.UUID,
		Status: next,
	})
	return err
}

func hydrateSalesOrder(
	ctx context.Context, q *sqlcgen.Queries, so sqlcgen.SalesOrder,
) (*stillhousev1.SalesOrder, error) {
	cust, err := q.GetCustomer(ctx, so.CustomerID)
	if err != nil {
		return nil, err
	}
	out := salesOrderToProto(so, cust.Name)
	lines, err := q.ListSalesOrderLines(ctx, so.ID)
	if err != nil {
		return nil, err
	}
	total := 0.0
	for _, l := range lines {
		out.Lines = append(out.Lines, &stillhousev1.SalesOrderLine{
			Id:             l.ID.String(),
			ProductId:      l.ProductID.String(),
			ProductName:    l.ProductName,
			BottleSizeMl:   l.BottleSizeMl,
			BottleAbvPct:   l.TargetAbvPct,
			BottlesOrdered: l.BottlesOrdered,
			BottlesShipped: l.BottlesShipped,
			UnitPrice:      numericToDecimalString(l.UnitPrice),
			Notes:          l.Notes,
		})
		out.BottlesOrdered += l.BottlesOrdered
		out.BottlesShipped += l.BottlesShipped
		total += float64(l.BottlesOrdered) * numericToFloat(l.UnitPrice)
	}
	out.TotalValue = fmt.Sprintf("%.2f", total)
	return out, nil
}

func salesOrderToProto(so sqlcgen.SalesOrder, customerName string) *stillhousev1.SalesOrder {
	out := &stillhousev1.SalesOrder{
		Id:                so.ID.String(),
		OrderNo:           so.OrderNo,
		CustomerId:        so.CustomerID.String(),
		CustomerName:      customerName,
		Status:            salesOrderStatusToProto(so.Status),
		OrderedOn:         formatDate(so.OrderedOn),
		RequiredBy:        formatDate(so.RequiredBy),
		CustomerReference: so.CustomerReference,
		PriceListId:       nullUUIDString(so.PriceListID),
		LocationId:        nullUUIDString(so.LocationID),
		Notes:             so.Notes,
		CancelReason:      so.CancelReason,
	}
	if so.ConfirmedAt.Valid {
		out.ConfirmedAt = timestamppb.New(so.ConfirmedAt.Time)
	}
	if so.CreatedAt.Valid {
		out.CreatedAt = timestamppb.New(so.CreatedAt.Time)
	}
	return out
}

func hydrateShipment(
	ctx context.Context, q *sqlcgen.Queries, id uuid.UUID,
) (*stillhousev1.Shipment, error) {
	sh, err := q.GetShipment(ctx, id)
	if err != nil {
		return nil, err
	}
	cust, err := q.GetCustomer(ctx, sh.CustomerID)
	if err != nil {
		return nil, err
	}
	orderNo := int32(0)
	if sh.SalesOrderID.Valid {
		so, e := q.GetSalesOrder(ctx, sh.SalesOrderID.UUID)
		if e != nil {
			return nil, e
		}
		orderNo = so.OrderNo
	}
	out := shipmentToProto(sh, cust.Name, orderNo)
	out.CustomerAddress = cust.Address
	out.CustomerLicenceNumber = cust.LicenceNumber
	out.CustomerJurisdiction = cust.Jurisdiction
	lines, err := q.ListShipmentLines(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		line := &stillhousev1.ShipmentLine{
			Id:                  l.ID.String(),
			SalesOrderLineId:    nullUUIDString(l.SalesOrderLineID),
			PackagedInventoryId: l.PackagedInventoryID.String(),
			LotCode:             l.LotCode,
			Jurisdiction:        l.Jurisdiction,
			ProductName:         l.ProductName,
			BottleSizeMl:        l.BottleSizeMl,
			BottleAbvPct:        l.TargetAbvPct,
			Bottles:             l.Bottles,
			BottlesOnHand:       l.BottlesOnHand,
			Released:            l.ReleasedAt.Valid,
			OnHold:              l.HeldAt.Valid,
			PackagingRemovalId:  nullUUIDString(l.PackagingRemovalID),
			Notes:               l.Notes,
		}
		if l.PackagingRemovalID.Valid {
			r, e := q.GetRemoval(ctx, l.PackagingRemovalID.UUID)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return nil, e
			}
			if e == nil {
				line.RemovalNo = r.RemovalNo
			}
		}
		out.Lines = append(out.Lines, line)
		out.Bottles += l.Bottles
		litres := float64(l.Bottles) * float64(l.BottleSizeMl) / 1000
		out.TotalLitres += litres
		out.TotalLaa += litres * l.TargetAbvPct / 100
	}
	return out, nil
}

func shipmentToProto(
	sh sqlcgen.Shipment, customerName string, orderNo int32,
) *stillhousev1.Shipment {
	out := &stillhousev1.Shipment{
		Id:           sh.ID.String(),
		ShipmentNo:   sh.ShipmentNo,
		SalesOrderId: nullUUIDString(sh.SalesOrderID),
		OrderNo:      orderNo,
		CustomerId:   sh.CustomerID.String(),
		CustomerName: customerName,
		Status:       shipmentStatusToProto(sh.Status),
		LocationId:   nullUUIDString(sh.LocationID),
		ShipDate:     formatDate(sh.ShipDate),
		Carrier:      sh.Carrier,
		TrackingRef:  sh.TrackingRef,
		BolReference: sh.BolReference,
		Notes:        sh.Notes,
		CancelReason: sh.CancelReason,
	}
	if sh.ShippedAt.Valid {
		out.ShippedAt = timestamppb.New(sh.ShippedAt.Time)
	}
	if sh.CreatedAt.Valid {
		out.CreatedAt = timestamppb.New(sh.CreatedAt.Time)
	}
	return out
}

func salesOrderStatusToProto(s sqlcgen.SalesOrderStatus) stillhousev1.SalesOrderStatus {
	switch s {
	case sqlcgen.SalesOrderStatusDraft:
		return stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_DRAFT
	case sqlcgen.SalesOrderStatusConfirmed:
		return stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_CONFIRMED
	case sqlcgen.SalesOrderStatusPartiallyShipped:
		return stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_PARTIALLY_SHIPPED
	case sqlcgen.SalesOrderStatusShipped:
		return stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_SHIPPED
	case sqlcgen.SalesOrderStatusClosed:
		return stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_CLOSED
	case sqlcgen.SalesOrderStatusCancelled:
		return stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_CANCELLED
	default:
		return stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_UNSPECIFIED
	}
}

func salesOrderStatusToDB(s stillhousev1.SalesOrderStatus) (sqlcgen.SalesOrderStatus, error) {
	switch s {
	case stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_DRAFT:
		return sqlcgen.SalesOrderStatusDraft, nil
	case stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_CONFIRMED:
		return sqlcgen.SalesOrderStatusConfirmed, nil
	case stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_CLOSED:
		return sqlcgen.SalesOrderStatusClosed, nil
	case stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_CANCELLED:
		return sqlcgen.SalesOrderStatusCancelled, nil
	case stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_PARTIALLY_SHIPPED,
		stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_SHIPPED:
		// Shipping states are reached by shipping. Letting them be set by
		// hand would let an order claim stock left the building when no
		// removal says so.
		return "", errors.New("shipping status follows the shipments — ship the order instead")
	default:
		return "", errors.New("choose a status")
	}
}

func shipmentStatusToProto(s sqlcgen.ShipmentStatus) stillhousev1.ShipmentStatus {
	switch s {
	case sqlcgen.ShipmentStatusPicking:
		return stillhousev1.ShipmentStatus_SHIPMENT_STATUS_PICKING
	case sqlcgen.ShipmentStatusShipped:
		return stillhousev1.ShipmentStatus_SHIPMENT_STATUS_SHIPPED
	case sqlcgen.ShipmentStatusCancelled:
		return stillhousev1.ShipmentStatus_SHIPMENT_STATUS_CANCELLED
	default:
		return stillhousev1.ShipmentStatus_SHIPMENT_STATUS_UNSPECIFIED
	}
}

// parseOptionalUUID accepts an empty string as "not given" and anything
// else as a UUID that has to parse.
func parseOptionalUUID(v, field string) (uuid.NullUUID, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return uuid.NullUUID{}, nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return uuid.NullUUID{}, fmt.Errorf("invalid %s", field)
	}
	return uuid.NullUUID{UUID: id, Valid: true}, nil
}
