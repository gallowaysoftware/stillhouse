package rpc

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/alerting"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// A work order is an intention with a subject, an owner and a date. The
// properties worth pinning are the ones that keep it from becoming a
// second production record: it is about one thing, it cannot be edited
// once it is history, and its timestamps are stamped by the transition
// rather than typed.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestWorkOrders(t *testing.T) {
	f := newLedgerFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	svc := NewWorkOrderService(f.db, log)
	tag := uuid.NewString()[:8]
	tank := f.tank(t, "WO tank "+tag, 500, 60)

	create := func(t *testing.T, req *stillhousev1.SaveWorkOrderRequest) *stillhousev1.WorkOrder {
		t.Helper()
		resp, err := svc.SaveWorkOrder(f.ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatalf("SaveWorkOrder: %v", err)
		}
		return resp.Msg.GetWorkOrder()
	}

	t.Run("a job needs a title", func(t *testing.T) {
		if _, err := svc.SaveWorkOrder(f.ctx, connect.NewRequest(
			&stillhousev1.SaveWorkOrderRequest{
				Kind: stillhousev1.WorkOrderKind_WORK_ORDER_KIND_CLEANING,
			})); err == nil {
			t.Error("a work order with no title was accepted")
		}
	})

	t.Run("it is about one thing", func(t *testing.T) {
		if _, err := svc.SaveWorkOrder(f.ctx, connect.NewRequest(
			&stillhousev1.SaveWorkOrderRequest{
				Kind:  stillhousev1.WorkOrderKind_WORK_ORDER_KIND_REGAUGE,
				Title: "Two subjects", ContainerId: tank.ID.String(),
				RecipeId: uuid.NewString(),
			})); err == nil {
			t.Error("a work order about both a cask and a recipe was accepted")
		}
	})

	t.Run("unassigned is a real state", func(t *testing.T) {
		// Planning a week is mostly unassigned jobs on a board; refusing
		// them would make the feature useless for the thing it is for.
		w := create(t, &stillhousev1.SaveWorkOrderRequest{
			Kind:  stillhousev1.WorkOrderKind_WORK_ORDER_KIND_MAINTENANCE,
			Title: "Service the pump " + tag,
		})
		if w.GetAssignedTo() != "" {
			t.Error("an unassigned work order came back assigned")
		}
		if w.GetWorkOrderNo() <= 0 {
			t.Error("no work order number was allocated")
		}
	})

	t.Run("a due date before the scheduled day is refused", func(t *testing.T) {
		if _, err := svc.SaveWorkOrder(f.ctx, connect.NewRequest(
			&stillhousev1.SaveWorkOrderRequest{
				Kind:  stillhousev1.WorkOrderKind_WORK_ORDER_KIND_CLEANING,
				Title: "Backwards", ScheduledFor: "2026-09-10", DueOn: "2026-09-01",
			})); err == nil {
			t.Error("a work order due before it is scheduled was accepted")
		}
	})

	t.Run("timestamps are stamped by the transition", func(t *testing.T) {
		w := create(t, &stillhousev1.SaveWorkOrderRequest{
			Kind:        stillhousev1.WorkOrderKind_WORK_ORDER_KIND_REGAUGE,
			Title:       "Regauge " + tag,
			ContainerId: tank.ID.String(),
			AssignedTo:  f.user.ID.String(),
		})
		if w.GetStartedAt() != nil {
			t.Error("a planned work order already has a start time")
		}
		started, err := svc.SetWorkOrderStatus(f.ctx, connect.NewRequest(
			&stillhousev1.SetWorkOrderStatusRequest{
				Id: w.GetId(), Status: stillhousev1.WorkOrderStatus_WORK_ORDER_STATUS_IN_PROGRESS,
			}))
		if err != nil {
			t.Fatalf("SetWorkOrderStatus: %v", err)
		}
		if started.Msg.GetWorkOrder().GetStartedAt() == nil {
			t.Error("starting a work order recorded no start time")
		}
		done, err := svc.SetWorkOrderStatus(f.ctx, connect.NewRequest(
			&stillhousev1.SetWorkOrderStatusRequest{
				Id: w.GetId(), Status: stillhousev1.WorkOrderStatus_WORK_ORDER_STATUS_DONE,
			}))
		if err != nil {
			t.Fatalf("SetWorkOrderStatus: %v", err)
		}
		if done.Msg.GetWorkOrder().GetCompletedAt() == nil {
			t.Error("finishing a work order recorded no completion time")
		}

		// And it is history now: editing a plan and editing a record are
		// different acts.
		if _, err := svc.SaveWorkOrder(f.ctx, connect.NewRequest(
			&stillhousev1.SaveWorkOrderRequest{
				Id: w.GetId(), Kind: stillhousev1.WorkOrderKind_WORK_ORDER_KIND_REGAUGE,
				Title: "Edited after the fact",
			})); err == nil {
			t.Error("a finished work order was edited")
		}
	})

	t.Run("cancelling needs a reason", func(t *testing.T) {
		w := create(t, &stillhousev1.SaveWorkOrderRequest{
			Kind: stillhousev1.WorkOrderKind_WORK_ORDER_KIND_OTHER, Title: "To cancel " + tag,
		})
		if _, err := svc.SetWorkOrderStatus(f.ctx, connect.NewRequest(
			&stillhousev1.SetWorkOrderStatusRequest{
				Id: w.GetId(), Status: stillhousev1.WorkOrderStatus_WORK_ORDER_STATUS_CANCELLED,
			})); err == nil {
			t.Error("a work order was cancelled with no reason")
		}
	})

	t.Run("the board can be filtered to me", func(t *testing.T) {
		mine, err := svc.ListWorkOrders(f.ctx, connect.NewRequest(
			&stillhousev1.ListWorkOrdersRequest{AssignedTo: "me", OpenOnly: true}))
		if err != nil {
			t.Fatalf("ListWorkOrders: %v", err)
		}
		for _, w := range mine.Msg.GetWorkOrders() {
			if w.GetAssignedTo() != f.user.ID.String() {
				t.Errorf("work order %d is on my board but assigned to %q",
					w.GetWorkOrderNo(), w.GetAssignedTo())
			}
		}
	})
}

