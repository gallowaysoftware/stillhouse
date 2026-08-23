package rpc

import (
	"log/slog"
	"os"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The release gate. Not a CRA requirement — the Excise Act does not care
// whether your methanol came back — but the control every food safety
// programme assumes exists, and the difference between a recall you can
// bound and one you cannot.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestBatchReleaseGatesRemoval(t *testing.T) {
	f := newDutyFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	lab := NewLabService(f.db, log)
	tenantSvc := NewTenantService(f.pool, f.q, log)
	tenantSvc.db = f.db

	tank := f.tank(t, "release-src", 500, 60)
	product := f.product(t, "Release Vodka "+uuid.NewString()[:8], 750, 40)
	f.stamps(t, "CA-ON", 1000)
	run, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		SourceContainerId: tank.ID.String(), ProductId: product.ID.String(),
		DestinationJurisdiction: "CA-ON", BottleCount: 300,
		LotCode: "REL-" + uuid.NewString()[:8],
	}))
	if err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}
	lotID := run.Msg.GetPackaged().GetId()

	remove := func(t *testing.T, n int32) error {
		t.Helper()
		_, err := f.removal.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
			PackagedInventoryId: lotID, BottlesRemoved: n,
			DestinationKind: stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER,
			DestinationName: "Test buyer",
		}))
		return err
	}

	t.Run("with the gate off, an unreleased lot still ships", func(t *testing.T) {
		// The default, and deliberately so: a one-person distillery that
		// signs off by looking at the bottle should not be blocked by a
		// workflow built for a QA department.
		if err := remove(t, 1); err != nil {
			t.Fatalf("removal was blocked with the gate off: %v", err)
		}
	})

	t.Run("with the gate on, it does not", func(t *testing.T) {
		if _, err := tenantSvc.SetBatchReleaseRequired(f.ctx, connect.NewRequest(
			&stillhousev1.SetBatchReleaseRequiredRequest{Required: true})); err != nil {
			t.Fatalf("SetBatchReleaseRequired: %v", err)
		}
		err := remove(t, 1)
		if err == nil {
			t.Fatal("an unreleased lot shipped with the gate on")
		}
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("got %v, want FailedPrecondition", connect.CodeOf(err))
		}
	})

	t.Run("a release needs to say what was checked", func(t *testing.T) {
		if _, err := lab.ReleaseLot(f.ctx, connect.NewRequest(&stillhousev1.ReleaseLotRequest{
			PackagedInventoryId: lotID,
		})); err == nil {
			t.Error("a lot was released with no note — \"approved\" is not a record of anything")
		}
	})

	t.Run("once released, it ships", func(t *testing.T) {
		if _, err := lab.ReleaseLot(f.ctx, connect.NewRequest(&stillhousev1.ReleaseLotRequest{
			PackagedInventoryId: lotID,
			Notes:               "Methanol 68 ppm, within spec. Sensory panel passed.",
		})); err != nil {
			t.Fatalf("ReleaseLot: %v", err)
		}
		if err := remove(t, 1); err != nil {
			t.Fatalf("a released lot was blocked: %v", err)
		}
	})

	t.Run("a hold blocks it again, and blocks regardless of the setting", func(t *testing.T) {
		if _, err := lab.HoldLot(f.ctx, connect.NewRequest(&stillhousev1.HoldLotRequest{
			PackagedInventoryId: lotID, Reason: "Customer complaint — off nose. Investigating.",
		})); err != nil {
			t.Fatalf("HoldLot: %v", err)
		}
		err := remove(t, 1)
		if err == nil {
			t.Fatal("a held lot shipped")
		}
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("got %v, want FailedPrecondition", connect.CodeOf(err))
		}
		// Turning the requirement off must not release a held lot.
		// Holding is an explicit act by a named person; honouring it only
		// when a flag happens to be on would make the act meaningless.
		if _, err := tenantSvc.SetBatchReleaseRequired(f.ctx, connect.NewRequest(
			&stillhousev1.SetBatchReleaseRequiredRequest{Required: false})); err != nil {
			t.Fatalf("SetBatchReleaseRequired: %v", err)
		}
		if err := remove(t, 1); err == nil {
			t.Fatal("turning the release requirement off released a held lot")
		}
	})

	t.Run("re-releasing clears the hold", func(t *testing.T) {
		if _, err := lab.ReleaseLot(f.ctx, connect.NewRequest(&stillhousev1.ReleaseLotRequest{
			PackagedInventoryId: lotID, Notes: "Re-tested after complaint; within spec.",
		})); err != nil {
			t.Fatalf("ReleaseLot: %v", err)
		}
		if err := remove(t, 1); err != nil {
			t.Fatalf("a re-released lot was still blocked: %v", err)
		}
	})
}

