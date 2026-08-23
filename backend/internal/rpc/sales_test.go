package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// salesStock creates a product and a packaged lot to pick from.
func (f *ledgerFixture) salesStock(
	t *testing.T, sizeML int32, abv float64, bottles int32,
) (product sqlcgen.Product, lot sqlcgen.PackagedInventory) {
	t.Helper()
	product, err := f.q.CreateProduct(f.ctx, sqlcgen.CreateProductParams{
		TenantID: f.tenant.ID, Name: "Ship " + uuid.NewString()[:8],
		SpiritKind: sqlcgen.SpiritKindVodka, BottleSizeMl: sizeML, TargetAbvPct: abv,
	})
	if err != nil {
		t.Fatalf("product: %v", err)
	}
	lot, err = f.q.UpsertPackagedInventory(f.ctx, sqlcgen.UpsertPackagedInventoryParams{
		TenantID: f.tenant.ID, ProductID: product.ID,
		LotCode: "SHIP-" + uuid.NewString()[:8], Jurisdiction: "CA-ON",
		BottlesOnHand: bottles,
	})
	if err != nil {
		t.Fatalf("packaged inventory: %v", err)
	}
	return product, lot
}

func (f *ledgerFixture) salesCustomer(t *testing.T, kind sqlcgen.RemovalDestinationKind) sqlcgen.Customer {
	t.Helper()
	c, err := f.q.CreateCustomer(f.ctx, sqlcgen.CreateCustomerParams{
		TenantID: f.tenant.ID, Name: "Buyer " + uuid.NewString()[:8],
		Kind: sqlcgen.CustomerKindProvincialBoard, Jurisdiction: "CA-ON",
		DefaultDestinationKind: string(kind),
	})
	if err != nil {
		t.Fatalf("customer: %v", err)
	}
	return c
}

