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

// A licence lapses because nobody was told a date was coming, and the
// consequence is not a warning letter — it is that the licensee is no
// longer licensed, with every movement after that date to explain.
//
// The rule's most important property is what it does NOT do: a licence
// with no recorded expiry raises nothing. Every CRA licence expires, so
// a missing date means nobody entered it, and a reminder derived from a
// guessed date would be believed.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestLicenceRenewalAlerts(t *testing.T) {
	f := newLedgerFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	// NewTenantService derives its tenantdb from the pool it is given, and
	// the fixture's pool is the superuser one used for seeding. Point it
	// at the RLS-enforcing handle instead, so this test exercises the
	// tenant boundary like every other DB-backed test (stage 153).
	tenantSvc := NewTenantService(f.pool, f.q, log)
	tenantSvc.db = f.db
	runner := alerting.NewRunner(f.db, f.q, nil, "http://example.test", time.Hour, log)
	alerts := NewAlertService(f.db, runner, log)
	now := time.Now().UTC().Truncate(24 * time.Hour)

	tenant, err := f.q.GetTenantByID(f.ctx, f.tenant.ID)
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}

	save := func(t *testing.T, number string, expires time.Time, hasExpiry bool) string {
		t.Helper()
		req := &stillhousev1.SaveExciseLicenceRequest{
			Kind:          stillhousev1.ExciseLicenceKind_EXCISE_LICENCE_KIND_SPIRITS,
			LicenceNumber: number,
			EffectiveFrom: now.AddDate(-2, 0, 0).Format("2006-01-02"),
		}
		if hasExpiry {
			req.ExpiresOn = expires.Format("2006-01-02")
		}
		resp, err := tenantSvc.SaveExciseLicence(f.ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatalf("SaveExciseLicence(%s): %v", number, err)
		}
		return resp.Msg.GetLicence().GetId()
	}

	// Keyed by the licence, not by kind: several licences raise the same
	// kind at once, and collapsing them would make the assertions depend
	// on which one happened to come back last.
	alertFor := func(t *testing.T, licenceID string) *stillhousev1.Alert {
		t.Helper()
		if err := runner.RunTenant(f.ctx, tenant, now); err != nil {
			t.Fatalf("RunTenant: %v", err)
		}
		resp, err := alerts.ListAlerts(f.ctx, connect.NewRequest(&stillhousev1.ListAlertsRequest{}))
		if err != nil {
			t.Fatalf("ListAlerts: %v", err)
		}
		for _, a := range resp.Msg.GetAlerts() {
			if a.GetEntityId() == licenceID {
				return a
			}
		}
		return nil
	}

	t.Run("a licence with no expiry recorded raises nothing", func(t *testing.T) {
		id := save(t, "NOEXPIRY-"+uuid.NewString()[:6], time.Time{}, false)
		if a := alertFor(t, id); a != nil {
			t.Errorf("a licence with no recorded expiry produced %v", a.GetKind())
		}
		// And the register says it is incomplete rather than looking done.
		list, err := tenantSvc.ListExciseLicences(f.ctx, connect.NewRequest(
			&stillhousev1.ListExciseLicencesRequest{}))
		if err != nil {
			t.Fatalf("ListExciseLicences: %v", err)
		}
		if list.Msg.GetMissingExpiryCount() < 1 {
			t.Error("the register does not report the licence with no expiry date")
		}
	})

	t.Run("inside the renewal window it warns", func(t *testing.T) {
		id := save(t, "SOON-"+uuid.NewString()[:6], now.AddDate(0, 0, 45), true)
		a := alertFor(t, id)
		if a == nil {
			t.Fatal("a licence expiring in 45 days raised no alert")
		}
		if a.GetKind() != stillhousev1.AlertKind_ALERT_KIND_LICENCE_EXPIRING {
			t.Errorf("kind %v, want licence expiring", a.GetKind())
		}
		if a.GetSeverity() != stillhousev1.AlertSeverity_ALERT_SEVERITY_WARNING {
			t.Errorf("severity %v at 45 days, want warning", a.GetSeverity())
		}
	})

	t.Run("inside CRA's 30 days it is critical", func(t *testing.T) {
		id := save(t, "URGENT-"+uuid.NewString()[:6], now.AddDate(0, 0, 20), true)
		a := alertFor(t, id)
		if a == nil {
			t.Fatal("no expiring alert")
		}
		if a.GetSeverity() != stillhousev1.AlertSeverity_ALERT_SEVERITY_CRITICAL {
			t.Errorf("severity %v inside 30 days, want critical — CRA wants the "+
				"renewal by then, so it is no longer a heads-up", a.GetSeverity())
		}
	})

	t.Run("an expired licence is its own, louder thing", func(t *testing.T) {
		id := save(t, "LAPSED-"+uuid.NewString()[:6], now.AddDate(0, 0, -10), true)
		a := alertFor(t, id)
		if a == nil {
			t.Fatal("an expired licence raised no alert")
		}
		if a.GetKind() != stillhousev1.AlertKind_ALERT_KIND_LICENCE_EXPIRED {
			t.Errorf("kind %v, want licence expired", a.GetKind())
		}
		if a.GetSeverity() != stillhousev1.AlertSeverity_ALERT_SEVERITY_CRITICAL {
			t.Errorf("severity %v for an expired licence, want critical", a.GetSeverity())
		}
	})

	t.Run("ceasing a licence stops its alert without deleting it", func(t *testing.T) {
		id := save(t, "CEASED-"+uuid.NewString()[:6], now.AddDate(0, 0, 5), true)
		if alertFor(t, id) == nil {
			t.Fatal("setup: the licence about to be ceased raised no alert")
		}
		if _, err := tenantSvc.SaveExciseLicence(f.ctx, connect.NewRequest(
			&stillhousev1.SaveExciseLicenceRequest{
				Id:            id,
				Kind:          stillhousev1.ExciseLicenceKind_EXCISE_LICENCE_KIND_SPIRITS,
				LicenceNumber: "CEASED",
				EffectiveFrom: now.AddDate(-2, 0, 0).Format("2006-01-02"),
				ExpiresOn:     now.AddDate(0, 0, 5).Format("2006-01-02"),
				CeasedOn:      now.Format("2006-01-02"),
			})); err != nil {
			t.Fatalf("cease licence: %v", err)
		}
		// Still in the register — a return filed under it has to stay
		// explicable — but no longer something to renew.
		list, err := tenantSvc.ListExciseLicences(f.ctx, connect.NewRequest(
			&stillhousev1.ListExciseLicencesRequest{}))
		if err != nil {
			t.Fatalf("ListExciseLicences: %v", err)
		}
		var stillListed bool
		for _, l := range list.Msg.GetLicences() {
			if l.GetId() == id {
				stillListed = true
				if l.GetCeasedOn() == "" {
					t.Error("the ceased licence does not report a ceased date")
				}
			}
		}
		if !stillListed {
			t.Error("ceasing a licence removed it from the register")
		}
	})
}
