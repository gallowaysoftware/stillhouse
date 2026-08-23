package rpc

import (
	"log/slog"
	"os"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The reason a customer record earns its place, as against the free-text
// destination it replaces: a buyer's kind decides whether a removal to
// them is duty-paid, in bond, or an export, and that decision lands on a
// filed return. Recorded on the customer it is made once. Retyped on
// every removal it is made every time, next to a free-text name that
// never had to agree with it.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestRemovalTakesItsClassificationFromTheCustomer(t *testing.T) {
	f := newDutyFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	customers := NewCustomerService(f.db, log)

	newCustomer := func(t *testing.T, name string, kind stillhousev1.CustomerKind) *stillhousev1.Customer {
		t.Helper()
		resp, err := customers.CreateCustomer(f.ctx, connect.NewRequest(&stillhousev1.CreateCustomerRequest{
			Name: name + " " + uuid.NewString()[:8], Kind: kind,
			Jurisdiction: "CA-ON", PaymentTermsDays: -1,
		}))
		if err != nil {
			t.Fatalf("CreateCustomer(%s): %v", name, err)
		}
		return resp.Msg.GetCustomer()
	}

	// The excise consequence of who the buyer is, taken from the buyer.
	board := newCustomer(t, "Provincial board", stillhousev1.CustomerKind_CUSTOMER_KIND_PROVINCIAL_BOARD)
	if got := board.GetDefaultDestinationKind(); got != stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER {
		t.Errorf("a provincial board defaulted to %v, want duty-paid customer", got)
	}
	licensee := newCustomer(t, "Another distillery", stillhousev1.CustomerKind_CUSTOMER_KIND_SPIRITS_LICENSEE)
	if got := licensee.GetDefaultDestinationKind(); got != stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_TRANSFER_OUT_IN_BOND {
		t.Errorf("another spirits licensee defaulted to %v, want transfer out in bond", got)
	}
	exporter := newCustomer(t, "Overseas buyer", stillhousev1.CustomerKind_CUSTOMER_KIND_EXPORT)
	if got := exporter.GetDefaultDestinationKind(); got != stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_EXPORT {
		t.Errorf("an export customer defaulted to %v, want export", got)
	}

	// A lot to remove from.
	tank := f.tank(t, "customer-src", 500, 60)
	product := f.product(t, "Customer Test Vodka "+uuid.NewString()[:8], 750, 40)
	f.stamps(t, "CA-ON", 1000)
	run, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		SourceContainerId:       tank.ID.String(),
		ProductId:               product.ID.String(),
		DestinationJurisdiction: "CA-ON",
		BottleCount:             300,
		LotCode:                 "LOT-" + uuid.NewString()[:8],
	}))
	if err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}
	lotID := run.Msg.GetPackaged().GetId()

	t.Run("naming a customer sets the kind and the name", func(t *testing.T) {
		resp, err := f.removal.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
			PackagedInventoryId: lotID,
			BottlesRemoved:      10,
			CustomerId:          board.GetId(),
			// Deliberately wrong, and deliberately ignored: the request
			// must not be able to reclassify a movement away from what
			// the buyer is.
			DestinationKind: stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_EXPORT,
			DestinationName: "typed by hand",
		}))
		if err != nil {
			t.Fatalf("CreateRemoval: %v", err)
		}
		r := resp.Msg.GetRemoval()
		if r.GetDestinationKind() != stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER {
			t.Errorf("destination kind %v; the request overrode the customer's classification",
				r.GetDestinationKind())
		}
		if r.GetDestinationName() != board.GetName() {
			t.Errorf("destination name %q, want the customer's name %q",
				r.GetDestinationName(), board.GetName())
		}
		if r.GetCustomerId() != board.GetId() {
			t.Errorf("removal does not point at the customer")
		}
	})

	t.Run("a removal to another licensee is not duty-paid", func(t *testing.T) {
		resp, err := f.removal.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
			PackagedInventoryId: lotID, BottlesRemoved: 5, CustomerId: licensee.GetId(),
		}))
		if err != nil {
			t.Fatalf("CreateRemoval: %v", err)
		}
		if got := resp.Msg.GetRemoval().GetDestinationKind(); got != stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_TRANSFER_OUT_IN_BOND {
			t.Errorf("destination kind %v, want transfer out in bond", got)
		}
	})

	t.Run("no customer still accepts a typed destination", func(t *testing.T) {
		// The one-off case, and every removal recorded before customers
		// existed. It has to keep working.
		resp, err := f.removal.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
			PackagedInventoryId: lotID, BottlesRemoved: 3,
			DestinationKind: stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_SAMPLE,
			DestinationName: "CFIA sample",
		}))
		if err != nil {
			t.Fatalf("CreateRemoval: %v", err)
		}
		r := resp.Msg.GetRemoval()
		if r.GetDestinationKind() != stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_SAMPLE {
			t.Errorf("destination kind %v, want sample", r.GetDestinationKind())
		}
		if r.GetCustomerId() != "" {
			t.Error("a removal with no customer came back pointing at one")
		}
	})

	t.Run("totals answer the question free text could not", func(t *testing.T) {
		got, err := customers.GetCustomer(f.ctx, connect.NewRequest(
			&stillhousev1.GetCustomerRequest{Id: board.GetId()}))
		if err != nil {
			t.Fatalf("GetCustomer: %v", err)
		}
		if got.Msg.GetRemovalCount() != 1 {
			t.Errorf("removal_count %d, want 1", got.Msg.GetRemovalCount())
		}
		if got.Msg.GetBottlesRemoved() != 10 {
			t.Errorf("bottles_removed %d, want 10", got.Msg.GetBottlesRemoved())
		}
		// 10 bottles × 750 mL × 40% = 3 LAA.
		if laa := got.Msg.GetTotalLaa(); laa < 2.99 || laa > 3.01 {
			t.Errorf("total_laa %v, want ~3", laa)
		}
	})

	t.Run("an archived customer cannot take a new removal", func(t *testing.T) {
		if _, err := customers.SetCustomerArchived(f.ctx, connect.NewRequest(
			&stillhousev1.SetCustomerArchivedRequest{Id: exporter.GetId(), Archived: true})); err != nil {
			t.Fatalf("SetCustomerArchived: %v", err)
		}
		_, err := f.removal.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
			PackagedInventoryId: lotID, BottlesRemoved: 1, CustomerId: exporter.GetId(),
		}))
		if err == nil {
			t.Fatal("an archived customer accepted a removal")
		}
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("got %v, want FailedPrecondition", connect.CodeOf(err))
		}
	})
}