// A lab result belongs to exactly one thing, and a judgement needs
// something to have judged.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestLabResultsAttachToOneSubject(t *testing.T) {
	f := newDutyFixture(t)
	lab := NewLabService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tank := f.tank(t, "lab-tank", 100, 65)

	t.Run("attached to nothing is refused", func(t *testing.T) {
		if _, err := lab.RecordLabResult(f.ctx, connect.NewRequest(
			&stillhousev1.RecordLabResultRequest{Analyte: "Methanol"})); err == nil {
			t.Error("a result attached to nothing was accepted")
		}
	})

	t.Run("attached to two things is refused", func(t *testing.T) {
		if _, err := lab.RecordLabResult(f.ctx, connect.NewRequest(
			&stillhousev1.RecordLabResultRequest{
				Analyte: "Methanol", ContainerId: tank.ID.String(),
				MashRunId: uuid.NewString(),
			})); err == nil {
			t.Error("a result attached to two subjects was accepted")
		}
	})

	t.Run("a pass with no value is refused", func(t *testing.T) {
		// A pass or a fail is a judgement against something. Recorded
		// with no number it is an opinion, and an opinion in a lab
		// register is worse than an informational reading.
		if _, err := lab.RecordLabResult(f.ctx, connect.NewRequest(
			&stillhousev1.RecordLabResultRequest{
				Analyte: "Methanol", ContainerId: tank.ID.String(),
				Status: stillhousev1.LabResultStatus_LAB_RESULT_STATUS_PASS,
			})); err == nil {
			t.Error("a pass with no value was accepted")
		}
	})

	t.Run("a real result round-trips and is findable from its subject", func(t *testing.T) {
		if _, err := lab.RecordLabResult(f.ctx, connect.NewRequest(
			&stillhousev1.RecordLabResultRequest{
				Analyte: "Methanol", ContainerId: tank.ID.String(),
				Value: 68, ValueSet: true, Uom: "ppm",
				SpecMax: 100, SpecMaxSet: true,
				Status:     stillhousev1.LabResultStatus_LAB_RESULT_STATUS_PASS,
				Laboratory: "Provincial Food Lab", Reference: "PFL-2026-4471",
				Method: "GC-FID", SampledOn: "2026-08-01", ReportedOn: "2026-08-04",
			})); err != nil {
			t.Fatalf("RecordLabResult: %v", err)
		}
		got, err := lab.ListLabResults(f.ctx, connect.NewRequest(
			&stillhousev1.ListLabResultsRequest{ContainerId: tank.ID.String()}))
		if err != nil {
			t.Fatalf("ListLabResults: %v", err)
		}
		if len(got.Msg.GetResults()) != 1 {
			t.Fatalf("got %d results for the tank, want 1", len(got.Msg.GetResults()))
		}
		r := got.Msg.GetResults()[0]
		if r.GetAnalyte() != "Methanol" || r.GetValue() != 68 || r.GetUom() != "ppm" {
			t.Errorf("result did not round-trip: %v", r)
		}
		if !r.GetSpecMaxSet() || r.GetSpecMax() != 100 {
			t.Error("the limit it was judged against did not survive")
		}
		if r.GetReference() != "PFL-2026-4471" {
			t.Error("the lab's own reference did not survive — the certificate is unfindable")
		}
	})
}
