package rpc

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/alerting"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// An alert is a condition with a life cycle, not a message. This is that
// claim under test: it opens when the condition becomes true, updates
// rather than duplicating while it stays true, and resolves itself when
// it stops — without anybody clicking anything.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestAlertLifeCycle(t *testing.T) {
	f := newLedgerFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	runner := alerting.NewRunner(f.db, f.q, nil, "http://example.test", time.Hour, log)
	svc := NewAlertService(f.db, runner, log)
	now := time.Now().UTC()

	// A fermentation that stopped reporting. Pitched three days ago,
	// still marked fermenting, no logs.
	mash := alertTestMash(t, f)
	ferment, err := f.q.CreateFermentationRun(f.ctx, sqlcgen.CreateFermentationRunParams{
		TenantID:       f.tenant.ID,
		MashRunID:      mash.ID,
		FermenterLabel: "Alert Fermenter",
		PitchAt:        pgtype.Timestamptz{Valid: true, Time: now.Add(-72 * time.Hour)},
		Status:         sqlcgen.FermentationStatusActive,
	})
	if err != nil {
		t.Fatalf("create fermentation: %v", err)
	}

	tenant, err := f.q.GetTenantByID(f.ctx, f.tenant.ID)
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}

	t.Run("opens when the condition becomes true", func(t *testing.T) {
		if err := runner.RunTenant(f.ctx, tenant, now); err != nil {
			t.Fatalf("RunTenant: %v", err)
		}
		a := findAlert(t, svc, f, stillhousev1.AlertKind_ALERT_KIND_FERMENTATION_STALLED)
		if a == nil {
			t.Fatal("a fermentation with no reading in 72 hours raised no alert")
		}
		if a.GetEntityId() != ferment.ID.String() {
			t.Errorf("alert points at %q, want the fermentation %q", a.GetEntityId(), ferment.ID)
		}
		if a.GetSeverity() != stillhousev1.AlertSeverity_ALERT_SEVERITY_WARNING {
			t.Errorf("severity %v, want warning", a.GetSeverity())
		}
	})

	t.Run("re-evaluating updates rather than duplicating", func(t *testing.T) {
		first := findAlert(t, svc, f, stillhousev1.AlertKind_ALERT_KIND_FERMENTATION_STALLED)
		firstOpened := first.GetOpenedAt().AsTime()

		for i := 0; i < 3; i++ {
			if err := runner.RunTenant(f.ctx, tenant, now); err != nil {
				t.Fatalf("RunTenant: %v", err)
			}
		}
		resp, err := svc.ListAlerts(f.ctx, connect.NewRequest(&stillhousev1.ListAlertsRequest{}))
		if err != nil {
			t.Fatalf("ListAlerts: %v", err)
		}
		var n int
		for _, a := range resp.Msg.GetAlerts() {
			if a.GetKind() == stillhousev1.AlertKind_ALERT_KIND_FERMENTATION_STALLED {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("four evaluations produced %d open alerts, want 1", n)
		}
		again := findAlert(t, svc, f, stillhousev1.AlertKind_ALERT_KIND_FERMENTATION_STALLED)
		// opened_at must not move: how long a thing has been true is the
		// most useful fact about it.
		if !again.GetOpenedAt().AsTime().Equal(firstOpened) {
			t.Errorf("opened_at moved from %v to %v on re-evaluation",
				firstOpened, again.GetOpenedAt().AsTime())
		}
		if !again.GetLastSeenAt().AsTime().After(firstOpened) {
			t.Error("last_seen_at did not advance across re-evaluations")
		}
	})

	t.Run("acknowledging does not resolve", func(t *testing.T) {
		a := findAlert(t, svc, f, stillhousev1.AlertKind_ALERT_KIND_FERMENTATION_STALLED)
		got, err := svc.AcknowledgeAlert(f.ctx, connect.NewRequest(
			&stillhousev1.AcknowledgeAlertRequest{Id: a.GetId()}))
		if err != nil {
			t.Fatalf("AcknowledgeAlert: %v", err)
		}
		if got.Msg.GetAlert().GetAcknowledgedAt() == nil {
			t.Error("acknowledging recorded no timestamp")
		}
		if got.Msg.GetAlert().GetResolvedAt() != nil {
			t.Error("acknowledging resolved the alert; the condition is still true")
		}
		// And it is still open on the next sweep.
		if err := runner.RunTenant(f.ctx, tenant, now.Add(10*time.Minute)); err != nil {
			t.Fatalf("RunTenant: %v", err)
		}
		if findAlert(t, svc, f, stillhousev1.AlertKind_ALERT_KIND_FERMENTATION_STALLED) == nil {
			t.Error("an acknowledged alert closed itself while its condition held")
		}
	})

	t.Run("resolves itself when the condition stops", func(t *testing.T) {
		// The operator does the thing: records a reading.
		if _, err := f.q.AddFermentationLog(f.ctx, sqlcgen.AddFermentationLogParams{
			TenantID:          f.tenant.ID,
			FermentationRunID: ferment.ID,
			ObservedAt:        pgtype.Timestamptz{Valid: true, Time: now},
			TemperatureC:      pgtype.Float8{Float64: 30, Valid: true},
		}); err != nil {
			t.Fatalf("add fermentation log: %v", err)
		}
		if err := runner.RunTenant(f.ctx, tenant, now.Add(20*time.Minute)); err != nil {
			t.Fatalf("RunTenant: %v", err)
		}
		if a := findAlert(t, svc, f, stillhousev1.AlertKind_ALERT_KIND_FERMENTATION_STALLED); a != nil {
			t.Error("the alert stayed open after its condition stopped being true")
		}
		// Resolved, not deleted: a ferment that stalled is worth being
		// able to see it stalled.
		resp, err := svc.ListAlerts(f.ctx, connect.NewRequest(
			&stillhousev1.ListAlertsRequest{IncludeResolved: true}))
		if err != nil {
			t.Fatalf("ListAlerts: %v", err)
		}
		var found bool
		for _, a := range resp.Msg.GetAlerts() {
			if a.GetKind() == stillhousev1.AlertKind_ALERT_KIND_FERMENTATION_STALLED &&
				a.GetResolvedAt() != nil {
				found = true
			}
		}
		if !found {
			t.Error("the resolved alert is not in the history")
		}
	})
}

