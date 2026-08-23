package rpc

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The plan is built from what is actually owed, and says so. What it
// cannot take into account it names.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestProductionPlan(t *testing.T) {
	f := newLedgerFixture(t)
	sched := NewSchedulingService(f.db, testLogger())
	sales := NewSalesService(f.db, testLogger())
	equip := NewEquipmentService(f.db, testLogger())

	t.Run("no orders is not the same as nothing to make", func(t *testing.T) {
		got, err := sched.ProductionPlan(f.ctx, connect.NewRequest(
			&stillhousev1.ProductionPlanRequest{}))
		if err != nil {
			t.Fatalf("ProductionPlan: %v", err)
		}
		if len(got.Msg.GetDemand()) != 0 {
			t.Fatalf("%d demand lines with no orders", len(got.Msg.GetDemand()))
		}
		var said bool
		for _, b := range got.Msg.GetBlindSpots() {
			if strings.Contains(b, "nothing has been ordered") {
				said = true
			}
		}
		if !said {
			t.Error("an empty plan reads as 'nothing needs making' unless it says " +
				"that nothing has been ordered")
		}
		if got.Msg.GetBasis() == "" {
			t.Error("a plan with no stated basis is a plan nobody can check")
		}
	})

	cust := f.salesCustomer(t, sqlcgen.RemovalDestinationKindDutyPaidCustomer)
	product, lot := f.salesStock(t, 750, 40, 100)

	order, err := sales.CreateSalesOrder(f.ctx, connect.NewRequest(
		&stillhousev1.CreateSalesOrderRequest{
			CustomerId: cust.ID.String(), RequiredBy: "2026-12-31",
		}))
	if err != nil {
		t.Fatalf("CreateSalesOrder: %v", err)
	}
	orderID := order.Msg.GetSalesOrder().GetId()
	if _, err := sales.AddSalesOrderLine(f.ctx, connect.NewRequest(
		&stillhousev1.AddSalesOrderLineRequest{
			SalesOrderId: orderID, ProductId: product.ID.String(),
			BottlesOrdered: 400,
		})); err != nil {
		t.Fatalf("AddSalesOrderLine: %v", err)
	}
	if _, err := sales.SetSalesOrderStatus(f.ctx, connect.NewRequest(
		&stillhousev1.SetSalesOrderStatusRequest{
			Id: orderID, Status: stillhousev1.SalesOrderStatus_SALES_ORDER_STATUS_CONFIRMED,
		})); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	_ = lot

	t.Run("demand is what is owed, less what is here", func(t *testing.T) {
		got, err := sched.ProductionPlan(f.ctx, connect.NewRequest(
			&stillhousev1.ProductionPlanRequest{}))
		if err != nil {
			t.Fatalf("ProductionPlan: %v", err)
		}
		var line *stillhousev1.DemandLine
		for _, d := range got.Msg.GetDemand() {
			if d.GetProductId() == product.ID.String() {
				line = d
			}
		}
		if line == nil {
			t.Fatal("a confirmed order produced no demand line")
		}
		if got, want := line.GetBottlesOwed(), int32(400); got != want {
			t.Errorf("owed = %d, want %d", got, want)
		}
		if got, want := line.GetBottlesOnHand(), int32(100); got != want {
			t.Errorf("on hand = %d, want %d", got, want)
		}
		if got, want := line.GetShortfall(), int32(300); got != want {
			t.Errorf("shortfall = %d, want 400 − 100 = %d", got, want)
		}
		// 300 × 0.75 L × 40 % = 90 LAA.
		if got, want := line.GetShortfallLaa(), 90.0; !near(got, want, 1e-6) {
			t.Errorf("shortfall LAA = %v, want %v", got, want)
		}
		if line.GetLate() {
			t.Error("an order required at the end of the year is not late today")
		}
	})

	t.Run("it says whether there is enough alcohol to cover it", func(t *testing.T) {
		got, err := sched.ProductionPlan(f.ctx, connect.NewRequest(
			&stillhousev1.ProductionPlanRequest{}))
		if err != nil {
			t.Fatalf("ProductionPlan: %v", err)
		}
		// No bulk yet: 90 LAA short against nothing.
		if !got.Msg.GetShortOfAlcohol() {
			t.Error("90 LAA of shortfall against no bulk is not reported as short")
		}
		f.tank(t, "Plan tank "+uuid.NewString()[:6], 1000, 50) // 500 LAA
		got, err = sched.ProductionPlan(f.ctx, connect.NewRequest(
			&stillhousev1.ProductionPlanRequest{}))
		if err != nil {
			t.Fatalf("ProductionPlan: %v", err)
		}
		if got.Msg.GetShortOfAlcohol() {
			t.Errorf("500 LAA available against %v needed is not short",
				got.Msg.GetShortfallLaa())
		}
	})

	t.Run("plant that cannot be planned against says why", func(t *testing.T) {
		// An empty schedule and a schedule that silently omitted half the
		// still house look identical from the outside.
		if _, err := equip.SaveEquipment(f.ctx, connect.NewRequest(
			&stillhousev1.SaveEquipmentRequest{
				Name:      "Untimed still " + uuid.NewString()[:6],
				Kind:      stillhousev1.EquipmentKind_EQUIPMENT_KIND_STILL,
				CapacityL: 1000, CapacityLSet: true,
			})); err != nil {
			t.Fatalf("SaveEquipment: %v", err)
		}
		if _, err := equip.SaveEquipment(f.ctx, connect.NewRequest(
			&stillhousev1.SaveEquipmentRequest{
				Name:      "Sized still " + uuid.NewString()[:6],
				Kind:      stillhousev1.EquipmentKind_EQUIPMENT_KIND_STILL,
				CapacityL: 1000, CapacityLSet: true,
				TypicalRunHours: 8, TypicalRunHoursSet: true,
			})); err != nil {
			t.Fatalf("SaveEquipment: %v", err)
		}
		got, err := sched.ProductionPlan(f.ctx, connect.NewRequest(
			&stillhousev1.ProductionPlanRequest{}))
		if err != nil {
			t.Fatalf("ProductionPlan: %v", err)
		}
		var plannable, blocked int
		for _, e := range got.Msg.GetEquipment() {
			if e.GetPlannable() {
				plannable++
				continue
			}
			blocked++
			if e.GetWhyNot() == "" {
				t.Errorf("%s cannot be planned against and does not say why", e.GetName())
			}
		}
		if plannable == 0 {
			t.Error("a still with a capacity and a typical run time is not plannable")
		}
		if blocked == 0 {
			t.Error("a still with no run time is being planned against anyway")
		}
		var named bool
		for _, b := range got.Msg.GetBlindSpots() {
			if strings.Contains(b, "cannot be planned against") {
				named = true
			}
		}
		if !named {
			t.Error("the plan does not name what it left out")
		}
	})

	t.Run("a backwards window is refused", func(t *testing.T) {
		if _, err := sched.ProductionPlan(f.ctx, connect.NewRequest(
			&stillhousev1.ProductionPlanRequest{From: "2026-12-01", To: "2026-01-01"},
		)); err == nil {
			t.Error("a window ending before it starts produced a plan")
		}
	})
}
