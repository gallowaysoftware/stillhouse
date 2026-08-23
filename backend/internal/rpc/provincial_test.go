package rpc

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The framework, and the discipline it exists to hold: Stillhouse does
// not ship other people's deadlines, so a requirement carries where it
// came from, and one with nothing behind it can never go overdue.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestProvincialReportingFramework(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewProvincialService(f.db, testLogger())

	reg, err := svc.SaveProvincialRegistration(f.ctx, connect.NewRequest(
		&stillhousev1.SaveProvincialRegistrationRequest{
			Jurisdiction: "ca-on", BoardName: "LCBO", RegistrationNo: "SUP-4021",
		}))
	if err != nil {
		t.Fatalf("SaveProvincialRegistration: %v", err)
	}
	if got, want := reg.Msg.GetRegistration().GetJurisdiction(), "CA-ON"; got != want {
		t.Errorf("jurisdiction = %q, want %q — codes are normalised", got, want)
	}
	regID := reg.Msg.GetRegistration().GetId()

	t.Run("a jurisdiction that is not a jurisdiction is refused", func(t *testing.T) {
		if _, err := svc.SaveProvincialRegistration(f.ctx, connect.NewRequest(
			&stillhousev1.SaveProvincialRegistrationRequest{Jurisdiction: "Ontario"})); err == nil {
			t.Error("a free-text province was accepted as a code")
		}
	})

	t.Run("claiming a source without citing one is refused", func(t *testing.T) {
		// Otherwise the provenance flag is a self-assessment nobody can
		// check, which is worse than admitting the requirement is hearsay.
		if _, err := svc.SaveProvincialReportDefinition(f.ctx, connect.NewRequest(
			&stillhousev1.SaveProvincialReportDefinitionRequest{
				RegistrationId: regID, Name: "Monthly sales",
				Cadence:    stillhousev1.ReportingCadence_REPORTING_CADENCE_MONTHLY,
				Provenance: stillhousev1.RequirementProvenance_REQUIREMENT_PROVENANCE_SOURCED,
			})); err == nil {
			t.Error("a requirement claimed to be sourced with no source")
		}
	})

	def, err := svc.SaveProvincialReportDefinition(f.ctx, connect.NewRequest(
		&stillhousev1.SaveProvincialReportDefinitionRequest{
			RegistrationId:        regID,
			Name:                  "Monthly sales report",
			Cadence:               stillhousev1.ReportingCadence_REPORTING_CADENCE_MONTHLY,
			DueDaysAfterPeriodEnd: 20,
			Provenance:            stillhousev1.RequirementProvenance_REQUIREMENT_PROVENANCE_INDICATIVE,
			Notes:                 "confirm with the board",
		}))
	if err != nil {
		t.Fatalf("SaveProvincialReportDefinition: %v", err)
	}
	defID := def.Msg.GetDefinition().GetId()

	t.Run("periods are generated, and generating twice does not duplicate", func(t *testing.T) {
		first, err := svc.GenerateProvincialPeriods(f.ctx, connect.NewRequest(
			&stillhousev1.GenerateProvincialPeriodsRequest{
				DefinitionId: defID, From: "2026-01-01", To: "2026-03-31",
			}))
		if err != nil {
			t.Fatalf("GenerateProvincialPeriods: %v", err)
		}
		if len(first.Msg.GetPeriods()) != 3 {
			t.Fatalf("got %d periods for Jan–Mar, want 3", len(first.Msg.GetPeriods()))
		}
		p := first.Msg.GetPeriods()[0]
		if p.GetPeriodStart() != "2026-01-01" || p.GetPeriodEnd() != "2026-01-31" {
			t.Errorf("first period = %s..%s", p.GetPeriodStart(), p.GetPeriodEnd())
		}
		if p.GetDueOn() != "2026-02-20" {
			t.Errorf("due on = %s, want 2026-02-20", p.GetDueOn())
		}

		if _, err := svc.GenerateProvincialPeriods(f.ctx, connect.NewRequest(
			&stillhousev1.GenerateProvincialPeriodsRequest{
				DefinitionId: defID, From: "2026-01-01", To: "2026-03-31",
			})); err != nil {
			t.Fatalf("second generate: %v", err)
		}
		all, err := svc.ListProvincialReportPeriods(f.ctx, connect.NewRequest(
			&stillhousev1.ListProvincialReportPeriodsRequest{}))
		if err != nil {
			t.Fatalf("ListProvincialReportPeriods: %v", err)
		}
		n := 0
		for _, r := range all.Msg.GetPeriods() {
			if r.GetDefinitionId() == defID {
				n++
			}
		}
		if n != 3 {
			t.Errorf("%d periods after generating twice, want 3", n)
		}
	})

	t.Run("filing needs something to show for it", func(t *testing.T) {
		all, err := svc.ListProvincialReportPeriods(f.ctx, connect.NewRequest(
			&stillhousev1.ListProvincialReportPeriodsRequest{UnfiledOnly: true}))
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(all.Msg.GetPeriods()) == 0 {
			t.Fatal("no unfiled periods")
		}
		id := all.Msg.GetPeriods()[0].GetId()

		if _, err := svc.MarkProvincialReportFiled(f.ctx, connect.NewRequest(
			&stillhousev1.MarkProvincialReportFiledRequest{Id: id})); err == nil {
			t.Error("a period was marked filed with nothing to show for it")
		}
		if _, err := svc.MarkProvincialReportFiled(f.ctx, connect.NewRequest(
			&stillhousev1.MarkProvincialReportFiledRequest{
				Id: id, Acknowledgement: "portal confirmation 88213",
			})); err != nil {
			t.Fatalf("MarkProvincialReportFiled: %v", err)
		}
		if _, err := svc.MarkProvincialReportFiled(f.ctx, connect.NewRequest(
			&stillhousev1.MarkProvincialReportFiledRequest{
				Id: id, Acknowledgement: "again",
			})); err == nil {
			t.Error("a filed period was filed twice")
		}
	})

	t.Run("a definition with no recorded due date never goes overdue", func(t *testing.T) {
		// Raising on it would mean inventing the deadline, which is the
		// thing this whole track refuses to do.
		bare, err := svc.SaveProvincialReportDefinition(f.ctx, connect.NewRequest(
			&stillhousev1.SaveProvincialReportDefinitionRequest{
				RegistrationId:        regID,
				Name:                  "Something they want, cadence unknown",
				Cadence:               stillhousev1.ReportingCadence_REPORTING_CADENCE_QUARTERLY,
				DueDaysAfterPeriodEnd: -1,
			}))
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		got, err := svc.GenerateProvincialPeriods(f.ctx, connect.NewRequest(
			&stillhousev1.GenerateProvincialPeriodsRequest{
				DefinitionId: bare.Msg.GetDefinition().GetId(),
				From:         "2020-01-01", To: "2020-12-31",
			}))
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if len(got.Msg.GetPeriods()) == 0 {
			t.Fatal("no periods generated")
		}
		for _, p := range got.Msg.GetPeriods() {
			if p.GetDueOn() != "" {
				t.Errorf("period %s got a due date of %s from nowhere",
					p.GetPeriodEnd(), p.GetDueOn())
			}
			if p.GetOverdue() {
				t.Errorf("a period from 2020 with no due date was called overdue")
			}
		}
	})

	t.Run("a cadence with no period boundaries says so", func(t *testing.T) {
		per, err := svc.SaveProvincialReportDefinition(f.ctx, connect.NewRequest(
			&stillhousev1.SaveProvincialReportDefinitionRequest{
				RegistrationId: regID, Name: "Form with every delivery",
				Cadence: stillhousev1.ReportingCadence_REPORTING_CADENCE_PER_SHIPMENT,
			}))
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		_, err = svc.GenerateProvincialPeriods(f.ctx, connect.NewRequest(
			&stillhousev1.GenerateProvincialPeriodsRequest{
				DefinitionId: per.Msg.GetDefinition().GetId(),
				From:         "2026-01-01", To: "2026-12-31",
			}))
		if err == nil {
			t.Fatal("periods were generated for a per-shipment report")
		}
		if !strings.Contains(err.Error(), "per-shipment") {
			t.Errorf("the refusal should say why, got: %v", err)
		}
	})
}

