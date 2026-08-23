package rpc

import (
	"log/slog"
	"os"
	"testing"

	"connectrpc.com/connect"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The DB-backed tests in this package drive the handlers through a pool
// that row-level security applies to (see internal/testdb), so every one
// of them exercises the tenant boundary incidentally. This test exercises
// it on purpose, at the layer an attacker would actually reach: the RPC
// surface, with a real user context, asking for another tenant's row by
// its id.
//
// The previous tenant-isolation test proved the policies act on a raw
// ListMaterials. What it could not prove is that a *handler* is scoped —
// a handler that reached the pool directly instead of going through
// WithTenantTx would sail past it. This one fails in that case.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestHandlersDoNotCrossTheTenantBoundary(t *testing.T) {
	// Two independent fixtures — two tenants, two owners, one database.
	a := newLedgerFixture(t)
	b := newLedgerFixture(t)
	if a.tenant.ID == b.tenant.ID {
		t.Fatal("fixtures produced the same tenant")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	bulkA := NewBulkService(a.db, log)
	bulkB := NewBulkService(b.db, log)

	// Tenant B fills a tank. Seeded through B's own admin handle, so the
	// row is unambiguously B's.
	tankB := b.tank(t, "B-tank", 1000, 60)

	t.Run("read", func(t *testing.T) {
		// A asks for B's container by id. It exists, and A holds a valid
		// session — the only thing between them is the policy.
		_, err := bulkA.GetBulkContainer(a.ctx, connect.NewRequest(
			&stillhousev1.GetBulkContainerRequest{Id: tankB.ID.String()}))
		if err == nil {
			t.Fatal("tenant A read tenant B's bulk container — the tenant boundary is not holding")
		}
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("got %v (%v); want NotFound — another tenant's row must be "+
				"indistinguishable from a missing one", connect.CodeOf(err), err)
		}
	})

	t.Run("list", func(t *testing.T) {
		resp, err := bulkA.ListBulkContainers(a.ctx, connect.NewRequest(
			&stillhousev1.ListBulkContainersRequest{}))
		if err != nil {
			t.Fatalf("ListBulkContainers: %v", err)
		}
		for _, c := range resp.Msg.GetContainers() {
			if c.GetId() == tankB.ID.String() {
				t.Fatal("tenant A listed tenant B's bulk container")
			}
		}
	})

	t.Run("write", func(t *testing.T) {
		// The direction that matters most: not reading someone else's
		// figures, but altering them. A archiving B's tank would take a
		// live container holding alcohol off B's books.
		_, err := bulkA.SetBulkContainerArchived(a.ctx, connect.NewRequest(
			&stillhousev1.SetBulkContainerArchivedRequest{
				Id: tankB.ID.String(), Archived: true,
			}))
		if err == nil {
			t.Error("tenant A archived tenant B's bulk container")
		}
		resp, err := bulkB.GetBulkContainer(b.ctx, connect.NewRequest(
			&stillhousev1.GetBulkContainerRequest{Id: tankB.ID.String()}))
		if err != nil {
			t.Fatalf("re-read B's container: %v", err)
		}
		if resp.Msg.GetContainer().GetArchived() {
			t.Error("tenant B's container came back archived after tenant A's attempt")
		}
	})
}
