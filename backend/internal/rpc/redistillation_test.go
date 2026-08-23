package rpc

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/alerting"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// A redistillation is the one operation where alcohol legitimately
// disappears in bulk and nobody is obliged to notice. What A8 asked for
// is the record either side of the reportable movement: quantity taken,
// quantity produced, and the loss between them.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestRedistillationAccountsForWhatDidNotComeBack(t *testing.T) {
	f := newLedgerFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	svc := NewRedistillationService(f.db, log)
	tag := uuid.NewString()[:8]

	// 1000 L at 70% = 700 LAA.
	tank := f.tank(t, "Redistill source "+tag, 1000, 70)

	t.Run("the withdrawal is the reportable movement and the ledger moves", func(t *testing.T) {
		_, beforeLAA := f.balance(t, tank.ID)
		// 100 L at 70% = 70 LAA.
		got, err := svc.StartRedistillation(f.ctx, connect.NewRequest(
			&stillhousev1.StartRedistillationRequest{
				SourceContainerId: tank.ID.String(),
				Reason:            stillhousev1.RedistillationReason_REDISTILLATION_REASON_OFF_SPEC,
				VolumeL:           100, AbvPct: 70,
				Notes: "Nose was wrong off the spirit safe.",
			}))
		if err != nil {
			t.Fatalf("StartRedistillation: %v", err)
		}
		r := got.Msg.GetRedistillation()
		if r.GetLaaTaken() < 69.99 || r.GetLaaTaken() > 70.01 {
			t.Errorf("laa_taken %v, want 70", r.GetLaaTaken())
		}
		if r.GetLaaProducedSet() || r.GetLossLaaSet() {
			t.Error("a redistillation that has not run yet reports an output or a loss — " +
				"the whole charge would look lost")
		}

		// The bulk ledger moved, and by the reportable reason.
		_, afterLAA := f.balance(t, tank.ID)
		if diff := beforeLAA - afterLAA; diff < 69.99 || diff > 70.01 {
			t.Errorf("the tank lost %v LAA, want 70", diff)
		}
		movements, err := f.q.ListBulkMovementsByContainer(f.ctx, uuid.NullUUID{UUID: tank.ID, Valid: true})
		if err != nil {
			t.Fatalf("list movements: %v", err)
		}
		var sawReturned bool
		for _, m := range movements {
			if m.Reason == sqlcgen.BulkMovementReasonReturnedToProduction {
				sawReturned = true
			}
		}
		if !sawReturned {
			t.Error("no returned-to-production movement was written; the B266 page 3 line " +
				"would not show the withdrawal")
		}
	})

	t.Run("more out than in is refused", func(t *testing.T) {
		start, err := svc.StartRedistillation(f.ctx, connect.NewRequest(
			&stillhousev1.StartRedistillationRequest{
				SourceContainerId: tank.ID.String(),
				Reason:            stillhousev1.RedistillationReason_REDISTILLATION_REASON_FEINTS_RECOVERY,
				VolumeL:           50, AbvPct: 60, // 30 LAA
			}))
		if err != nil {
			t.Fatalf("StartRedistillation: %v", err)
		}
		// A still does not create alcohol. The usual cause is a figure
		// typed in litres where LAA was wanted.
		_, err = svc.RecordRedistillationOutput(f.ctx, connect.NewRequest(
			&stillhousev1.RecordRedistillationOutputRequest{
				Id: start.Msg.GetRedistillation().GetId(), LaaProduced: 50,
			}))
		if err == nil {
			t.Fatal("50 LAA out of a 30 LAA charge was accepted")
		}
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("got %v, want InvalidArgument", connect.CodeOf(err))
		}
	})

	t.Run("recording the output computes the loss and says so", func(t *testing.T) {
		start, err := svc.StartRedistillation(f.ctx, connect.NewRequest(
			&stillhousev1.StartRedistillationRequest{
				SourceContainerId: tank.ID.String(),
				Reason:            stillhousev1.RedistillationReason_REDISTILLATION_REASON_REPROCESSING,
				VolumeL:           200, AbvPct: 70, // 140 LAA
			}))
		if err != nil {
			t.Fatalf("StartRedistillation: %v", err)
		}
		id := start.Msg.GetRedistillation().GetId()

		got, err := svc.RecordRedistillationOutput(f.ctx, connect.NewRequest(
			&stillhousev1.RecordRedistillationOutputRequest{Id: id, LaaProduced: 132}))
		if err != nil {
			t.Fatalf("RecordRedistillationOutput: %v", err)
		}
		if l := got.Msg.GetLossLaa(); l < 7.99 || l > 8.01 {
			t.Errorf("loss %v, want 8 — 140 in, 132 out", l)
		}
		// The point of the whole item: the loss is stated, so it can be
		// ruled relieved or duty-payable rather than being a number that
		// got smaller.
		if !got.Msg.GetNeedsLossClassification() {
			t.Error("an 8 LAA loss did not ask to be classified")
		}

		// And it closes once. A second recording would double-count the
		// output against one charge.
		if _, err := svc.RecordRedistillationOutput(f.ctx, connect.NewRequest(
			&stillhousev1.RecordRedistillationOutputRequest{Id: id, LaaProduced: 130},
		)); err == nil {
			t.Error("the output was recorded twice against one charge")
		}
	})

	t.Run("a run with no loss does not ask to be classified", func(t *testing.T) {
		start, err := svc.StartRedistillation(f.ctx, connect.NewRequest(
			&stillhousev1.StartRedistillationRequest{
				SourceContainerId: tank.ID.String(),
				Reason:            stillhousev1.RedistillationReason_REDISTILLATION_REASON_REPROCESSING,
				VolumeL:           10, AbvPct: 70, // 7 LAA
			}))
		if err != nil {
			t.Fatalf("StartRedistillation: %v", err)
		}
		got, err := svc.RecordRedistillationOutput(f.ctx, connect.NewRequest(
			&stillhousev1.RecordRedistillationOutputRequest{
				Id: start.Msg.GetRedistillation().GetId(), LaaProduced: 7,
			}))
		if err != nil {
			t.Fatalf("RecordRedistillationOutput: %v", err)
		}
		if got.Msg.GetNeedsLossClassification() {
			t.Error("a lossless run asked to be classified — an unremarkable outcome " +
				"should not generate work")
		}
	})

	t.Run("the period summary carries how much is still open", func(t *testing.T) {
		today := time.Now().UTC().Format("2006-01-02")
		sum, err := svc.RedistillationSummary(f.ctx, connect.NewRequest(
			&stillhousev1.RedistillationSummaryRequest{PeriodStart: today, PeriodEnd: today}))
		if err != nil {
			t.Fatalf("RedistillationSummary: %v", err)
		}
		// Two of the five started above never had an output recorded, so
		// their charge is in laa_taken and not in laa_produced. The loss
		// figure overstates until they close, which is exactly why the
		// open count travels with it.
		if sum.Msg.GetStillOpen() < 1 {
			t.Error("the summary reports nothing open, but two charges have no output")
		}
		if sum.Msg.GetLaaTaken() <= sum.Msg.GetLaaProduced() {
			t.Errorf("taken %v is not more than produced %v, but charges are still open",
				sum.Msg.GetLaaTaken(), sum.Msg.GetLaaProduced())
		}
	})
}

