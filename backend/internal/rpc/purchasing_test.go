package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The point of the purchasing track, end to end: what it cost to get the
// grain to the door ends up in the cost of the grain, not in an expense
// account. Without that, inventory value and cost of sales are
// understated by exactly that amount.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestPurchaseOrderReceivingAndLandedCost(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewPurchasingService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tag := uuid.NewString()[:8]

	material, err := f.q.CreateMaterial(f.ctx, sqlcgen.CreateMaterialParams{
		TenantID: f.tenant.ID, Name: "PO Rye " + tag,
		Kind: sqlcgen.MaterialKindGrain, Uom: "kg",
	})
	if err != nil {
		t.Fatalf("create material: %v", err)
	}
	supplier, err := svc.SaveSupplier(f.ctx, connect.NewRequest(&stillhousev1.SaveSupplierRequest{
		Name: "Prairie Grain " + tag, Country: "CA", PaymentTermsDays: 30,
	}))
	if err != nil {
		t.Fatalf("SaveSupplier: %v", err)
	}

	po, err := svc.CreatePurchaseOrder(f.ctx, connect.NewRequest(
		&stillhousev1.CreatePurchaseOrderRequest{
			SupplierId: supplier.Msg.GetSupplier().GetId(),
			ExpectedOn: "2026-09-15", Reference: "PO ref " + tag,
		}))
	if err != nil {
		t.Fatalf("CreatePurchaseOrder: %v", err)
	}
	poID := po.Msg.GetPurchaseOrder().GetId()

	withLine, err := svc.AddPurchaseOrderLine(f.ctx, connect.NewRequest(
		&stillhousev1.AddPurchaseOrderLineRequest{
			PurchaseOrderId: poID, MaterialId: material.ID.String(),
			QuantityOrdered: 1000, UnitPrice: "0.55", Uom: "kg",
		}))
	if err != nil {
		t.Fatalf("AddPurchaseOrderLine: %v", err)
	}
	lineID := withLine.Msg.GetPurchaseOrder().GetLines()[0].GetId()

	t.Run("receiving against a draft is refused", func(t *testing.T) {
		if _, err := svc.ReceiveAgainstPO(f.ctx, connect.NewRequest(
			&stillhousev1.ReceiveAgainstPORequest{
				PurchaseOrderLineId: lineID, QuantityReceived: 100,
			})); err == nil {
			t.Error("goods were received against an order that was never placed")
		}
	})

	if _, err := svc.SetPurchaseOrderStatus(f.ctx, connect.NewRequest(
		&stillhousev1.SetPurchaseOrderStatusRequest{
			Id: poID, Status: stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_PLACED,
		})); err != nil {
		t.Fatalf("place order: %v", err)
	}

	t.Run("a partial receipt leaves the order partially received", func(t *testing.T) {
		got, err := svc.ReceiveAgainstPO(f.ctx, connect.NewRequest(
			&stillhousev1.ReceiveAgainstPORequest{
				PurchaseOrderLineId: lineID, QuantityReceived: 600,
				SupplierLot: "SL-" + tag,
				// 1,000 kg of freight over 600 kg — deliberately lumpy,
				// because that is what a part-load costs.
				FreightCad: 120, ImportDutyCad: 0, HandlingCad: 30,
			}))
		if err != nil {
			t.Fatalf("ReceiveAgainstPO: %v", err)
		}
		if got.Msg.GetQuantityOutstanding() != 400 {
			t.Errorf("outstanding %v, want 400", got.Msg.GetQuantityOutstanding())
		}
		if s := got.Msg.GetPurchaseOrder().GetStatus(); s != stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_PARTIALLY_RECEIVED {
			t.Errorf("status %v, want partially received", s)
		}
		// 0.55 + (120 + 30) / 600 = 0.55 + 0.25 = 0.80
		if !got.Msg.GetLandedUnitCostKnown() {
			t.Fatal("the landed cost is not known despite a priced line")
		}
		if v := got.Msg.GetLandedUnitCostCad(); v < 0.7999 || v > 0.8001 {
			t.Errorf("landed unit cost %v, want 0.80 — the supplier's 0.55 plus 150 CAD "+
				"of freight and handling over 600 kg", v)
		}
	})

	t.Run("the balance completes it", func(t *testing.T) {
		got, err := svc.ReceiveAgainstPO(f.ctx, connect.NewRequest(
			&stillhousev1.ReceiveAgainstPORequest{
				PurchaseOrderLineId: lineID, QuantityReceived: 400,
			}))
		if err != nil {
			t.Fatalf("ReceiveAgainstPO: %v", err)
		}
		if got.Msg.GetQuantityOutstanding() != 0 {
			t.Errorf("outstanding %v, want 0", got.Msg.GetQuantityOutstanding())
		}
		if s := got.Msg.GetPurchaseOrder().GetStatus(); s != stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_RECEIVED {
			t.Errorf("status %v, want received — it follows the lines, not a person", s)
		}
		// No charges on this one, so the landed cost is the line price.
		if v := got.Msg.GetLandedUnitCostCad(); v < 0.5499 || v > 0.5501 {
			t.Errorf("landed unit cost %v, want 0.55", v)
		}
	})

	t.Run("charges arriving later update the landed cost", func(t *testing.T) {
		// A freight invoice a week after the goods is the ordinary case.
		// What it cost to get here did not change; only when we learned
		// it.
		lots, err := f.q.ListMaterialLots(f.ctx, sqlcgen.ListMaterialLotsParams{
			MaterialID: uuid.NullUUID{UUID: material.ID, Valid: true},
		})
		if err != nil {
			t.Fatalf("list lots: %v", err)
		}
		var target uuid.UUID
		for _, l := range lots {
			if l.QuantityReceived == 400 {
				target = l.ID
			}
		}
		if target == uuid.Nil {
			t.Fatal("could not find the second lot")
		}
		got, err := svc.SetLandedCharges(f.ctx, connect.NewRequest(
			&stillhousev1.SetLandedChargesRequest{
				MaterialLotId: target.String(), FreightCad: 80,
			}))
		if err != nil {
			t.Fatalf("SetLandedCharges: %v", err)
		}
		// 0.55 + 80/400 = 0.75
		if v := got.Msg.GetLandedUnitCostCad(); v < 0.7499 || v > 0.7501 {
			t.Errorf("landed unit cost %v after a late freight bill, want 0.75", v)
		}
	})

	t.Run("goods received not yet invoiced is answerable", func(t *testing.T) {
		grni, err := svc.ListGRNI(f.ctx, connect.NewRequest(&stillhousev1.ListGRNIRequest{}))
		if err != nil {
			t.Fatalf("ListGRNI: %v", err)
		}
		var found int
		for _, l := range grni.Msg.GetLines() {
			if l.GetMaterialName() == material.Name {
				found++
			}
		}
		if found != 2 {
			t.Fatalf("GRNI shows %d lots for this material, want 2", found)
		}
		// Marking one invoiced takes it off the list.
		lotID := ""
		for _, l := range grni.Msg.GetLines() {
			if l.GetMaterialName() == material.Name {
				lotID = l.GetMaterialLotId()
				break
			}
		}
		if _, err := svc.MarkLotInvoiced(f.ctx, connect.NewRequest(
			&stillhousev1.MarkLotInvoicedRequest{
				MaterialLotId: lotID, InvoiceReference: "INV-" + tag,
			})); err != nil {
			t.Fatalf("MarkLotInvoiced: %v", err)
		}
		after, err := svc.ListGRNI(f.ctx, connect.NewRequest(&stillhousev1.ListGRNIRequest{}))
		if err != nil {
			t.Fatalf("ListGRNI: %v", err)
		}
		var still int
		for _, l := range after.Msg.GetLines() {
			if l.GetMaterialName() == material.Name {
				still++
			}
		}
		if still != 1 {
			t.Errorf("after invoicing one lot, %d remain in GRNI, want 1", still)
		}
	})

	t.Run("an invoice reference is required to clear GRNI", func(t *testing.T) {
		if _, err := svc.MarkLotInvoiced(f.ctx, connect.NewRequest(
			&stillhousev1.MarkLotInvoicedRequest{MaterialLotId: uuid.NewString()},
		)); err == nil {
			t.Error("a lot was marked invoiced with no reference — nothing links it to the bill")
		}
	})

	t.Run("a placed order's lines cannot be edited", func(t *testing.T) {
		if _, err := svc.AddPurchaseOrderLine(f.ctx, connect.NewRequest(
			&stillhousev1.AddPurchaseOrderLineRequest{
				PurchaseOrderId: poID, MaterialId: material.ID.String(),
				QuantityOrdered: 10, UnitPrice: "0.55",
			})); err == nil {
			t.Error("a line was added to an order the supplier already has")
		}
	})
}

