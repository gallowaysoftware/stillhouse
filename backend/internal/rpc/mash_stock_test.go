package rpc

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

func (b *floorBatch) add(mat, lot string, qty float64, uom string) error {
	_, err := b.mash.AddMashIngredient(b.ctx, connect.NewRequest(&stillhousev1.AddMashIngredientRequest{
		MashRunId: b.mashID, MaterialId: mat, MaterialLotId: lot, QuantityUsed: qty, Uom: uom}))
	return err
}

func (b *floorBatch) material(t *testing.T, name, uom string) string {
	t.Helper()
	m, err := b.mat.CreateMaterial(b.ctx, connect.NewRequest(&stillhousev1.CreateMaterialRequest{
		Name: name, Kind: stillhousev1.MaterialKind_MATERIAL_KIND_MALT, Uom: uom}))
	must(t, err)
	return m.Msg.GetMaterial().GetId()
}

// The number is the debit, so it has to be in the unit the lot is counted in.
func TestAnIngredientIsInItsOwnUnit(t *testing.T) {
	const invalid = connect.CodeInvalidArgument
	b := newFloorBatch(t)
	mats, err := b.mat.ListMaterials(b.ctx, connect.NewRequest(&stillhousev1.ListMaterialsRequest{}))
	must(t, err)
	malt := mats.Msg.GetMaterials()[0].GetId()
	refusedWith(t, "grams off a lot in kilograms", b.add(malt, b.lotID, 5, "g"), invalid)
	refusedWith(t, "grams with no lot", b.add(malt, "", 5, "g"), invalid)
	refusedWith(t, "another material's lot", b.add(b.material(t, "Rye", "kg"), b.lotID, 5, "kg"), invalid)
	if got := b.onHand(t); got != 19 {
		t.Errorf("a refused entry moved stock: %v on hand, want 19", got)
	}
	must(t, b.add(malt, b.lotID, 1, " KG "))
	m, err := b.mash.GetMashRun(b.ctx, connect.NewRequest(&stillhousev1.GetMashRunRequest{Id: b.mashID}))
	must(t, err)
	if got := m.Msg.GetMashRun().GetBench().GetTotalGrainKg(); got != 7 {
		t.Errorf("the bench counts %v kg, want 7", got)
	}
	for _, ing := range m.Msg.GetMashRun().GetIngredients() {
		if ing.GetUom() != "kg" {
			t.Errorf("stored as %q, want the material's own spelling", ing.GetUom())
		}
	}
}

// A material counted in pounds is mashed as the mass it is.
func TestABenchReadsMassNotNumbers(t *testing.T) {
	b := newFloorBatch(t)
	must(t, b.add(b.material(t, "Corn", "lb"), "", 100, "lb"))
	m, err := b.mash.GetMashRun(b.ctx, connect.NewRequest(&stillhousev1.GetMashRunRequest{Id: b.mashID}))
	must(t, err)
	if got := m.Msg.GetMashRun().GetBench().GetTotalGrainKg(); !near(got, 51.3592, 1e-3) {
		t.Errorf("6 kg and 100 lb make %v kg, want 51.3592", got)
	}
}

func TestALotCanBeUsedUpInParts(t *testing.T) {
	b := newFloorBatch(t)
	mats, err := b.mat.ListMaterials(b.ctx, connect.NewRequest(&stillhousev1.ListMaterialsRequest{}))
	must(t, err)
	malt := mats.Msg.GetMaterials()[0].GetId()
	lot, err := b.mat.RecordMaterialReceipt(b.ctx, connect.NewRequest(&stillhousev1.RecordMaterialReceiptRequest{
		MaterialId: malt, SupplierLot: "L2", QuantityReceived: 25}))
	must(t, err)
	for _, q := range []float64{8.3, 8.3, 8.4} {
		must(t, b.add(malt, lot.Msg.GetLot().GetId(), q, "kg"))
	}
	left, err := b.mat.ListMaterialLots(b.ctx, connect.NewRequest(&stillhousev1.ListMaterialLotsRequest{MaterialId: malt, OnHandOnly: true}))
	must(t, err)
	for _, l := range left.Msg.GetLots() {
		if l.GetSupplierLot() == "L2" {
			t.Errorf("%v of a used-up lot still shows on hand", l.GetQuantityOnHand())
		}
	}
	refusedWith(t, "more than is there", b.add(malt, lot.Msg.GetLot().GetId(), 0.001, "kg"), connect.CodeFailedPrecondition)
}

