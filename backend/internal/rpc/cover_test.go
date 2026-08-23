package rpc

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// Cover, and the two things that make it honest: nothing consumed means
// unknown rather than infinite, and no reorder point means no alert.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestMaterialCover(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewMaterialService(f.db, testLogger())

	mat, err := f.q.CreateMaterial(f.ctx, sqlcgen.CreateMaterialParams{
		TenantID: f.tenant.ID, Name: "Cover rye " + uuid.NewString()[:8],
		Kind: sqlcgen.MaterialKindGrain, Uom: "kg",
	})
	if err != nil {
		t.Fatalf("material: %v", err)
	}
	if _, err := f.q.CreateMaterialLot(f.ctx, sqlcgen.CreateMaterialLotParams{
		TenantID: f.tenant.ID, MaterialID: mat.ID, SupplierLot: "L1",
		QuantityReceived: 900,
		ReceivedAt:       pgtype.Timestamptz{Valid: true, Time: time.Now()},
	}); err != nil {
		t.Fatalf("lot: %v", err)
	}

	find := func(t *testing.T, res *stillhousev1.MaterialCoverResponse) *stillhousev1.MaterialCover {
		t.Helper()
		for _, m := range res.GetMaterials() {
			if m.GetMaterialId() == mat.ID.String() {
				return m
			}
		}
		t.Fatal("the material is missing from the cover report")
		return nil
	}

	t.Run("nothing consumed is unknown, not infinite", func(t *testing.T) {
		// A material nobody has used yet may be about to be used daily.
		got, err := svc.MaterialCover(f.ctx, connect.NewRequest(
			&stillhousev1.MaterialCoverRequest{}))
		if err != nil {
			t.Fatalf("MaterialCover: %v", err)
		}
		m := find(t, got.Msg)
		if m.GetCoverKnown() {
			t.Error("a material nothing has consumed reports a known cover")
		}
		if m.GetCoverDays() != 0 {
			t.Errorf("cover = %v with no consumption; it should be absent", m.GetCoverDays())
		}
		if got, want := m.GetOnHand(), 900.0; !near(got, want, 1e-6) {
			t.Errorf("on hand = %v, want %v", got, want)
		}
		if got.Msg.GetBasis() == "" {
			t.Error("a figure with no stated basis is one nobody can check")
		}
	})

	t.Run("no reorder point means no alert and no threshold", func(t *testing.T) {
		got, err := svc.MaterialCover(f.ctx, connect.NewRequest(
			&stillhousev1.MaterialCoverRequest{}))
		if err != nil {
			t.Fatalf("MaterialCover: %v", err)
		}
		m := find(t, got.Msg)
		if m.GetReorderPointSet() {
			t.Error("an unset reorder point reads as set")
		}
		if m.GetBelowReorderPoint() {
			t.Error("a material with no reorder point is below it")
		}
	})

	if _, err := svc.SetMaterialReorder(f.ctx, connect.NewRequest(
		&stillhousev1.SetMaterialReorderRequest{
			Id:           mat.ID.String(),
			ReorderPoint: 1000, ReorderPointSet: true,
			LeadTimeDays: 21, LeadTimeDaysSet: true,
		})); err != nil {
		t.Fatalf("SetMaterialReorder: %v", err)
	}

	t.Run("below the point the licensee chose", func(t *testing.T) {
		got, err := svc.MaterialCover(f.ctx, connect.NewRequest(
			&stillhousev1.MaterialCoverRequest{}))
		if err != nil {
			t.Fatalf("MaterialCover: %v", err)
		}
		m := find(t, got.Msg)
		if !m.GetBelowReorderPoint() {
			t.Errorf("900 kg on hand against a reorder point of 1000 is not below it")
		}
		if got, want := m.GetLeadTimeDays(), int32(21); got != want {
			t.Errorf("lead time = %d, want %d", got, want)
		}
	})

	t.Run("an order quantity of zero is refused", func(t *testing.T) {
		if _, err := svc.SetMaterialReorder(f.ctx, connect.NewRequest(
			&stillhousev1.SetMaterialReorderRequest{
				Id: mat.ID.String(), ReorderQuantity: 0, ReorderQuantitySet: true,
			})); err == nil {
			t.Error("an order quantity that orders nothing was accepted")
		}
	})

	t.Run("a ten-year window is not a consumption rate", func(t *testing.T) {
		if _, err := svc.MaterialCover(f.ctx, connect.NewRequest(
			&stillhousev1.MaterialCoverRequest{WindowDays: 100000})); err == nil {
			t.Error("an absurd window was accepted")
		}
	})
}