// Overdue work raises an alert; work merely scheduled in the past does
// not. A system that shouts about a job done a day late is a system
// people mute — and the same channel carries the return deadline.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestOverdueWorkAlertsButLateStartsDoNot(t *testing.T) {
	f := newLedgerFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	svc := NewWorkOrderService(f.db, log)
	runner := alerting.NewRunner(f.db, f.q, nil, "http://example.test", time.Hour, log)
	alerts := NewAlertService(f.db, runner, log)
	now := time.Now().UTC()
	tag := uuid.NewString()[:8]

	tenant, err := f.q.GetTenantByID(f.ctx, f.tenant.ID)
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}

	// Scheduled last week, no due date. Ordinary.
	if _, err := svc.SaveWorkOrder(f.ctx, connect.NewRequest(&stillhousev1.SaveWorkOrderRequest{
		Kind:         stillhousev1.WorkOrderKind_WORK_ORDER_KIND_CLEANING,
		Title:        "Scheduled but not due " + tag,
		ScheduledFor: now.AddDate(0, 0, -7).Format("2006-01-02"),
	})); err != nil {
		t.Fatalf("SaveWorkOrder: %v", err)
	}
	// Due last week. Overdue.
	overdue, err := svc.SaveWorkOrder(f.ctx, connect.NewRequest(&stillhousev1.SaveWorkOrderRequest{
		Kind:         stillhousev1.WorkOrderKind_WORK_ORDER_KIND_MAINTENANCE,
		Title:        "Overdue " + tag,
		ScheduledFor: now.AddDate(0, 0, -14).Format("2006-01-02"),
		DueOn:        now.AddDate(0, 0, -5).Format("2006-01-02"),
	}))
	if err != nil {
		t.Fatalf("SaveWorkOrder: %v", err)
	}

	if err := runner.RunTenant(f.ctx, tenant, now); err != nil {
		t.Fatalf("RunTenant: %v", err)
	}
	resp, err := alerts.ListAlerts(f.ctx, connect.NewRequest(&stillhousev1.ListAlertsRequest{}))
	if err != nil {
		t.Fatalf("ListAlerts: %v", err)
	}
	var raised int
	var sawOverdue bool
	for _, a := range resp.Msg.GetAlerts() {
		if a.GetKind() != stillhousev1.AlertKind_ALERT_KIND_WORK_ORDER_OVERDUE {
			continue
		}
		raised++
		if a.GetEntityId() == overdue.Msg.GetWorkOrder().GetId() {
			sawOverdue = true
		}
	}
	if !sawOverdue {
		t.Error("a work order five days past its due date raised no alert")
	}
	if raised != 1 {
		t.Errorf("%d work-order alerts raised, want 1 — a job merely scheduled in the "+
			"past is an ordinary week and must not shout", raised)
	}
}
