package rpc

import (
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// Order → shipment → invoice → payment, with the arithmetic checked
// exactly. Sixty bottles at $34.95 is $2,097.00 and thirteen percent of
// that is $272.61; through a float64 the first is 2096.9999999999995.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestInvoiceFromShipment(t *testing.T) {
	f := newLedgerFixture(t)
	sales := NewSalesService(f.db, testLogger())
	inv := NewInvoicingService(f.db, testLogger())

	cust := f.salesCustomer(t, sqlcgen.RemovalDestinationKindDutyPaidCustomer)
	if _, err := f.pool.Exec(f.ctx,
		"UPDATE customers SET payment_terms_days = 30, address = $2 WHERE id = $1",
		cust.ID, "1 King St W, Toronto ON"); err != nil {
		t.Fatalf("set terms: %v", err)
	}
	product, lot := f.salesStock(t, 750, 40, 200)

	order, err := sales.CreateSalesOrder(f.ctx, connect.NewRequest(
		&stillhousev1.CreateSalesOrderRequest{CustomerId: cust.ID.String()}))
	if err != nil {
		t.Fatalf("CreateSalesOrder: %v", err)
	}
	orderID := order.Msg.GetSalesOrder().GetId()
	withLine, err := sales.AddSalesOrderLine(f.ctx, connect.NewRequest(
		&stillhousev1.AddSalesOrderLineRequest{
			SalesOrderId: orderID, ProductId: product.ID.String(),
			BottlesOrdered: 60, UnitPrice: "34.95",
		}))
	if err != nil {
		t.Fatalf("AddSalesOrderLine: %v", err)
	}
	if _, err := sales.SetSalesOrderStatus(f.ctx, connect.NewRequest(
		&stillhousev1.SetSalesOrderStatusRequest{
			Id: orderID, Status: stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_CONFIRMED,
		})); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	sh, err := sales.CreateShipment(f.ctx, connect.NewRequest(
		&stillhousev1.CreateShipmentRequest{SalesOrderId: orderID}))
	if err != nil {
		t.Fatalf("CreateShipment: %v", err)
	}
	shipmentID := sh.Msg.GetShipment().GetId()
	if _, err := sales.AddShipmentLine(f.ctx, connect.NewRequest(
		&stillhousev1.AddShipmentLineRequest{
			ShipmentId: shipmentID, PackagedInventoryId: lot.ID.String(),
			Bottles: 60, SalesOrderLineId: withLine.Msg.GetSalesOrder().GetLines()[0].GetId(),
		})); err != nil {
		t.Fatalf("AddShipmentLine: %v", err)
	}

	t.Run("a shipment that has not gone cannot be invoiced", func(t *testing.T) {
		if _, err := inv.CreateInvoice(f.ctx, connect.NewRequest(
			&stillhousev1.CreateInvoiceRequest{ShipmentId: shipmentID})); err == nil {
			t.Error("an invoice was raised for goods still in the warehouse")
		}
	})

	if _, err := sales.ShipShipment(f.ctx, connect.NewRequest(
		&stillhousev1.ShipShipmentRequest{Id: shipmentID, ShipDate: "2026-08-20"})); err != nil {
		t.Fatalf("ShipShipment: %v", err)
	}

	if _, err := inv.SaveTaxRate(f.ctx, connect.NewRequest(
		&stillhousev1.SaveTaxRateRequest{
			Jurisdiction: "CA-ON", Name: "HST", Rate: "0.13",
			EffectiveFrom: "2020-01-01",
			Provenance:    stillhousev1.RequirementProvenance_REQUIREMENT_PROVENANCE_INDICATIVE,
		})); err != nil {
		t.Fatalf("SaveTaxRate: %v", err)
	}

	t.Run("a rate typed as 13 instead of 0.13 is refused", func(t *testing.T) {
		// The mistake is easy and multiplies every invoice by fourteen.
		if _, err := inv.SaveTaxRate(f.ctx, connect.NewRequest(
			&stillhousev1.SaveTaxRateRequest{
				Jurisdiction: "CA-BC", Name: "PST", Rate: "7",
			})); err == nil {
			t.Error("a rate of 700 % was accepted")
		}
	})

	created, err := inv.CreateInvoice(f.ctx, connect.NewRequest(
		&stillhousev1.CreateInvoiceRequest{ShipmentId: shipmentID}))
	if err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}
	invoiceID := created.Msg.GetInvoice().GetId()

	t.Run("the lines come from what actually shipped, at the agreed price", func(t *testing.T) {
		i := created.Msg.GetInvoice()
		if len(i.GetLines()) != 1 {
			t.Fatalf("%d lines, want 1", len(i.GetLines()))
		}
		l := i.GetLines()[0]
		if !strings.Contains(l.GetDescription(), product.Name) {
			t.Errorf("description = %q, want the product name in it", l.GetDescription())
		}
		if got, want := l.GetLineTotal(), "2097.00"; got != want {
			t.Errorf("60 × 34.95 = %s, want %s", got, want)
		}
		if got, want := i.GetTermsDays(), int32(30); got != want {
			t.Errorf("terms = %d, want the customer's %d", got, want)
		}
	})

	issued, err := inv.IssueInvoice(f.ctx, connect.NewRequest(
		&stillhousev1.IssueInvoiceRequest{Id: invoiceID, IssueDate: "2026-08-21", TermsDays: -1}))
	if err != nil {
		t.Fatalf("IssueInvoice: %v", err)
	}

	t.Run("tax is applied at issue, exactly", func(t *testing.T) {
		i := issued.Msg.GetInvoice()
		if got, want := i.GetSubtotal(), "2097.00"; got != want {
			t.Errorf("subtotal = %s, want %s", got, want)
		}
		if got, want := i.GetTax(), "272.61"; got != want {
			t.Errorf("HST = %s, want %s", got, want)
		}
		if got, want := i.GetTotal(), "2369.61"; got != want {
			t.Errorf("total = %s, want %s", got, want)
		}
		if got, want := i.GetDueDate(), "2026-09-20"; got != want {
			t.Errorf("due = %s, want %s (30 days after issue)", got, want)
		}
		if i.GetLines()[0].GetTaxName() != "HST" {
			t.Errorf("tax name = %q", i.GetLines()[0].GetTaxName())
		}
		// Frozen at issue: a document already sent does not change.
		if i.GetBillToAddress() == "" {
			t.Error("the bill-to address was not copied onto the document")
		}
	})

	t.Run("an issued invoice cannot be edited", func(t *testing.T) {
		if _, err := inv.AddInvoiceLine(f.ctx, connect.NewRequest(
			&stillhousev1.AddInvoiceLineRequest{
				InvoiceId: invoiceID, Description: "freight", Quantity: "1", UnitPrice: "50",
			})); err == nil {
			t.Error("a line was added to a document already sent")
		}
	})

	t.Run("a part payment leaves it part paid, and the rest settles it", func(t *testing.T) {
		got, err := inv.RecordPayment(f.ctx, connect.NewRequest(
			&stillhousev1.RecordPaymentRequest{
				InvoiceId: invoiceID, Amount: "1000.00", ReceivedOn: "2026-09-01",
				Reference: "EFT 4471",
			}))
		if err != nil {
			t.Fatalf("RecordPayment: %v", err)
		}
		if want := stillhousev1.InvoiceStatus_INVOICE_STATUS_PART_PAID; got.Msg.GetInvoice().GetStatus() != want {
			t.Errorf("status = %v, want %v", got.Msg.GetInvoice().GetStatus(), want)
		}
		if got, want := got.Msg.GetInvoice().GetOutstanding(), "1369.61"; got != want {
			t.Errorf("outstanding = %s, want %s", got, want)
		}
		settled, err := inv.RecordPayment(f.ctx, connect.NewRequest(
			&stillhousev1.RecordPaymentRequest{
				InvoiceId: invoiceID, Amount: "1369.61", ReceivedOn: "2026-09-15",
			}))
		if err != nil {
			t.Fatalf("RecordPayment: %v", err)
		}
		if want := stillhousev1.InvoiceStatus_INVOICE_STATUS_PAID; settled.Msg.GetInvoice().GetStatus() != want {
			t.Errorf("status = %v, want %v", settled.Msg.GetInvoice().GetStatus(), want)
		}
		if got, want := settled.Msg.GetInvoice().GetOutstanding(), "0.00"; got != want {
			t.Errorf("outstanding = %s, want %s", got, want)
		}
	})

	t.Run("a paid invoice cannot be voided", func(t *testing.T) {
		// Money already received cannot be made not to have arrived.
		if _, err := inv.VoidInvoice(f.ctx, connect.NewRequest(
			&stillhousev1.VoidInvoiceRequest{Id: invoiceID, Reason: "mistake"})); err == nil {
			t.Error("an invoice with payments against it was voided")
		}
	})

	t.Run("a credit note credits it at the rate it was charged at", func(t *testing.T) {
		// Not today's rate: crediting at a different rate than was charged
		// leaves the customer owing the difference forever.
		if _, err := inv.SaveTaxRate(f.ctx, connect.NewRequest(
			&stillhousev1.SaveTaxRateRequest{
				Jurisdiction: "CA-ON", Name: "HST", Rate: "0.15",
				EffectiveFrom: "2026-09-01",
			})); err != nil {
			t.Fatalf("SaveTaxRate: %v", err)
		}
		note, err := inv.CreateCreditNote(f.ctx, connect.NewRequest(
			&stillhousev1.CreateCreditNoteRequest{
				InvoiceId: invoiceID, Reason: "two cases broken in transit",
			}))
		if err != nil {
			t.Fatalf("CreateCreditNote: %v", err)
		}
		n := note.Msg.GetCreditNote()
		if got, want := n.GetTax(), "272.61"; got != want {
			t.Errorf("credit tax = %s, want the %s originally charged", got, want)
		}
		if want := stillhousev1.InvoiceKind_INVOICE_KIND_CREDIT_NOTE; n.GetKind() != want {
			t.Errorf("kind = %v", n.GetKind())
		}
		if n.GetCreditsInvoiceId() != invoiceID {
			t.Error("the credit note does not say what it credits")
		}
	})

	t.Run("a credit note needs an invoice and a reason", func(t *testing.T) {
		if _, err := inv.CreateCreditNote(f.ctx, connect.NewRequest(
			&stillhousev1.CreateCreditNoteRequest{InvoiceId: invoiceID})); err == nil {
			t.Error("an unexplained reduction in revenue was accepted")
		}
	})
}