// The figures a provincial report is built from, and the thing that makes
// them right: the jurisdiction is the buyer's, not the stamps'.
func TestProvincialSalesFollowsTheBuyerNotTheStamps(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewProvincialService(f.db, testLogger())
	removals := NewRemovalService(f.db, testLogger())

	// A case stamped for Ontario, sold to an Alberta buyer. Reporting by
	// stamp would credit Ontario with a shipment that went to Alberta.
	alberta, err := f.q.CreateCustomer(f.ctx, sqlcgen.CreateCustomerParams{
		TenantID: f.tenant.ID, Name: "AGLC " + uuid.NewString()[:6],
		Kind: sqlcgen.CustomerKindProvincialBoard, Jurisdiction: "CA-AB",
		DefaultDestinationKind: string(sqlcgen.RemovalDestinationKindDutyPaidCustomer),
	})
	if err != nil {
		t.Fatalf("customer: %v", err)
	}
	_, lot := f.salesStock(t, 750, 40, 100)

	if _, err := removals.CreateRemoval(f.ctx, connect.NewRequest(
		&stillhousev1.CreateRemovalRequest{
			PackagedInventoryId: lot.ID.String(), BottlesRemoved: 60,
			CustomerId: alberta.ID.String(), RemovalDate: "2026-08-05",
		})); err != nil {
		t.Fatalf("CreateRemoval: %v", err)
	}

	report := func(t *testing.T, jur string) *stillhousev1.ProvincialSalesReportResponse {
		t.Helper()
		got, err := svc.ProvincialSalesReport(f.ctx, connect.NewRequest(
			&stillhousev1.ProvincialSalesReportRequest{
				Jurisdiction: jur, PeriodStart: "2026-08-01", PeriodEnd: "2026-08-31",
			}))
		if err != nil {
			t.Fatalf("ProvincialSalesReport(%s): %v", jur, err)
		}
		return got.Msg
	}

	ab := report(t, "CA-AB")
	if got, want := ab.GetTotalBottles(), int32(60); got != want {
		t.Errorf("Alberta bottles = %d, want %d", got, want)
	}
	if got, want := ab.GetTotalLaa(), 18.0; !near(got, want, 1e-6) {
		t.Errorf("Alberta LAA = %v, want %v", got, want)
	}

	on := report(t, "CA-ON")
	if on.GetTotalBottles() != 0 {
		t.Errorf("Ontario was credited with %d bottles that went to Alberta — the "+
			"report is following the stamps rather than the buyer",
			on.GetTotalBottles())
	}
	if ab.GetBasis() == "" {
		t.Error("a report with no stated basis is a number nobody can check")
	}

	t.Run("removals with no customer are reported, not dropped", func(t *testing.T) {
		if _, err := removals.CreateRemoval(f.ctx, connect.NewRequest(
			&stillhousev1.CreateRemovalRequest{
				PackagedInventoryId: lot.ID.String(), BottlesRemoved: 10,
				DestinationKind: stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_SAMPLE,
				DestinationName: "trade sample", RemovalDate: "2026-08-06",
			})); err != nil {
			t.Fatalf("CreateRemoval: %v", err)
		}
		r := report(t, "CA-AB")
		if r.GetUnattributedBottles() != 10 {
			t.Errorf("unattributed bottles = %d, want 10 — a report that silently "+
				"omits them understates the province it is for",
				r.GetUnattributedBottles())
		}
		if r.GetTotalBottles() != 60 {
			t.Errorf("Alberta total moved to %d; an unattributed removal must not "+
				"be credited to a board", r.GetTotalBottles())
		}
	})
}