// The arrow the whole track exists for: shipping writes the removals.
//
// Before this, a shipment and a removal were two unrelated acts, so a
// pallet that went out without somebody remembering to type a removal was
// a silent under-report on the return. The assertion that matters is not
// that a removal exists — it is that the LAA and the duty on it are what
// actually left the building, and that the two rows point at each other
// afterwards.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestShipmentProducesRemovals(t *testing.T) {
	f := newLedgerFixture(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	svc := NewSalesService(f.db, logger)

	cust := f.salesCustomer(t, sqlcgen.RemovalDestinationKindDutyPaidCustomer)
	product, lot := f.salesStock(t, 750, 40, 100)

	order, err := svc.CreateSalesOrder(f.ctx, connect.NewRequest(&stillhousev1.CreateSalesOrderRequest{
		CustomerId: cust.ID.String(), RequiredBy: "2026-09-30",
		CustomerReference: "PO-1234",
	}))
	if err != nil {
		t.Fatalf("CreateSalesOrder: %v", err)
	}
	orderID := order.Msg.GetSalesOrder().GetId()

	withLine, err := svc.AddSalesOrderLine(f.ctx, connect.NewRequest(&stillhousev1.AddSalesOrderLineRequest{
		SalesOrderId: orderID, ProductId: product.ID.String(),
		BottlesOrdered: 60, UnitPrice: "34.95",
	}))
	if err != nil {
		t.Fatalf("AddSalesOrderLine: %v", err)
	}
	orderLineID := withLine.Msg.GetSalesOrder().GetLines()[0].GetId()
	if got, want := withLine.Msg.GetSalesOrder().GetTotalValue(), "2097.00"; got != want {
		t.Errorf("order value = %s, want %s", got, want)
	}

	if _, err := svc.SetSalesOrderStatus(f.ctx, connect.NewRequest(&stillhousev1.SetSalesOrderStatusRequest{
		Id: orderID, Status: stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_CONFIRMED,
	})); err != nil {
		t.Fatalf("confirm order: %v", err)
	}

	t.Run("a confirmed order shows the stock as spoken for", func(t *testing.T) {
		res, err := svc.ListStockCommitments(f.ctx, connect.NewRequest(&stillhousev1.ListStockCommitmentsRequest{}))
		if err != nil {
			t.Fatalf("ListStockCommitments: %v", err)
		}
		var found *stillhousev1.StockCommitment
		for _, c := range res.Msg.GetCommitments() {
			if c.GetProductId() == product.ID.String() {
				found = c
			}
		}
		if found == nil {
			t.Fatal("the product an order was just taken for is not on the stock screen")
		}
		if got, want := found.GetBottlesReserved(), int32(60); got != want {
			t.Errorf("reserved = %d, want %d", got, want)
		}
		// Reservation is soft on purpose: the alcohol has not moved, so
		// nothing was decremented and availability is untouched.
		if got, want := found.GetBottlesOnHand(), int32(100); got != want {
			t.Errorf("on hand = %d, want %d — a promise moved alcohol", got, want)
		}
		if got, want := found.GetBottlesAvailable(), int32(100); got != want {
			t.Errorf("available = %d, want %d", got, want)
		}
	})

	shipment, err := svc.CreateShipment(f.ctx, connect.NewRequest(&stillhousev1.CreateShipmentRequest{
		SalesOrderId: orderID, Carrier: "Loomis", TrackingRef: "TRK-9",
	}))
	if err != nil {
		t.Fatalf("CreateShipment: %v", err)
	}
	shipmentID := shipment.Msg.GetShipment().GetId()
	if got, want := shipment.Msg.GetShipment().GetCustomerId(), cust.ID.String(); got != want {
		t.Errorf("shipment customer = %s, want the order's %s", got, want)
	}

	picked, err := svc.AddShipmentLine(f.ctx, connect.NewRequest(&stillhousev1.AddShipmentLineRequest{
		ShipmentId: shipmentID, PackagedInventoryId: lot.ID.String(),
		Bottles: 60, SalesOrderLineId: orderLineID,
	}))
	if err != nil {
		t.Fatalf("AddShipmentLine: %v", err)
	}
	// 60 × 0.75 L × 40 % = 18 LAA. Shown before the operator commits, so
	// they see what they are about to put on the return.
	if got, want := picked.Msg.GetShipment().GetTotalLaa(), 18.0; !near(got, want, 1e-9) {
		t.Errorf("picked LAA = %v, want %v", got, want)
	}

	t.Run("picking twice from the same lot cannot oversell it", func(t *testing.T) {
		// 100 on hand, 60 already picked onto this shipment and not yet
		// decremented. A second pick of 60 would promise 120.
		_, err := svc.AddShipmentLine(f.ctx, connect.NewRequest(&stillhousev1.AddShipmentLineRequest{
			ShipmentId: shipmentID, PackagedInventoryId: lot.ID.String(), Bottles: 60,
		}))
		if err == nil {
			t.Fatal("120 bottles were picked from a lot holding 100")
		}
		if !strings.Contains(err.Error(), "unpicked") {
			t.Errorf("error should say what is left to pick, got: %v", err)
		}
	})

	shipped, err := svc.ShipShipment(f.ctx, connect.NewRequest(&stillhousev1.ShipShipmentRequest{
		Id: shipmentID, ShipDate: "2026-08-20",
	}))
	if err != nil {
		t.Fatalf("ShipShipment: %v", err)
	}

	removals := shipped.Msg.GetRemovals()
	if len(removals) != 1 {
		t.Fatalf("shipping one picked line wrote %d removals, want 1", len(removals))
	}
	r := removals[0]

	t.Run("the removal carries what actually left", func(t *testing.T) {
		if got, want := r.GetBottlesRemoved(), int32(60); got != want {
			t.Errorf("bottles = %d, want %d", got, want)
		}
		if got, want := r.GetTotalLaa(), 18.0; !near(got, want, 1e-9) {
			t.Errorf("removal LAA = %v, want %v — the return and the truck disagree", got, want)
		}
		if got, want := r.GetTotalLaa(), shipped.Msg.GetShipment().GetTotalLaa(); !near(got, want, 1e-9) {
			t.Errorf("removal LAA %v != shipment LAA %v", got, want)
		}
		if got, want := r.GetRemovalDate(), "2026-08-20"; got != want {
			t.Errorf("removal date = %s, want the ship date %s", got, want)
		}
	})

	t.Run("the destination comes from the customer, not the request", func(t *testing.T) {
		if got, want := r.GetDestinationKind(),
			stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER; got != want {
			t.Errorf("destination kind = %v, want %v", got, want)
		}
		if got, want := r.GetDestinationName(), cust.Name; got != want {
			t.Errorf("destination name = %q, want %q", got, want)
		}
	})

	t.Run("duty is charged, and is what a hand-recorded removal would charge", func(t *testing.T) {
		if shipped.Msg.GetDutyCad() <= 0 {
			t.Fatal("stock that was never dutied at packaging left with no duty on it")
		}
		if !near(shipped.Msg.GetDutyCad(), r.GetDutyAmountCad(), 1e-9) {
			t.Errorf("shipment duty %v != removal duty %v",
				shipped.Msg.GetDutyCad(), r.GetDutyAmountCad())
		}
		// The same 60 bottles recorded by hand against an identical lot.
		// Both paths run recordRemoval, and this is what says so.
		_, byHand := f.salesStock(t, 750, 40, 60)
		rsvc := NewRemovalService(f.db, logger)
		manual, err := rsvc.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
			PackagedInventoryId: byHand.ID.String(), BottlesRemoved: 60,
			RemovalDate: "2026-08-20", CustomerId: cust.ID.String(),
		}))
		if err != nil {
			t.Fatalf("hand-recorded removal: %v", err)
		}
		if got, want := r.GetDutyAmountCad(), manual.Msg.GetRemoval().GetDutyAmountCad(); !near(got, want, 1e-9) {
			t.Errorf("shipped removal duty %v != hand-recorded duty %v — the two paths have drifted",
				got, want)
		}
		if got, want := r.GetDutyRatePerLaa(), manual.Msg.GetRemoval().GetDutyRatePerLaa(); !near(got, want, 1e-9) {
			t.Errorf("shipped rate %v != hand-recorded rate %v", got, want)
		}
	})

	t.Run("the stock is gone", func(t *testing.T) {
		after, err := f.q.GetPackagedInventoryForUpdate(f.ctx, lot.ID)
		if err != nil {
			t.Fatalf("re-read lot: %v", err)
		}
		if got, want := after.BottlesOnHand, int32(40); got != want {
			t.Errorf("on hand = %d, want %d", got, want)
		}
	})

	t.Run("the shipment and the removal point at each other", func(t *testing.T) {
		got, err := svc.GetShipment(f.ctx, connect.NewRequest(&stillhousev1.GetShipmentRequest{Id: shipmentID}))
		if err != nil {
			t.Fatalf("GetShipment: %v", err)
		}
		line := got.Msg.GetShipment().GetLines()[0]
		if line.GetPackagingRemovalId() != r.GetId() {
			t.Errorf("shipment line points at removal %q, want %q",
				line.GetPackagingRemovalId(), r.GetId())
		}
		if line.GetRemovalNo() != r.GetRemovalNo() {
			t.Errorf("line removal no = %d, want %d", line.GetRemovalNo(), r.GetRemovalNo())
		}
		rowID, err := uuid.Parse(r.GetId())
		if err != nil {
			t.Fatalf("removal id: %v", err)
		}
		row, err := f.q.GetRemoval(f.ctx, rowID)
		if err != nil {
			t.Fatalf("re-read removal: %v", err)
		}
		if !row.ShipmentID.Valid || row.ShipmentID.UUID.String() != shipmentID {
			t.Errorf("removal shipment_id = %v, want %s", row.ShipmentID, shipmentID)
		}
	})

	t.Run("the order closed itself out", func(t *testing.T) {
		got, err := svc.GetSalesOrder(f.ctx, connect.NewRequest(&stillhousev1.GetSalesOrderRequest{Id: orderID}))
		if err != nil {
			t.Fatalf("GetSalesOrder: %v", err)
		}
		so := got.Msg.GetSalesOrder()
		if got, want := so.GetStatus(),
			stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_SHIPPED; got != want {
			t.Errorf("order status = %v, want %v", got, want)
		}
		if got, want := so.GetLines()[0].GetBottlesShipped(), int32(60); got != want {
			t.Errorf("bottles shipped on the line = %d, want %d", got, want)
		}
		if len(got.Msg.GetShipments()) != 1 {
			t.Errorf("order shows %d shipments, want 1", len(got.Msg.GetShipments()))
		}
	})

	t.Run("shipping twice is refused", func(t *testing.T) {
		// The failure this guards against writes a second set of removals
		// against one pallet and doubles the duty on the return.
		if _, err := svc.ShipShipment(f.ctx, connect.NewRequest(&stillhousev1.ShipShipmentRequest{
			Id: shipmentID,
		})); err == nil {
			t.Fatal("a shipment that had already gone shipped again")
		}
		var n int
		if err := f.pool.QueryRow(f.ctx,
			"SELECT COUNT(*) FROM packaging_removals WHERE shipment_id = $1",
			shipmentID).Scan(&n); err != nil {
			t.Fatalf("count removals: %v", err)
		}
		if n != 1 {
			t.Errorf("%d removals attached to one shipment, want 1", n)
		}
	})

	t.Run("a shipment that has gone cannot be cancelled", func(t *testing.T) {
		if _, err := svc.CancelShipment(f.ctx, connect.NewRequest(&stillhousev1.CancelShipmentRequest{
			Id: shipmentID, Reason: "changed my mind",
		})); err == nil {
			t.Error("a shipment whose removals are on the return was cancelled")
		}
	})
}

