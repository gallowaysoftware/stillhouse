package rpc

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// EDM3-8-1: a container of 100 to 1,500 litres, marked rather than
// stamped, for delivery to a registered user. Packaging, with its own
// B266 line, and a way back to bulk under s.156 that bottles do not have.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestMarkedSpecialContainers(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewMarkedContainerService(f.db, testLogger())
	tank := f.tank(t, "MSC source "+uuid.NewString()[:6], 2000, 50) // 1000 LAA

	t.Run("a vessel outside the range is not one", func(t *testing.T) {
		for _, capacity := range []float64{50, 99.9, 1500.1, 2000} {
			_, err := svc.MarkContainer(f.ctx, connect.NewRequest(
				&stillhousev1.MarkContainerRequest{
					Mark: "EDM3-8-1 mark", CapacityL: capacity,
					SourceContainerId: tank.ID.String(), VolumeL: 50, AbvPct: 50,
				}))
			if err == nil {
				t.Errorf("%.1f L was accepted as a marked special container", capacity)
				continue
			}
			if !strings.Contains(err.Error(), "1500") {
				t.Errorf("the refusal should name the range, got: %v", err)
			}
		}
	})

	t.Run("an unmarked container is not one either", func(t *testing.T) {
		if _, err := svc.MarkContainer(f.ctx, connect.NewRequest(
			&stillhousev1.MarkContainerRequest{
				CapacityL: 200, SourceContainerId: tank.ID.String(),
				VolumeL: 180, AbvPct: 50,
			})); err == nil {
			t.Error("a container with no mark was accepted")
		}
	})

	t.Run("more than fits is refused", func(t *testing.T) {
		if _, err := svc.MarkContainer(f.ctx, connect.NewRequest(
			&stillhousev1.MarkContainerRequest{
				Mark: "M", CapacityL: 200, SourceContainerId: tank.ID.String(),
				VolumeL: 250, AbvPct: 50,
			})); err == nil {
			t.Error("250 L went into a 200 L container")
		}
	})

	marked, err := svc.MarkContainer(f.ctx, connect.NewRequest(
		&stillhousev1.MarkContainerRequest{
			Mark: "SPECIAL CONTAINER — SPIRITS — 200 L", CapacityL: 200,
			SourceContainerId: tank.ID.String(), VolumeL: 180, AbvPct: 50,
			FilledOn: "2026-08-05", Description: "keg for a BYO premises",
		}))
	if err != nil {
		t.Fatalf("MarkContainer: %v", err)
	}
	c := marked.Msg.GetContainer()
	containerID := c.GetId()

	t.Run("filling draws from bulk", func(t *testing.T) {
		if got, want := c.GetLaa(), 90.0; !near(got, want, 1e-6) {
			t.Errorf("LAA = %v, want 180 L at 50 %% = %v", got, want)
		}
		after, err := f.q.GetBulkContainer(f.ctx, tank.ID)
		if err != nil {
			t.Fatalf("re-read tank: %v", err)
		}
		if got, want := after.CurrentLaa, 910.0; !near(got, want, 1e-6) {
			t.Errorf("tank LAA = %v, want %v — the packaging act did not debit bulk",
				got, want)
		}
	})

	t.Run("no excise stamp was drawn", func(t *testing.T) {
		// They are marked, which is the whole distinction. A licensee
		// whose circumstances differ sees that none was consumed rather
		// than discovering a silent one.
		var used int
		if err := f.pool.QueryRow(f.ctx,
			"SELECT COUNT(*) FROM bottling_run_stamp_usage WHERE tenant_id = $1",
			f.tenant.ID).Scan(&used); err != nil {
			t.Fatalf("count stamp usage: %v", err)
		}
		if used != 0 {
			t.Errorf("%d stamp usages recorded for a marked container", used)
		}
	})

	t.Run("it lands on the third column of the packaging split", func(t *testing.T) {
		b266 := NewB266Service(f.db, testLogger())
		got, err := b266.GenerateB266(f.ctx, connect.NewRequest(
			&stillhousev1.GenerateB266Request{
				PeriodStart: "2026-08-01", PeriodEnd: "2026-08-31",
			}))
		if err != nil {
			t.Fatalf("GenerateB266: %v", err)
		}
		r := got.Msg.GetReport()
		if got, want := r.GetPackagedMarkedContainersLaa(), 90.0; !near(got, want, 1e-6) {
			t.Errorf("marked containers LAA = %v, want %v", got, want)
		}
		if got, want := r.GetPackagedMarkedContainersCount(), int32(1); got != want {
			t.Errorf("count = %d, want %d", got, want)
		}
		// And not into the bottle columns, which are counted in bottles.
		if r.GetPackagedDutyPaidBottles() != 0 || r.GetPackagedNonDutyPaidBottles() != 0 {
			t.Error("a marked container was counted as bottles")
		}
	})

	t.Run("delivering it", func(t *testing.T) {
		cust := f.salesCustomer(t, sqlcgen.RemovalDestinationKindDutyPaidCustomer)
		got, err := svc.DeliverMarkedContainer(f.ctx, connect.NewRequest(
			&stillhousev1.DeliverMarkedContainerRequest{
				ContainerId: containerID, CustomerId: cust.ID.String(),
				DeliveryDate: "2026-08-10", Reference: "BOL-9",
			}))
		if err != nil {
			t.Fatalf("DeliverMarkedContainer: %v", err)
		}
		d := got.Msg.GetDelivery()
		if got, want := d.GetLaa(), 90.0; !near(got, want, 1e-6) {
			t.Errorf("delivered LAA = %v, want %v", got, want)
		}
		if d.GetCustomerName() != cust.Name {
			t.Errorf("customer = %q, want %q", d.GetCustomerName(), cust.Name)
		}
		if want := stillhousev1.MarkedContainerStatus_MARKED_CONTAINER_STATUS_DELIVERED; got.Msg.GetContainer().GetStatus() != want {
			t.Errorf("status = %v, want %v", got.Msg.GetContainer().GetStatus(), want)
		}
		// Duty on the way out, because this tenant is not at-packaging.
		if got.Msg.GetDutyCad() <= 0 {
			t.Error("a container that carried no duty at the fill left with none either")
		}
		if _, err := svc.DeliverMarkedContainer(f.ctx, connect.NewRequest(
			&stillhousev1.DeliverMarkedContainerRequest{
				ContainerId: containerID, CustomerId: cust.ID.String(),
			})); err == nil {
			t.Error("a container that had already gone was delivered again")
		}
	})
}

