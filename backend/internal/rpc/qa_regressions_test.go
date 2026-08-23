package rpc

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// Findings from the stage 181 QA pass, each asserted where it broke.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

// QA-1: a name collision answered `internal error` with a 500. The
// constraint knew what was wrong; the handler threw it away.
func TestDuplicateContainerNameNamesTheCollision(t *testing.T) {
	f := newLedgerFixture(t)
	bulk := NewBulkService(f.db, testLogger())
	name := "Tank " + uuid.NewString()[:8]

	mk := func() error {
		_, err := bulk.CreateBulkContainer(f.ctx, connect.NewRequest(
			&stillhousev1.CreateBulkContainerRequest{
				Name: name, Kind: stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_TANK,
			}))
		return err
	}
	if err := mk(); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err := mk()
	if err == nil {
		t.Fatal("two containers were created with the same name")
	}
	if got := connect.CodeOf(err); got != connect.CodeAlreadyExists {
		t.Errorf("code = %v, want already_exists — an operator cannot act on an "+
			"internal error", got)
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("the message should say what collided, got: %v", err)
	}
}

// QA-2: a cask both owned by a customer and held elsewhere landed in
// neither figure, so the totals read zero above a row that plainly was
// not. Three figures now partition everything that is not ours-and-here.
func TestOwnershipFiguresAccountForEveryRow(t *testing.T) {
	f := newLedgerFixture(t)
	bulk := NewBulkService(f.db, testLogger())
	cust := f.salesCustomer(t, sqlcgen.RemovalDestinationKindDutyPaidCustomer)

	theirsHere := f.tank(t, "TH "+uuid.NewString()[:6], 100, 50) // 50 LAA
	oursAway := f.tank(t, "OA "+uuid.NewString()[:6], 200, 50)   // 100 LAA
	theirsAway := f.tank(t, "TA "+uuid.NewString()[:6], 400, 50) // 200 LAA
	_ = f.tank(t, "OH "+uuid.NewString()[:6], 800, 50)           // 400 LAA, ours and here

	for _, id := range []uuid.UUID{theirsHere.ID, theirsAway.ID} {
		if _, err := bulk.SetBulkContainerOwner(f.ctx, connect.NewRequest(
			&stillhousev1.SetBulkContainerOwnerRequest{
				Id: id.String(), OwnerCustomerId: cust.ID.String(),
			})); err != nil {
			t.Fatalf("SetBulkContainerOwner: %v", err)
		}
	}
	for _, id := range []uuid.UUID{oursAway.ID, theirsAway.ID} {
		if _, err := bulk.SetBulkContainerPossession(f.ctx, connect.NewRequest(
			&stillhousev1.SetBulkContainerPossessionRequest{
				Id: id.String(), HeldByName: "Partner Distillery Ltd",
				Possession: stillhousev1.BulkPossession_BULK_POSSESSION_HELD_ELSEWHERE,
			})); err != nil {
			t.Fatalf("SetBulkContainerPossession: %v", err)
		}
	}

	res, err := bulk.ListBulkContainers(f.ctx, connect.NewRequest(
		&stillhousev1.ListBulkContainersRequest{}))
	if err != nil {
		t.Fatalf("ListBulkContainers: %v", err)
	}
	s := res.Msg.GetSummary()
	for _, tc := range []struct {
		name string
		got  float64
		want float64
	}{
		{"held for others", s.GetHeldForOthersLaa(), 50},
		{"held elsewhere", s.GetHeldElsewhereLaa(), 100},
		{"third party elsewhere", s.GetThirdPartyElsewhereLaa(), 200},
		{"available", s.GetAvailableLaa(), 400},
	} {
		if !near(tc.got, tc.want, 1e-6) {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	// The three must add up to everything that is not ours-and-here, or
	// the screen has a row it cannot account for.
	notSimple := s.GetHeldForOthersLaa() + s.GetHeldElsewhereLaa() + s.GetThirdPartyElsewhereLaa()
	if want := s.GetTotalLaa() - s.GetAvailableLaa(); !near(notSimple, want, 1e-6) {
		t.Errorf("the three figures total %v but %v is not ours-and-here — "+
			"something is in none of them", notSimple, want)
	}

	third, err := bulk.ListThirdPartySpirits(f.ctx, connect.NewRequest(
		&stillhousev1.ListThirdPartySpiritsRequest{}))
	if err != nil {
		t.Fatalf("ListThirdPartySpirits: %v", err)
	}
	listed := 0.0
	for _, c := range third.Msg.GetContainers() {
		listed += c.GetCurrentLaa()
	}
	summed := third.Msg.GetHeldForOthersLaa() +
		third.Msg.GetHeldElsewhereLaa() + third.Msg.GetThirdPartyElsewhereLaa()
	if !near(listed, summed, 1e-6) {
		t.Errorf("the list holds %v LAA but its figures total %v", listed, summed)
	}
}

// QA-5: a run with no material chain behind it reported a complete cost,
// because "no lines" satisfied "no unpriced lines". Zero traced materials
// is not zero materials, the same distinction the labour side already
// made about hours.
func TestCostWithNoMaterialsIsNotComplete(t *testing.T) {
	f := newLedgerFixture(t)
	costing := NewCostingService(f.db, testLogger())
	bottling := NewBottlingService(f.db, testLogger())

	// Adopted stock: no mash, no distillation, nothing to price.
	tank := f.tank(t, "Adopted "+uuid.NewString()[:6], 1000, 60)
	product, err := f.q.CreateProduct(f.ctx, sqlcgen.CreateProductParams{
		TenantID: f.tenant.ID, Name: "NoChain " + uuid.NewString()[:8],
		SpiritKind: sqlcgen.SpiritKindVodka, BottleSizeMl: 750, TargetAbvPct: 40,
	})
	if err != nil {
		t.Fatalf("product: %v", err)
	}
	f.seedStamps(t, "CA-ON", 1000)
	run, err := bottling.CreateBottlingRun(f.ctx, connect.NewRequest(
		&stillhousev1.CreateBottlingRunRequest{
			ProductId: product.ID.String(), SourceContainerId: tank.ID.String(),
			DestinationJurisdiction: "CA-ON", BottleCount: 400,
			LotCode: "NC-" + uuid.NewString()[:8], BottlingDate: "2026-08-01",
		}))
	if err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}
	runID := run.Msg.GetRun().GetId()

	// Rates and hours, so labour and overhead are both available and the
	// only thing missing is the materials.
	if _, err := costing.SaveCostRates(f.ctx, connect.NewRequest(
		&stillhousev1.SaveCostRatesRequest{
			EffectiveFrom: "2026-01-01", LabourRateCadPerHour: "30.00",
			OverheadBasis: stillhousev1.OverheadBasis_OVERHEAD_BASIS_PER_LABOUR_HOUR,
			OverheadRate:  "10.00",
		})); err != nil {
		t.Fatalf("SaveCostRates: %v", err)
	}
	if _, err := costing.RecordLabour(f.ctx, connect.NewRequest(
		&stillhousev1.RecordLabourRequest{
			Subject: &stillhousev1.LabourSubject{BottlingRunId: runID},
			Hours:   4, WorkedOn: "2026-08-01",
		})); err != nil {
		t.Fatalf("RecordLabour: %v", err)
	}

	got, err := costing.BottlingRunFullCost(f.ctx, connect.NewRequest(
		&stillhousev1.BottlingRunFullCostRequest{BottlingRunId: runID}))
	if err != nil {
		t.Fatalf("BottlingRunFullCost: %v", err)
	}
	c := got.Msg
	if c.GetMaterials().GetAvailable() {
		t.Error("a run with nothing priceable behind it reported materials as available")
	}
	if c.GetMaterials().GetMissing() == "" {
		t.Error("an unavailable component must say why")
	}
	if strings.Contains(c.GetMaterials().GetBasis(), "0 priced") {
		t.Errorf("basis claims a count it does not have: %q", c.GetMaterials().GetBasis())
	}
	if c.GetComplete() {
		t.Error("a cost containing no materials at all called itself complete — " +
			"this is the failure the whole component model exists to prevent")
	}
	// Labour and overhead still land: a partial cost is worth showing.
	if !c.GetLabour().GetAvailable() || !c.GetOverhead().GetAvailable() {
		t.Error("labour and overhead should still be available")
	}
	if got, want := c.GetTotalCad(), 160.0; !near(got, want, 1e-6) {
		t.Errorf("total = %v, want 4 h × ($30 + $10) = %v", got, want)
	}
}

// QA-4: a helper that only knows the "es" ending, applied to words that
// do not take it.
func TestPluralEndings(t *testing.T) {
	for _, tc := range []struct {
		word string
		got  string
		want string
	}{
		{"loss", "loss" + plural(2), "losses"},
		{"loss", "loss" + plural(1), "loss"},
		{"destruction", "destruction" + pluralS(2), "destructions"},
		{"destruction", "destruction" + pluralS(1), "destruction"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s → %q, want %q", tc.word, tc.got, tc.want)
		}
	}
}

// QA-6: the field was set nowhere, and a build is happy either way. A
// report over every jurisdiction is unreadable without it.
func TestProvincialLinesNameTheirJurisdiction(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewProvincialService(f.db, testLogger())
	removals := NewRemovalService(f.db, testLogger())

	cust, err := f.q.CreateCustomer(f.ctx, sqlcgen.CreateCustomerParams{
		TenantID: f.tenant.ID, Name: "Board " + uuid.NewString()[:6],
		Kind: sqlcgen.CustomerKindProvincialBoard, Jurisdiction: "CA-BC",
		DefaultDestinationKind: string(sqlcgen.RemovalDestinationKindDutyPaidCustomer),
	})
	if err != nil {
		t.Fatalf("customer: %v", err)
	}
	_, lot := f.salesStock(t, 750, 40, 50)
	if _, err := removals.CreateRemoval(f.ctx, connect.NewRequest(
		&stillhousev1.CreateRemovalRequest{
			PackagedInventoryId: lot.ID.String(), BottlesRemoved: 12,
			CustomerId: cust.ID.String(), RemovalDate: "2026-08-05",
		})); err != nil {
		t.Fatalf("CreateRemoval: %v", err)
	}
	got, err := svc.ProvincialSalesReport(f.ctx, connect.NewRequest(
		&stillhousev1.ProvincialSalesReportRequest{
			PeriodStart: "2026-08-01", PeriodEnd: "2026-08-31",
		}))
	if err != nil {
		t.Fatalf("ProvincialSalesReport: %v", err)
	}
	if len(got.Msg.GetLines()) == 0 {
		t.Fatal("no lines")
	}
	for _, l := range got.Msg.GetLines() {
		if l.GetJurisdiction() == "" {
			t.Errorf("%s has no jurisdiction — the all-provinces view cannot be read",
				l.GetProductName())
		}
	}
}