// A pick that only partly satisfies an order must leave the order open,
// or the rest of it is quietly forgotten.
func TestPartialShipmentLeavesTheOrderOpen(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewSalesService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	cust := f.salesCustomer(t, sqlcgen.RemovalDestinationKindDutyPaidCustomer)
	product, lot := f.salesStock(t, 700, 43, 200)

	order, err := svc.CreateSalesOrder(f.ctx, connect.NewRequest(&stillhousev1.CreateSalesOrderRequest{
		CustomerId: cust.ID.String(),
	}))
	if err != nil {
		t.Fatalf("CreateSalesOrder: %v", err)
	}
	orderID := order.Msg.GetSalesOrder().GetId()
	withLine, err := svc.AddSalesOrderLine(f.ctx, connect.NewRequest(&stillhousev1.AddSalesOrderLineRequest{
		SalesOrderId: orderID, ProductId: product.ID.String(), BottlesOrdered: 120,
	}))
	if err != nil {
		t.Fatalf("AddSalesOrderLine: %v", err)
	}
	lineID := withLine.Msg.GetSalesOrder().GetLines()[0].GetId()
	if _, err := svc.SetSalesOrderStatus(f.ctx, connect.NewRequest(&stillhousev1.SetSalesOrderStatusRequest{
		Id: orderID, Status: stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_CONFIRMED,
	})); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	ship := func(bottles int32) {
		t.Helper()
		sh, err := svc.CreateShipment(f.ctx, connect.NewRequest(&stillhousev1.CreateShipmentRequest{
			SalesOrderId: orderID,
		}))
		if err != nil {
			t.Fatalf("CreateShipment: %v", err)
		}
		if _, err := svc.AddShipmentLine(f.ctx, connect.NewRequest(&stillhousev1.AddShipmentLineRequest{
			ShipmentId: sh.Msg.GetShipment().GetId(), PackagedInventoryId: lot.ID.String(),
			Bottles: bottles, SalesOrderLineId: lineID,
		})); err != nil {
			t.Fatalf("AddShipmentLine: %v", err)
		}
		if _, err := svc.ShipShipment(f.ctx, connect.NewRequest(&stillhousev1.ShipShipmentRequest{
			Id: sh.Msg.GetShipment().GetId(), ShipDate: "2026-08-21",
		})); err != nil {
			t.Fatalf("ShipShipment: %v", err)
		}
	}

	ship(50)
	got, err := svc.GetSalesOrder(f.ctx, connect.NewRequest(&stillhousev1.GetSalesOrderRequest{Id: orderID}))
	if err != nil {
		t.Fatalf("GetSalesOrder: %v", err)
	}
	if want := stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_PARTIALLY_SHIPPED; got.Msg.GetSalesOrder().GetStatus() != want {
		t.Errorf("after 50 of 120: status = %v, want %v", got.Msg.GetSalesOrder().GetStatus(), want)
	}

	ship(70)
	got, err = svc.GetSalesOrder(f.ctx, connect.NewRequest(&stillhousev1.GetSalesOrderRequest{Id: orderID}))
	if err != nil {
		t.Fatalf("GetSalesOrder: %v", err)
	}
	so := got.Msg.GetSalesOrder()
	if want := stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_SHIPPED; so.GetStatus() != want {
		t.Errorf("after 120 of 120: status = %v, want %v", so.GetStatus(), want)
	}
	if got, want := so.GetBottlesShipped(), int32(120); got != want {
		t.Errorf("bottles shipped = %d, want %d", got, want)
	}
	after, err := f.q.GetPackagedInventoryForUpdate(f.ctx, lot.ID)
	if err != nil {
		t.Fatalf("re-read lot: %v", err)
	}
	if got, want := after.BottlesOnHand, int32(80); got != want {
		t.Errorf("on hand = %d, want %d", got, want)
	}
}