// Spirit that went into the still and never came back on the books is
// the one shape of gap a period-end reconciliation cannot explain.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestOpenRedistillationRaisesAnAlert(t *testing.T) {
	f := newLedgerFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	svc := NewRedistillationService(f.db, log)
	runner := alerting.NewRunner(f.db, f.q, nil, "http://example.test", time.Hour, log)
	alerts := NewAlertService(f.db, runner, log)
	now := time.Now().UTC()

	tank := f.tank(t, "Open redistill "+uuid.NewString()[:8], 500, 65)
	start, err := svc.StartRedistillation(f.ctx, connect.NewRequest(
		&stillhousev1.StartRedistillationRequest{
			SourceContainerId: tank.ID.String(),
			Reason:            stillhousev1.RedistillationReason_REDISTILLATION_REASON_OFF_SPEC,
			VolumeL:           100, AbvPct: 65,
			// Ten days ago: past the week that covers a long run and a
			// weekend.
			TakenOn: now.AddDate(0, 0, -10).Format("2006-01-02"),
		}))
	if err != nil {
		t.Fatalf("StartRedistillation: %v", err)
	}
	id := start.Msg.GetRedistillation().GetId()

	tenant, err := f.q.GetTenantByID(f.ctx, f.tenant.ID)
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	find := func(t *testing.T) *stillhousev1.Alert {
		t.Helper()
		if err := runner.RunTenant(f.ctx, tenant, now); err != nil {
			t.Fatalf("RunTenant: %v", err)
		}
		resp, err := alerts.ListAlerts(f.ctx, connect.NewRequest(&stillhousev1.ListAlertsRequest{}))
		if err != nil {
			t.Fatalf("ListAlerts: %v", err)
		}
		for _, a := range resp.Msg.GetAlerts() {
			if a.GetEntityId() == id {
				return a
			}
		}
		return nil
	}

	a := find(t)
	if a == nil {
		t.Fatal("spirit in the still for ten days with no output raised no alert")
	}
	if a.GetKind() != stillhousev1.AlertKind_ALERT_KIND_REDISTILLATION_OPEN {
		t.Errorf("kind %v, want redistillation open", a.GetKind())
	}

	// Recording the output closes it, without anyone dismissing anything.
	if _, err := svc.RecordRedistillationOutput(f.ctx, connect.NewRequest(
		&stillhousev1.RecordRedistillationOutputRequest{Id: id, LaaProduced: 60},
	)); err != nil {
		t.Fatalf("RecordRedistillationOutput: %v", err)
	}
	if a := find(t); a != nil {
		t.Error("the alert stayed open after the output was recorded")
	}
}
