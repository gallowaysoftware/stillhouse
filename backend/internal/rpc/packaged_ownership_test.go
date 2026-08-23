package rpc

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/journal"
)

// A lot bottled from a customer's cask is theirs. Nothing was sold when
// it ships, so there is no cost of sales; the revenue is a service fee,
// and the bottles were never the licensee's inventory to value. It is
// still on the B266, because the licensee held the spirits.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestContractPackagedStockIsNotASale(t *testing.T) {
	f := newLedgerFixture(t)
	bulk := NewBulkService(f.db, testLogger())
	bottling := NewBottlingService(f.db, testLogger())
	removals := NewRemovalService(f.db, testLogger())
	costing := NewCostingService(f.db, testLogger())

	cust := f.salesCustomer(t, sqlcgen.RemovalDestinationKindDutyPaidCustomer)
	theirs := f.tank(t, "Theirs "+uuid.NewString()[:6], 1000, 50) // 500 LAA
	ours := f.tank(t, "Ours "+uuid.NewString()[:6], 1000, 50)     // 500 LAA
	if _, err := bulk.SetBulkContainerOwner(f.ctx, connect.NewRequest(
		&stillhousev1.SetBulkContainerOwnerRequest{
			Id: theirs.ID.String(), OwnerCustomerId: cust.ID.String(),
		})); err != nil {
		t.Fatalf("SetBulkContainerOwner: %v", err)
	}
	f.seedStamps(t, "CA-ON", 5000)

	product, err := f.q.CreateProduct(f.ctx, sqlcgen.CreateProductParams{
		TenantID: f.tenant.ID, Name: "Contract " + uuid.NewString()[:8],
		SpiritKind: sqlcgen.SpiritKindWhisky, BottleSizeMl: 750, TargetAbvPct: 40,
	})
	if err != nil {
		t.Fatalf("product: %v", err)
	}
	bottle := func(t *testing.T, from uuid.UUID, lot string) *stillhousev1.CreateBottlingRunResponse {
		t.Helper()
		got, err := bottling.CreateBottlingRun(f.ctx, connect.NewRequest(
			&stillhousev1.CreateBottlingRunRequest{
				ProductId: product.ID.String(), SourceContainerId: from.String(),
				DestinationJurisdiction: "CA-ON", BottleCount: 300,
				LotCode: lot, BottlingDate: "2026-08-01",
			}))
		if err != nil {
			t.Fatalf("CreateBottlingRun(%s): %v", lot, err)
		}
		return got.Msg
	}
	theirRun := bottle(t, theirs.ID, "THEIRS-"+uuid.NewString()[:6])
	ourRun := bottle(t, ours.ID, "OURS-"+uuid.NewString()[:6])

	t.Run("the lot carries the owner it was packaged under", func(t *testing.T) {
		lotID := uuid.MustParse(theirRun.GetPackaged().GetId())
		var owner uuid.NullUUID
		if err := f.pool.QueryRow(f.ctx,
			"SELECT owner_customer_id FROM packaged_inventory WHERE id = $1",
			lotID).Scan(&owner); err != nil {
			t.Fatalf("read lot: %v", err)
		}
		if !owner.Valid || owner.UUID != cust.ID {
			t.Errorf("lot owner = %v, want the customer %v", owner, cust.ID)
		}
	})

	t.Run("selling the cask afterwards does not restate the lot", func(t *testing.T) {
		// Copied at the run, not joined. A closed period cannot move
		// because the underlying cask changed hands later.
		if _, err := bulk.SetBulkContainerOwner(f.ctx, connect.NewRequest(
			&stillhousev1.SetBulkContainerOwnerRequest{Id: theirs.ID.String()},
		)); err != nil {
			t.Fatalf("clear owner: %v", err)
		}
		lotID := uuid.MustParse(theirRun.GetPackaged().GetId())
		var owner uuid.NullUUID
		if err := f.pool.QueryRow(f.ctx,
			"SELECT owner_customer_id FROM packaged_inventory WHERE id = $1",
			lotID).Scan(&owner); err != nil {
			t.Fatalf("read lot: %v", err)
		}
		if !owner.Valid {
			t.Error("the lot lost its owner when the cask was sold in place — a " +
				"period already closed would restate")
		}
	})

	// Ship some of each.
	for _, lot := range []string{
		theirRun.GetPackaged().GetId(), ourRun.GetPackaged().GetId(),
	} {
		if _, err := removals.CreateRemoval(f.ctx, connect.NewRequest(
			&stillhousev1.CreateRemovalRequest{
				PackagedInventoryId: lot, BottlesRemoved: 100,
				CustomerId: cust.ID.String(), RemovalDate: "2026-08-10",
			})); err != nil {
			t.Fatalf("CreateRemoval: %v", err)
		}
	}

	t.Run("cost of sales covers only what was ours", func(t *testing.T) {
		var j *journal.Journal
		if err := f.db.WithTenantTx(f.ctx, f.tenant.ID,
			func(ctx context.Context, q *sqlcgen.Queries) error {
				var e error
				j, e = journal.Build(ctx, q,
					mustDay("2026-08-01"), mustDay("2026-08-31"))
				return e
			}); err != nil {
			t.Fatalf("journal.Build: %v", err)
		}
		cogs := 0
		for _, l := range j.Lines {
			if l.Kind == sqlcgen.JournalEventKindCogsOnRemoval {
				cogs++
			}
		}
		if cogs > 1 {
			t.Errorf("%d cost-of-sales lines for two removals, one of which sold "+
				"nothing — a contract-packaged removal is not a sale", cogs)
		}
		// And the reader is told why the journal and the return differ.
		var told bool
		for _, n := range j.Notes {
			if strings.Contains(n, "service fee") {
				told = true
			}
		}
		if !told {
			t.Error("the journal says nothing about the removals it left out")
		}
		// The old warning was a figure known to be wrong with a note on
		// it. It should be gone.
		for _, w := range j.Warnings {
			if strings.Contains(w.Detail, "not modelled yet") {
				t.Errorf("the standing caveat survived the fix: %s", w.Detail)
			}
		}
	})

	t.Run("a customer's bottles are not the licensee's inventory", func(t *testing.T) {
		got, err := costing.InventoryValue(f.ctx, connect.NewRequest(
			&stillhousev1.InventoryValueRequest{}))
		if err != nil {
			t.Fatalf("InventoryValue: %v", err)
		}
		for _, l := range got.Msg.GetFinishedGoods().GetLines() {
			if strings.HasPrefix(l.GetName(), "THEIRS-") {
				t.Errorf("%s is a customer's stock and is being valued as the "+
					"licensee's inventory", l.GetName())
			}
		}
	})
}

func mustDay(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}
