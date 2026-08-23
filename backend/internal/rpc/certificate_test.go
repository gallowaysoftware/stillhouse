package rpc

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The one failure mode that matters: a certificate that quietly rounded
// up over a gap in the record.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestAgeCertificateRefusesToClaimOverAGap(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewCertificateService(f.db, testLogger())
	bottling := NewBottlingService(f.db, testLogger())
	f.seedStamps(t, "CA-ON", 2000)

	tank := f.tank(t, "Vat "+uuid.NewString()[:6], 2000, 60)
	product, err := f.q.CreateProduct(f.ctx, sqlcgen.CreateProductParams{
		TenantID: f.tenant.ID, Name: "Export whisky " + uuid.NewString()[:8],
		SpiritKind: sqlcgen.SpiritKindWhisky, BottleSizeMl: 750, TargetAbvPct: 40,
	})
	if err != nil {
		t.Fatalf("product: %v", err)
	}
	run, err := bottling.CreateBottlingRun(f.ctx, connect.NewRequest(
		&stillhousev1.CreateBottlingRunRequest{
			ProductId: product.ID.String(), SourceContainerId: tank.ID.String(),
			DestinationJurisdiction: "CA-ON", BottleCount: 600,
			LotCode: "EXP-" + uuid.NewString()[:8], BottlingDate: "2026-08-01",
		}))
	if err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}
	runID := run.Msg.GetRun().GetId()

	t.Run("a tank never filled from wood carries no age", func(t *testing.T) {
		got, err := svc.AgeCertificate(f.ctx, connect.NewRequest(
			&stillhousev1.AgeCertificateRequest{
				BottlingRunId: runID, Consignee: "A importer",
				DestinationCountry: "US",
			}))
		if err != nil {
			t.Fatalf("AgeCertificate: %v", err)
		}
		c := got.Msg
		if c.GetAgeSupportable() {
			t.Error("an age was claimed for spirits with no cask behind them")
		}
		if len(c.GetCaveats()) == 0 {
			t.Error("a certificate with no evidence should say so")
		}
		var said bool
		for _, cv := range c.GetCaveats() {
			if strings.Contains(cv, "no age evidence") {
				said = true
			}
		}
		if !said {
			t.Errorf("the caveats do not name the problem: %v", c.GetCaveats())
		}
	})

	t.Run("it names the producer and the lot", func(t *testing.T) {
		got, err := svc.AgeCertificate(f.ctx, connect.NewRequest(
			&stillhousev1.AgeCertificateRequest{BottlingRunId: runID}))
		if err != nil {
			t.Fatalf("AgeCertificate: %v", err)
		}
		c := got.Msg
		if c.GetProducerName() != f.tenant.Name {
			t.Errorf("producer = %q, want %q", c.GetProducerName(), f.tenant.Name)
		}
		if c.GetProducerLicenceNo() == "" {
			t.Error("a certificate has to name the licence number")
		}
		if c.GetProductName() != product.Name {
			t.Errorf("product = %q", c.GetProductName())
		}
		if c.GetBottleCount() != 600 {
			t.Errorf("bottles = %d", c.GetBottleCount())
		}
		if c.GetBasis() == "" {
			t.Error("it must say what it is and is not attesting")
		}
	})

	t.Run("Stillhouse does not claim to certify", func(t *testing.T) {
		got, err := svc.AgeCertificate(f.ctx, connect.NewRequest(
			&stillhousev1.AgeCertificateRequest{BottlingRunId: runID}))
		if err != nil {
			t.Fatalf("AgeCertificate: %v", err)
		}
		// A certificate of age and origin is signed by a Canadian
		// official. This is the evidence behind one, and saying so is
		// the difference between a packet and a forgery.
		if !strings.Contains(got.Msg.GetBasis(), "does not certify") {
			t.Errorf("the basis should disclaim certifying: %q", got.Msg.GetBasis())
		}
	})

	t.Run("a voided run has nothing to certify", func(t *testing.T) {
		if _, err := bottling.VoidBottlingRun(f.ctx, connect.NewRequest(
			&stillhousev1.VoidBottlingRunRequest{Id: runID, Reason: "mis-keyed"},
		)); err != nil {
			t.Fatalf("VoidBottlingRun: %v", err)
		}
		if _, err := svc.AgeCertificate(f.ctx, connect.NewRequest(
			&stillhousev1.AgeCertificateRequest{BottlingRunId: runID})); err == nil {
			t.Error("a voided run produced a certificate packet")
		}
	})
}