// The ageing buckets, and the thing that makes them useful: they run from
// the due date, not the issue date.
func TestARAgeingRunsFromTheDueDate(t *testing.T) {
	f := newLedgerFixture(t)
	inv := NewInvoicingService(f.db, testLogger())
	cust := f.salesCustomer(t, sqlcgen.RemovalDestinationKindDutyPaidCustomer)

	// Issued 45 days ago on 60-day terms: not overdue.
	created, err := inv.CreateInvoice(f.ctx, connect.NewRequest(
		&stillhousev1.CreateInvoiceRequest{CustomerId: cust.ID.String()}))
	if err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}
	id := created.Msg.GetInvoice().GetId()
	if _, err := inv.AddInvoiceLine(f.ctx, connect.NewRequest(
		&stillhousev1.AddInvoiceLineRequest{
			InvoiceId: id, Description: "Cask storage", Quantity: "1", UnitPrice: "500.00",
		})); err != nil {
		t.Fatalf("AddInvoiceLine: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx,
		"UPDATE invoices SET terms_days = 60 WHERE id = $1", uuid.MustParse(id)); err != nil {
		t.Fatalf("set terms: %v", err)
	}
	issued, err := inv.IssueInvoice(f.ctx, connect.NewRequest(
		&stillhousev1.IssueInvoiceRequest{
			Id: id, IssueDate: daysAgo(45), TermsDays: 60,
		}))
	if err != nil {
		t.Fatalf("IssueInvoice: %v", err)
	}
	if issued.Msg.GetInvoice().GetDaysOverdue() != 0 {
		t.Errorf("an invoice on 60-day terms issued 45 days ago reads as %d days "+
			"overdue — the buckets are running from the issue date",
			issued.Msg.GetInvoice().GetDaysOverdue())
	}

	ageing, err := inv.AgeingReport(f.ctx, connect.NewRequest(&stillhousev1.AgeingReportRequest{}))
	if err != nil {
		t.Fatalf("AgeingReport: %v", err)
	}
	var line *stillhousev1.AgeingLine
	for _, l := range ageing.Msg.GetLines() {
		if l.GetCustomerId() == cust.ID.String() {
			line = l
		}
	}
	if line == nil {
		t.Fatal("the customer with an outstanding invoice is not in the ageing")
	}
	buckets := map[string]string{}
	for _, b := range line.GetBuckets() {
		buckets[b.GetLabel()] = b.GetAmount()
	}
	if got, want := buckets["Not yet due"], "500.00"; got != want {
		t.Errorf("not-yet-due bucket = %s, want %s", got, want)
	}
	for _, label := range []string{"1–30 days", "31–60 days", "61–90 days", "Over 90 days"} {
		if got := buckets[label]; got != "0.00" {
			t.Errorf("%s bucket = %s, want 0.00 — an invoice within its terms is "+
				"not late", label, got)
		}
	}
	if ageing.Msg.GetBasis() == "" {
		t.Error("a report with no stated basis is a number nobody can check")
	}
}

func daysAgo(n int) string {
	return time.Now().UTC().AddDate(0, 0, -n).Format("2006-01-02")
}