func TestCapturedLAACountsEachGaugeOnce(t *testing.T) {
	b := newFloorBatch(t)
	tank := b.tank(t, "T", 0, 0)
	captured := func() float64 {
		m, err := b.mash.GetMashRun(b.ctx, connect.NewRequest(&stillhousev1.GetMashRunRequest{Id: b.mashID}))
		must(t, err)
		return m.Msg.GetMashRun().GetActualCapturedLaa()
	}
	run := func(ferms ...string) string {
		r, err := b.dist.CreateDistillationRun(b.ctx, connect.NewRequest(&stillhousev1.CreateDistillationRunRequest{StillLabel: "S"}))
		must(t, err)
		for i, f := range ferms {
			_, err = b.dist.AddDistillationCharge(b.ctx, connect.NewRequest(&stillhousev1.AddDistillationChargeRequest{
				DistillationRunId: r.Msg.GetRun().GetId(), FermentationRunId: f, VolumeChargedL: 50, AbvPct: 8, ChargeOrder: int32(i + 1)}))
			must(t, err)
		}
		return r.Msg.GetRun().GetId()
	}
	gauge := func(id string, vol float64) {
		_, err := b.dist.RecordProductionGauge(b.ctx, connect.NewRequest(&stillhousev1.RecordProductionGaugeRequest{
			DistillationRunId: id, DestinationContainerId: tank.ID.String(), VolumeL: vol, AbvPct: 60, GaugeDate: timestamppb.New(time.Now())}))
		must(t, err)
	}
	second, err := b.ferm.CreateFermentationRun(b.ctx, connect.NewRequest(&stillhousev1.CreateFermentationRunRequest{MashRunId: b.mashID, FermenterLabel: "FV2"}))
	must(t, err)

	gauge(run(b.fermID), 5)
	gauge(run(b.fermID, second.Msg.GetRun().GetId()), 5)
	if got := captured(); !near(got, 6, 1e-9) {
		t.Errorf("two gauges of 3 LAA, one fed by two fermenters of this mash: %v, want 6", got)
	}
	third := run(b.fermID)
	gauge(third, 4)
	_, err = b.dist.VoidDistillationRun(b.ctx, connect.NewRequest(&stillhousev1.VoidDistillationRunRequest{Id: third, Reason: "x"}))
	must(t, err)
	if got := captured(); !near(got, 6, 1e-9) {
		t.Errorf("a voided gauge counts: %v, want 6", got)
	}

	// Half of the next run's wash is another mash's.
	var version string
	must(t, b.pool.QueryRow(b.ctx, "SELECT recipe_version_id FROM mash_runs WHERE id = $1", b.mashID).Scan(&version))
	other, err := b.mash.CreateMashRun(b.ctx, connect.NewRequest(&stillhousev1.CreateMashRunRequest{RecipeVersionId: version}))
	must(t, err)
	otherFerm, err := b.ferm.CreateFermentationRun(b.ctx, connect.NewRequest(&stillhousev1.CreateFermentationRunRequest{
		MashRunId: other.Msg.GetMashRun().GetId(), FermenterLabel: "FV3"}))
	must(t, err)
	gauge(run(b.fermID, otherFerm.Msg.GetRun().GetId()), 10)
	if got := captured(); !near(got, 9, 1e-9) {
		t.Errorf("half of a 6 LAA gauge belongs to another mash: %v, want 9", got)
	}
}

func TestARecipeRefusesWhatItCannotProject(t *testing.T) {
	const invalid = connect.CodeInvalidArgument
	b := newFloorBatch(t)
	svc := NewRecipeService(b.db, testLogger())
	recipe, err := svc.CreateRecipe(b.ctx, connect.NewRequest(&stillhousev1.CreateRecipeRequest{
		Name: "Q", SpiritKind: stillhousev1.SpiritKind_SPIRIT_KIND_WHISKY}))
	must(t, err)
	malt := b.material(t, "Rye", "kg")
	save := func(ings ...*stillhousev1.RecipeIngredientInput) error {
		_, err := svc.SaveRecipeVersion(b.ctx, connect.NewRequest(&stillhousev1.SaveRecipeVersionRequest{
			RecipeId: recipe.Msg.GetRecipe().GetId(), Ingredients: ings,
			MashEfficiencyFraction: .8, FermentEfficiencyFraction: .9, DistillationRecoveryFraction: .85}))
		return err
	}
	line := func(qty float64, uom string) *stillhousev1.RecipeIngredientInput {
		return &stillhousev1.RecipeIngredientInput{MaterialId: malt, Quantity: qty, Uom: uom}
	}
	refusedWith(t, "grain in litres", save(line(20, "L")), invalid)
	refusedWith(t, "grain twice", save(line(20, "kg"), line(5, "kg")), invalid)
	refusedWith(t, "none of it", save(line(0, "kg")), invalid)
	refusedWith(t, "a material that is not there", save(&stillhousev1.RecipeIngredientInput{
		MaterialId: "0b0f3f0a-0000-4000-8000-000000000001", Quantity: 1, Uom: "kg"}), connect.CodeNotFound)
	must(t, save(line(44, "lb")))
	_, err = svc.CreateRecipe(b.ctx, connect.NewRequest(&stillhousev1.CreateRecipeRequest{
		Name: "Q", SpiritKind: stillhousev1.SpiritKind_SPIRIT_KIND_WHISKY}))
	refusedWith(t, "a second recipe of that name", err, connect.CodeAlreadyExists)
}