// Money on a price list crosses the wire as a decimal string. Rendering
// 34.95 as a double and back is how a cent goes missing, and these are
// amounts somebody invoices.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestPriceListKeepsItsCents(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewCustomerService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	product := f.product(t, "Priced Gin "+uuid.NewString()[:8], 750, 43)

	list, err := svc.CreatePriceList(f.ctx, connect.NewRequest(&stillhousev1.CreatePriceListRequest{
		Name:          "LCBO wholesale " + uuid.NewString()[:8],
		Channel:       stillhousev1.SalesChannel_SALES_CHANNEL_WHOLESALE,
		Jurisdiction:  "CA-ON",
		EffectiveFrom: "2026-01-01",
	}))
	if err != nil {
		t.Fatalf("CreatePriceList: %v", err)
	}
	listID := list.Msg.GetPriceList().GetId()

	for _, price := range []string{"34.95", "0.01", "1234.5678"} {
		resp, err := svc.SetPriceListEntry(f.ctx, connect.NewRequest(&stillhousev1.SetPriceListEntryRequest{
			PriceListId: listID, ProductId: product.ID.String(), UnitPrice: price, CaseSize: 12,
		}))
		if err != nil {
			t.Fatalf("SetPriceListEntry(%s): %v", price, err)
		}
		entries := resp.Msg.GetPriceList().GetEntries()
		if len(entries) != 1 {
			t.Fatalf("got %d entries, want 1", len(entries))
		}
		if got := entries[0].GetUnitPrice(); got != price {
			t.Errorf("stored %s, read back %s", price, got)
		}
		if got := entries[0].GetCaseSize(); got != 12 {
			t.Errorf("case_size %d, want 12", got)
		}
	}

	// An empty price removes the entry: "no price for this product on
	// this list" is a real state and is not the same as zero.
	resp, err := svc.SetPriceListEntry(f.ctx, connect.NewRequest(&stillhousev1.SetPriceListEntryRequest{
		PriceListId: listID, ProductId: product.ID.String(), UnitPrice: "",
	}))
	if err != nil {
		t.Fatalf("SetPriceListEntry(remove): %v", err)
	}
	if n := len(resp.Msg.GetPriceList().GetEntries()); n != 0 {
		t.Errorf("after removal the list still has %d entries", n)
	}

	if _, err := svc.SetPriceListEntry(f.ctx, connect.NewRequest(&stillhousev1.SetPriceListEntryRequest{
		PriceListId: listID, ProductId: product.ID.String(), UnitPrice: "not a number",
	})); err == nil {
		t.Error("a non-numeric price was accepted")
	}
}