// s.156: the mark comes off and the contents go back to bulk. A movement
// in the ledger, not a correction — the alcohol really did go back, and
// the return must stop saying it was packaged.
func TestUnmarkingReturnsTheAlcoholToBulk(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewMarkedContainerService(f.db, testLogger())
	b266 := NewB266Service(f.db, testLogger())
	source := f.tank(t, "Unmark src "+uuid.NewString()[:6], 1000, 50) // 500 LAA
	dest := f.tank(t, "Unmark dst "+uuid.NewString()[:6], 100, 40)    // 40 LAA

	marked, err := svc.MarkContainer(f.ctx, connect.NewRequest(
		&stillhousev1.MarkContainerRequest{
			Mark: "M-1", CapacityL: 500, SourceContainerId: source.ID.String(),
			VolumeL: 400, AbvPct: 50, FilledOn: "2026-08-05",
		}))
	if err != nil {
		t.Fatalf("MarkContainer: %v", err)
	}
	id := marked.Msg.GetContainer().GetId()

	t.Run("a reason is required", func(t *testing.T) {
		if _, err := svc.UnmarkContainer(f.ctx, connect.NewRequest(
			&stillhousev1.UnmarkContainerRequest{
				Id: id, DestinationContainerId: dest.ID.String(),
			})); err == nil {
			t.Error("alcohol left the packaging figures with no explanation")
		}
	})

	got, err := svc.UnmarkContainer(f.ctx, connect.NewRequest(
		&stillhousev1.UnmarkContainerRequest{
			Id: id, DestinationContainerId: dest.ID.String(),
			UnmarkedOn: "2026-08-12", Reason: "customer cancelled; returned to stock",
		}))
	if err != nil {
		t.Fatalf("UnmarkContainer: %v", err)
	}
	if got, want := got.Msg.GetReturnedLaa(), 200.0; !near(got, want, 1e-6) {
		t.Errorf("returned = %v, want %v", got, want)
	}

	after, err := f.q.GetBulkContainer(f.ctx, dest.ID)
	if err != nil {
		t.Fatalf("re-read destination: %v", err)
	}
	if got, want := after.CurrentLaa, 240.0; !near(got, want, 1e-6) {
		t.Errorf("destination LAA = %v, want 40 + 200 = %v", got, want)
	}
	// Volume-weighted strength, the same arithmetic every other receipt uses.
	if got, want := after.CurrentAbvPct.Float64, 240.0/500.0*100; !near(got, want, 1e-6) {
		t.Errorf("destination ABV = %v, want %v", got, want)
	}

	// The return must stop saying it was packaged.
	report, err := b266.GenerateB266(f.ctx, connect.NewRequest(
		&stillhousev1.GenerateB266Request{
			PeriodStart: "2026-08-01", PeriodEnd: "2026-08-31",
		}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	if got := report.Msg.GetReport().GetPackagedMarkedContainersLaa(); got != 0 {
		t.Errorf("an unmarked container is still reported as packaged (%v LAA) — "+
			"the return would say it was packaged and nothing about its coming back",
			got)
	}

	t.Run("it cannot be unmarked twice", func(t *testing.T) {
		if _, err := svc.UnmarkContainer(f.ctx, connect.NewRequest(
			&stillhousev1.UnmarkContainerRequest{
				Id: id, DestinationContainerId: dest.ID.String(), Reason: "again",
			})); err == nil {
			t.Error("the same alcohol was returned to bulk twice")
		}
	})
}
