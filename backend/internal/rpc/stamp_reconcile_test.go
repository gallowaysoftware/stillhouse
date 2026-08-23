package rpc

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// End to end, at the layer an auditor's question arrives at: order a
// range of stamps, bottle against them, lose some, and ask where every
// serial went.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestStampOrderReconcilesToTheSerial(t *testing.T) {
	f := newDutyFixture(t)
	// The stamp service on the fixture's RLS-enforcing handle, like every
	// other DB-backed test since stage 153.
	svc := NewExciseStampService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	order, err := f.q.CreateStampOrder(f.ctx, sqlcgen.CreateStampOrderParams{
		TenantID: f.tenant.ID, Jurisdiction: "CA-ON", QuantityOrdered: 100,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := f.q.ReceiveStampOrder(f.ctx, sqlcgen.ReceiveStampOrderParams{
		ID:               order.ID,
		ReceivedAt:       pgtype.Timestamptz{Valid: true, Time: time.Now()},
		QuantityReceived: 100,
		SerialStart:      pgtype.Text{String: "ONT00001", Valid: true},
		SerialEnd:        pgtype.Text{String: "ONT00100", Valid: true},
	}); err != nil {
		t.Fatalf("receive order: %v", err)
	}

	reconcile := func(t *testing.T) *stillhousev1.ReconcileStampOrderResponse {
		t.Helper()
		resp, err := svc.ReconcileStampOrder(f.ctx, connect.NewRequest(
			&stillhousev1.ReconcileStampOrderRequest{StampOrderId: order.ID.String()}))
		if err != nil {
			t.Fatalf("ReconcileStampOrder: %v", err)
		}
		return resp.Msg
	}

	t.Run("a fresh order is entirely on hand", func(t *testing.T) {
		got := reconcile(t)
		if !got.GetSerialRangeKnown() {
			t.Fatal("the recorded serial range was not recognised")
		}
		if got.GetReceivedCount() != 100 || got.GetAppliedCount() != 0 {
			t.Errorf("received %d applied %d, want 100 and 0",
				got.GetReceivedCount(), got.GetAppliedCount())
		}
		if len(got.GetDiscrepancies()) != 0 {
			t.Errorf("a fresh order reported discrepancies: %v", got.GetDiscrepancies())
		}
		var onHand int64
		for _, a := range got.GetAllocations() {
			if a.GetKind() == "on_hand" {
				onHand += a.GetCount()
			}
		}
		if onHand != 100 {
			t.Errorf("on hand %d, want 100", onHand)
		}
	})

	t.Run("losing stamps needs a reason and shows up as its own kind", func(t *testing.T) {
		if _, err := svc.RecordStampDisposition(f.ctx, connect.NewRequest(
			&stillhousev1.RecordStampDispositionRequest{
				StampOrderId: order.ID.String(),
				Kind:         stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_LOST,
				Quantity:     10,
				SerialStart:  "ONT00091", SerialEnd: "ONT00100",
			})); err == nil {
			t.Error("a loss was recorded with no explanation")
		}
		if _, err := svc.RecordStampDisposition(f.ctx, connect.NewRequest(
			&stillhousev1.RecordStampDispositionRequest{
				StampOrderId: order.ID.String(),
				Kind:         stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_LOST,
				Quantity:     10,
				SerialStart:  "ONT00091", SerialEnd: "ONT00100",
				Explanation: "Roll not on the shelf at the November count.",
				ReportedRef: "CRA-2026-11-02",
			})); err != nil {
			t.Fatalf("RecordStampDisposition: %v", err)
		}

		got := reconcile(t)
		if got.GetDisposedCount() != 10 {
			t.Errorf("disposed %d, want 10", got.GetDisposedCount())
		}
		var lostRange bool
		for _, a := range got.GetAllocations() {
			if a.GetKind() == "disposed" && a.GetPurpose() == "lost" &&
				a.GetSerialStart() == "ONT00091" && a.GetSerialEnd() == "ONT00100" {
				lostRange = true
			}
		}
		if !lostRange {
			t.Error("the lost range is not reported as its own allocation")
		}
		// The loss is distinguishable from spoilage, which is the whole
		// point of having a kind rather than one void counter.
		list, err := svc.ListStampDispositions(f.ctx, connect.NewRequest(
			&stillhousev1.ListStampDispositionsRequest{
				Kind: stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_LOST,
			}))
		if err != nil {
			t.Fatalf("ListStampDispositions: %v", err)
		}
		if len(list.Msg.GetDispositions()) != 1 {
			t.Fatalf("filtering for losses returned %d rows, want 1",
				len(list.Msg.GetDispositions()))
		}
		if list.Msg.GetDispositions()[0].GetReportedRef() != "CRA-2026-11-02" {
			t.Error("the CRA report reference did not survive")
		}
	})

	t.Run("a void still lands in the register, as spoilage", func(t *testing.T) {
		before := reconcile(t).GetDisposedCount()
		if _, err := svc.VoidStamps(f.ctx, connect.NewRequest(&stillhousev1.VoidStampsRequest{
			Id: order.ID.String(), Quantity: 3, Reason: "jammed in the applicator",
		})); err != nil {
			t.Fatalf("VoidStamps: %v", err)
		}
		got := reconcile(t)
		if got.GetDisposedCount() != before+3 {
			t.Errorf("disposed %d after a void of 3, want %d", got.GetDisposedCount(), before+3)
		}
		list, err := svc.ListStampDispositions(f.ctx, connect.NewRequest(
			&stillhousev1.ListStampDispositionsRequest{
				Kind: stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_SPOILED,
			}))
		if err != nil {
			t.Fatalf("ListStampDispositions: %v", err)
		}
		var found bool
		for _, d := range list.Msg.GetDispositions() {
			if d.GetExplanation() == "jammed in the applicator" {
				found = true
			}
		}
		if !found {
			t.Error("a void's reason did not reach the stamp record")
		}
	})

	t.Run("more accounted for than received is a discrepancy", func(t *testing.T) {
		// Push a disposition past what is left; the handler refuses,
		// which is the guard. The reconciliation's own arithmetic check
		// is covered in internal/stamps.
		_, err := svc.RecordStampDisposition(f.ctx, connect.NewRequest(
			&stillhousev1.RecordStampDispositionRequest{
				StampOrderId: order.ID.String(),
				Kind:         stillhousev1.StampDispositionKind_STAMP_DISPOSITION_KIND_DESTROYED,
				Quantity:     1000, Explanation: "impossible",
			}))
		if err == nil {
			t.Fatal("disposing of more stamps than the order holds was accepted")
		}
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("got %v, want InvalidArgument", connect.CodeOf(err))
		}
	})
}