func findAlert(t *testing.T, svc *AlertService, f *ledgerFixture, kind stillhousev1.AlertKind) *stillhousev1.Alert {
	t.Helper()
	resp, err := svc.ListAlerts(f.ctx, connect.NewRequest(&stillhousev1.ListAlertsRequest{}))
	if err != nil {
		t.Fatalf("ListAlerts: %v", err)
	}
	for _, a := range resp.Msg.GetAlerts() {
		if a.GetKind() == kind {
			return a
		}
	}
	return nil
}

// alertTestMash builds the recipe → version → mash chain a fermentation
// hangs off. The alert being tested is about the fermentation; this is
// scaffolding.
func alertTestMash(t *testing.T, f *ledgerFixture) sqlcgen.MashRun {
	t.Helper()
	recipe, err := f.q.CreateRecipe(f.ctx, sqlcgen.CreateRecipeParams{
		TenantID: f.tenant.ID, Name: "Alert Recipe " + uuid.NewString()[:8],
		SpiritKind: sqlcgen.SpiritKindVodka,
	})
	if err != nil {
		t.Fatalf("create recipe: %v", err)
	}
	rv, err := f.q.CreateRecipeVersion(f.ctx, sqlcgen.CreateRecipeVersionParams{
		TenantID: f.tenant.ID, RecipeID: recipe.ID, VersionNo: 1,
		MashEfficiencyPct: 0.85, FermentEfficiencyPct: 0.92, DistillationRecoveryPct: 0.9,
	})
	if err != nil {
		t.Fatalf("create recipe version: %v", err)
	}
	mash, err := f.q.CreateMashRun(f.ctx, sqlcgen.CreateMashRunParams{
		TenantID: f.tenant.ID, RecipeVersionID: rv.ID, MashNo: 9100,
		MashDate: pgtype.Date{Valid: true, Time: time.Now()},
		Status:   sqlcgen.MashStatusFermenting,
	})
	if err != nil {
		t.Fatalf("create mash: %v", err)
	}
	return mash
}