// A hold says this stock must not leave. It has to bite at the pick, so
// the picker finds out in the warehouse — and again at the ship, so a lot
// held after it was picked still cannot go.
func TestHeldLotCannotBePickedOrShipped(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewSalesService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	cust := f.salesCustomer(t, sqlcgen.RemovalDestinationKindDutyPaidCustomer)
	_, held := f.salesStock(t, 750, 40, 50)
	_, clean := f.salesStock(t, 750, 40, 50)

	if _, err := f.q.HoldPackagedLot(f.ctx, sqlcgen.HoldPackagedLotParams{
		ID: held.ID, HeldBy: uuid.NullUUID{UUID: f.user.ID, Valid: true},
		HoldReason: "off-flavour under investigation",
	}); err != nil {
		t.Fatalf("hold lot: %v", err)
	}

	sh, err := svc.CreateShipment(f.ctx, connect.NewRequest(&stillhousev1.CreateShipmentRequest{
		CustomerId: cust.ID.String(),
	}))
	if err != nil {
		t.Fatalf("CreateShipment: %v", err)
	}
	shipmentID := sh.Msg.GetShipment().GetId()

	_, err = svc.AddShipmentLine(f.ctx, connect.NewRequest(&stillhousev1.AddShipmentLineRequest{
		ShipmentId: shipmentID, PackagedInventoryId: held.ID.String(), Bottles: 10,
	}))
	if err == nil {
		t.Fatal("a held lot was picked")
	}
	if !strings.Contains(err.Error(), "off-flavour") {
		t.Errorf("the refusal should quote the hold reason, got: %v", err)
	}

	// Picked while clean, held afterwards: the ship must still refuse,
	// because the removal is the thing that would put it on the return.
	if _, err := svc.AddShipmentLine(f.ctx, connect.NewRequest(&stillhousev1.AddShipmentLineRequest{
		ShipmentId: shipmentID, PackagedInventoryId: clean.ID.String(), Bottles: 10,
	})); err != nil {
		t.Fatalf("AddShipmentLine: %v", err)
	}
	if _, err := f.q.HoldPackagedLot(f.ctx, sqlcgen.HoldPackagedLotParams{
		ID: clean.ID, HeldBy: uuid.NullUUID{UUID: f.user.ID, Valid: true},
		HoldReason: "recall",
	}); err != nil {
		t.Fatalf("hold lot: %v", err)
	}
	if _, err := svc.ShipShipment(f.ctx, connect.NewRequest(&stillhousev1.ShipShipmentRequest{
		Id: shipmentID,
	})); err == nil {
		t.Fatal("a lot held between picking and shipping still left the building")
	}

	// And nothing partial was left behind: the transaction rolls back, so
	// the lot is untouched and the shipment is still picking.
	after, err := f.q.GetPackagedInventoryForUpdate(f.ctx, clean.ID)
	if err != nil {
		t.Fatalf("re-read lot: %v", err)
	}
	if got, want := after.BottlesOnHand, int32(50); got != want {
		t.Errorf("on hand = %d, want %d — a refused shipment still moved stock", got, want)
	}
	got, err := svc.GetShipment(f.ctx, connect.NewRequest(&stillhousev1.GetShipmentRequest{Id: shipmentID}))
	if err != nil {
		t.Fatalf("GetShipment: %v", err)
	}
	if want := stillhousev1.ShipmentStatus_SHIPMENT_STATUS_PICKING; got.Msg.GetShipment().GetStatus() != want {
		t.Errorf("status = %v, want %v", got.Msg.GetShipment().GetStatus(), want)
	}
}