// Landed cost is only worth collecting if the figures built on it use
// it. This is the E3/K4 test: freight absorbed into a lot has to reach
// the accounting journal and the bottling-run cost, or it is another
// hand-maintained number that nothing reads.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestLandedCostReachesTheFiguresBuiltOnIt(t *testing.T) {
	f := newLedgerFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	purchasing := NewPurchasingService(f.db, log)
	journal := NewJournalService(f.db, log)
	tag := uuid.NewString()[:8]
	day := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")

	material, err := f.q.CreateMaterial(f.ctx, sqlcgen.CreateMaterialParams{
		TenantID: f.tenant.ID, Name: "Landed Barley " + tag,
		Kind: sqlcgen.MaterialKindMalt, Uom: "kg",
	})
	if err != nil {
		t.Fatalf("create material: %v", err)
	}
	supplier, err := purchasing.SaveSupplier(f.ctx, connect.NewRequest(
		&stillhousev1.SaveSupplierRequest{Name: "Maltster " + tag, PaymentTermsDays: -1}))
	if err != nil {
		t.Fatalf("SaveSupplier: %v", err)
	}
	po, err := purchasing.CreatePurchaseOrder(f.ctx, connect.NewRequest(
		&stillhousev1.CreatePurchaseOrderRequest{SupplierId: supplier.Msg.GetSupplier().GetId()}))
	if err != nil {
		t.Fatalf("CreatePurchaseOrder: %v", err)
	}
	poID := po.Msg.GetPurchaseOrder().GetId()
	withLine, err := purchasing.AddPurchaseOrderLine(f.ctx, connect.NewRequest(
		&stillhousev1.AddPurchaseOrderLineRequest{
			PurchaseOrderId: poID, MaterialId: material.ID.String(),
			QuantityOrdered: 500, UnitPrice: "1.00",
		}))
	if err != nil {
		t.Fatalf("AddPurchaseOrderLine: %v", err)
	}
	if _, err := purchasing.SetPurchaseOrderStatus(f.ctx, connect.NewRequest(
		&stillhousev1.SetPurchaseOrderStatusRequest{
			Id: poID, Status: stillhousev1.PurchaseOrderStatus_PURCHASE_ORDER_STATUS_PLACED,
		})); err != nil {
		t.Fatalf("place: %v", err)
	}
	// 500 kg at 1.00, plus 250 of freight → landed 1.50/kg, 750 total.
	if _, err := purchasing.ReceiveAgainstPO(f.ctx, connect.NewRequest(
		&stillhousev1.ReceiveAgainstPORequest{
			PurchaseOrderLineId: withLine.Msg.GetPurchaseOrder().GetLines()[0].GetId(),
			QuantityReceived:    500, FreightCad: 250, ReceivedOn: day,
		})); err != nil {
		t.Fatalf("ReceiveAgainstPO: %v", err)
	}

	got, err := journal.PreviewJournal(f.ctx, connect.NewRequest(
		&stillhousev1.PreviewJournalRequest{PeriodStart: day, PeriodEnd: day}))
	if err != nil {
		t.Fatalf("PreviewJournal: %v", err)
	}
	var receipt *stillhousev1.JournalLine
	for _, l := range got.Msg.GetLines() {
		if l.GetKind() == stillhousev1.JournalEventKind_JOURNAL_EVENT_KIND_MATERIAL_RECEIPT &&
			l.GetDescription() == material.Name {
			receipt = l
		}
	}
	if receipt == nil {
		t.Fatal("the receipt produced no journal line")
	}
	// 750, not 500. The 250 of freight is part of what the malt cost.
	if v := receipt.GetAmountCad(); v < 749.99 || v > 750.01 {
		t.Errorf("journal posted %v for the receipt, want 750 — the supplier's 500 plus "+
			"250 of freight, which belongs in inventory rather than an expense account", v)
	}
	if !strings.Contains(receipt.GetBasis(), "landed") {
		t.Errorf("basis %q does not say the figure is a landed cost — somebody reconciling "+
			"against the supplier invoice will find 500 and no explanation", receipt.GetBasis())
	}
	var noted bool
	for _, w := range got.Msg.GetWarnings() {
		if strings.Contains(w.GetDetail(), "landed cost") {
			noted = true
		}
	}
	if !noted {
		t.Error("nothing tells the reader some lines will not tie to supplier invoices")
	}
}