// Zero is how a strength nobody knew is stored.
func TestAMashWithAnUnknownStrengthIsStillSharedIn(t *testing.T) {
	b := newFloorBatch(t)
	tank := b.tank(t, "T", 0, 0)
	var version string
	must(t, b.pool.QueryRow(b.ctx, "SELECT recipe_version_id FROM mash_runs WHERE id = $1", b.mashID).Scan(&version))
	other, err := b.mash.CreateMashRun(b.ctx, connect.NewRequest(&stillhousev1.CreateMashRunRequest{RecipeVersionId: version}))
	must(t, err)
	ferm, err := b.ferm.CreateFermentationRun(b.ctx, connect.NewRequest(&stillhousev1.CreateFermentationRunRequest{
		MashRunId: other.Msg.GetMashRun().GetId(), FermenterLabel: "FV9"}))
	must(t, err)
	run, err := b.dist.CreateDistillationRun(b.ctx, connect.NewRequest(&stillhousev1.CreateDistillationRunRequest{StillLabel: "S"}))
	must(t, err)
	for ferment, c := range map[string][2]float64{b.fermID: {300, 0}, ferm.Msg.GetRun().GetId(): {100, 8}} {
		_, err = b.dist.AddDistillationCharge(b.ctx, connect.NewRequest(&stillhousev1.AddDistillationChargeRequest{
			DistillationRunId: run.Msg.GetRun().GetId(), FermentationRunId: ferment, VolumeChargedL: c[0], AbvPct: c[1]}))
		must(t, err)
	}
	_, err = b.dist.RecordProductionGauge(b.ctx, connect.NewRequest(&stillhousev1.RecordProductionGaugeRequest{
		DistillationRunId: run.Msg.GetRun().GetId(), DestinationContainerId: tank.ID.String(), VolumeL: 20, AbvPct: 60}))
	must(t, err)
	m, err := b.mash.GetMashRun(b.ctx, connect.NewRequest(&stillhousev1.GetMashRunRequest{Id: b.mashID}))
	must(t, err)
	if got := m.Msg.GetMashRun().GetActualCapturedLaa(); !near(got, 9, 1e-9) {
		t.Errorf("300 of 400 L is %v LAA of 12, want 9", got)
	}
}

func TestACancelledFermentationIsNotCharged(t *testing.T) {
	b := newFloorBatch(t)
	_, err := b.dist.VoidDistillationRun(b.ctx, connect.NewRequest(&stillhousev1.VoidDistillationRunRequest{Id: b.runID, Reason: "x"}))
	must(t, err)
	_, err = b.ferm.UpdateFermentationStatus(b.ctx, connect.NewRequest(&stillhousev1.UpdateFermentationStatusRequest{
		Id: b.fermID, Status: stillhousev1.FermentationStatus_FERMENTATION_STATUS_CANCELLED}))
	must(t, err)
	run, err := b.dist.CreateDistillationRun(b.ctx, connect.NewRequest(&stillhousev1.CreateDistillationRunRequest{StillLabel: "S"}))
	must(t, err)
	_, err = b.dist.AddDistillationCharge(b.ctx, connect.NewRequest(&stillhousev1.AddDistillationChargeRequest{
		DistillationRunId: run.Msg.GetRun().GetId(), FermentationRunId: b.fermID, VolumeChargedL: 5, AbvPct: 8}))
	refusedWith(t, "charging it", err, connect.CodeFailedPrecondition)
}

func TestAChargedFermentationIsNotCancelledUnderneathIt(t *testing.T) {
	b := newFloorBatch(t)
	_, err := b.ferm.UpdateFermentationStatus(b.ctx, connect.NewRequest(&stillhousev1.UpdateFermentationStatusRequest{
		Id: b.fermID, Status: stillhousev1.FermentationStatus_FERMENTATION_STATUS_CANCELLED}))
	refusedWith(t, "cancelling", err, connect.CodeFailedPrecondition)
	_, err = b.dist.VoidDistillationRun(b.ctx, connect.NewRequest(&stillhousev1.VoidDistillationRunRequest{Id: b.runID, Reason: "x"}))
	must(t, err)
	_, err = b.dist.DeleteDistillationCharge(b.ctx, connect.NewRequest(&stillhousev1.DeleteDistillationChargeRequest{Id: b.chargeID}))
	refusedWith(t, "deleting a voided run's charge", err, connect.CodeFailedPrecondition)
	_, err = b.ferm.UpdateFermentationStatus(b.ctx, connect.NewRequest(&stillhousev1.UpdateFermentationStatusRequest{
		Id: b.fermID, Status: stillhousev1.FermentationStatus_FERMENTATION_STATUS_CANCELLED}))
	must(t, err)
}