// A shipment with nothing on it writes no removals; an order cannot be
// cancelled once stock has gone; shipping states cannot be set by hand.
func TestSalesRefusals(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewSalesService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	cust := f.salesCustomer(t, sqlcgen.RemovalDestinationKindExport)
	product, lot := f.salesStock(t, 750, 40, 30)

	t.Run("an empty shipment cannot ship", func(t *testing.T) {
		sh, err := svc.CreateShipment(f.ctx, connect.NewRequest(&stillhousev1.CreateShipmentRequest{
			CustomerId: cust.ID.String(),
		}))
		if err != nil {
			t.Fatalf("CreateShipment: %v", err)
		}
		if _, err := svc.ShipShipment(f.ctx, connect.NewRequest(&stillhousev1.ShipShipmentRequest{
			Id: sh.Msg.GetShipment().GetId(),
		})); err == nil {
			t.Error("an empty shipment shipped")
		}
	})

	t.Run("shipping status cannot be claimed by hand", func(t *testing.T) {
		order, err := svc.CreateSalesOrder(f.ctx, connect.NewRequest(&stillhousev1.CreateSalesOrderRequest{
			CustomerId: cust.ID.String(),
		}))
		if err != nil {
			t.Fatalf("CreateSalesOrder: %v", err)
		}
		if _, err := svc.SetSalesOrderStatus(f.ctx, connect.NewRequest(&stillhousev1.SetSalesOrderStatusRequest{
			Id:     order.Msg.GetSalesOrder().GetId(),
			Status: stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_SHIPPED,
		})); err == nil {
			t.Error("an order claimed stock had left with no removal to say so")
		}
	})

	t.Run("an empty order cannot be confirmed", func(t *testing.T) {
		order, err := svc.CreateSalesOrder(f.ctx, connect.NewRequest(&stillhousev1.CreateSalesOrderRequest{
			CustomerId: cust.ID.String(),
		}))
		if err != nil {
			t.Fatalf("CreateSalesOrder: %v", err)
		}
		if _, err := svc.SetSalesOrderStatus(f.ctx, connect.NewRequest(&stillhousev1.SetSalesOrderStatusRequest{
			Id:     order.Msg.GetSalesOrder().GetId(),
			Status: stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_CONFIRMED,
		})); err == nil {
			t.Error("an order with no lines was confirmed")
		}
	})

	t.Run("an order that has shipped cannot be cancelled", func(t *testing.T) {
		order, err := svc.CreateSalesOrder(f.ctx, connect.NewRequest(&stillhousev1.CreateSalesOrderRequest{
			CustomerId: cust.ID.String(),
		}))
		if err != nil {
			t.Fatalf("CreateSalesOrder: %v", err)
		}
		orderID := order.Msg.GetSalesOrder().GetId()
		withLine, err := svc.AddSalesOrderLine(f.ctx, connect.NewRequest(&stillhousev1.AddSalesOrderLineRequest{
			SalesOrderId: orderID, ProductId: product.ID.String(), BottlesOrdered: 20,
		}))
		if err != nil {
			t.Fatalf("AddSalesOrderLine: %v", err)
		}
		if _, err := svc.SetSalesOrderStatus(f.ctx, connect.NewRequest(&stillhousev1.SetSalesOrderStatusRequest{
			Id: orderID, Status: stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_CONFIRMED,
		})); err != nil {
			t.Fatalf("confirm: %v", err)
		}
		sh, err := svc.CreateShipment(f.ctx, connect.NewRequest(&stillhousev1.CreateShipmentRequest{
			SalesOrderId: orderID,
		}))
		if err != nil {
			t.Fatalf("CreateShipment: %v", err)
		}
		if _, err := svc.AddShipmentLine(f.ctx, connect.NewRequest(&stillhousev1.AddShipmentLineRequest{
			ShipmentId: sh.Msg.GetShipment().GetId(), PackagedInventoryId: lot.ID.String(),
			Bottles: 10, SalesOrderLineId: withLine.Msg.GetSalesOrder().GetLines()[0].GetId(),
		})); err != nil {
			t.Fatalf("AddShipmentLine: %v", err)
		}
		if _, err := svc.ShipShipment(f.ctx, connect.NewRequest(&stillhousev1.ShipShipmentRequest{
			Id: sh.Msg.GetShipment().GetId(),
		})); err != nil {
			t.Fatalf("ShipShipment: %v", err)
		}
		if _, err := svc.SetSalesOrderStatus(f.ctx, connect.NewRequest(&stillhousev1.SetSalesOrderStatusRequest{
			Id:           orderID,
			Status:       stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_CANCELLED,
			CancelReason: "customer changed their mind",
		})); err == nil {
			t.Error("an order whose stock is on the return was cancelled")
		}
	})
}
