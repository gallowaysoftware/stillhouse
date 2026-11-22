package rpc

import (
	"log/slog"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// floorBatch is a mash with 6 kg of a 25 kg lot in it, fermenting, charged
// to a still run that has not been gauged.
type floorBatch struct {
	*ledgerFixture
	mash *MashService
	ferm *FermentationService
	dist *DistillationService
	mat  *MaterialService

	lotID, mashID, ingredientID, fermID, runID, chargeID string
}

func newFloorBatch(t *testing.T) *floorBatch {
	t.Helper()
	f := newLedgerFixture(t)
	log := slog.New(slog.DiscardHandler)
	b := &floorBatch{ledgerFixture: f,
		mash: NewMashService(f.db, log), ferm: NewFermentationService(f.db, log),
		dist: NewDistillationService(f.db, log), mat: NewMaterialService(f.db, log)}
	recipes := NewRecipeService(f.db, log)

	m, err := b.mat.CreateMaterial(f.ctx, connect.NewRequest(&stillhousev1.CreateMaterialRequest{
		Name: "Malt", Kind: stillhousev1.MaterialKind_MATERIAL_KIND_MALT, Uom: "kg"}))
	must(t, err)
	matID := m.Msg.GetMaterial().GetId()
	lot, err := b.mat.RecordMaterialReceipt(f.ctx, connect.NewRequest(&stillhousev1.RecordMaterialReceiptRequest{
		MaterialId: matID, SupplierLot: "L1", QuantityReceived: 25}))
	must(t, err)
	b.lotID = lot.Msg.GetLot().GetId()
	r, err := recipes.CreateRecipe(f.ctx, connect.NewRequest(&stillhousev1.CreateRecipeRequest{
		Name: "R", SpiritKind: stillhousev1.SpiritKind_SPIRIT_KIND_WHISKY}))
	must(t, err)
	v, err := recipes.SaveRecipeVersion(f.ctx, connect.NewRequest(&stillhousev1.SaveRecipeVersionRequest{
		RecipeId: r.Msg.GetRecipe().GetId(), MashEfficiencyFraction: 0.8, FermentEfficiencyFraction: 0.9, DistillationRecoveryFraction: 0.9,
		Ingredients: []*stillhousev1.RecipeIngredientInput{{MaterialId: matID, Quantity: 6, Uom: "kg"}}}))
	must(t, err)
	mr, err := b.mash.CreateMashRun(f.ctx, connect.NewRequest(&stillhousev1.CreateMashRunRequest{RecipeVersionId: v.Msg.GetVersion().GetId()}))
	must(t, err)
	b.mashID = mr.Msg.GetMashRun().GetId()
	ing, err := b.mash.AddMashIngredient(f.ctx, connect.NewRequest(&stillhousev1.AddMashIngredientRequest{
		MashRunId: b.mashID, MaterialId: matID, MaterialLotId: b.lotID, QuantityUsed: 6, Uom: "kg"}))
	must(t, err)
	b.ingredientID = ing.Msg.GetUsage().GetId()
	fr, err := b.ferm.CreateFermentationRun(f.ctx, connect.NewRequest(&stillhousev1.CreateFermentationRunRequest{MashRunId: b.mashID, FermenterLabel: "FV1"}))
	must(t, err)
	b.fermID = fr.Msg.GetRun().GetId()
	dr, err := b.dist.CreateDistillationRun(f.ctx, connect.NewRequest(&stillhousev1.CreateDistillationRunRequest{StillLabel: "S"}))
	must(t, err)
	b.runID = dr.Msg.GetRun().GetId()
	ch, err := b.dist.AddDistillationCharge(f.ctx, connect.NewRequest(&stillhousev1.AddDistillationChargeRequest{
		DistillationRunId: b.runID, FermentationRunId: b.fermID, VolumeChargedL: 20, AbvPct: 8}))
	must(t, err)
	b.chargeID = ch.Msg.GetCharge().GetId()
	return b
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (b *floorBatch) onHand(t *testing.T) float64 {
	t.Helper()
	lots, err := b.mat.ListMaterialLots(b.ctx, connect.NewRequest(&stillhousev1.ListMaterialLotsRequest{}))
	must(t, err)
	return lots.Msg.GetLots()[0].GetQuantityOnHand()
}

func TestDeleteMashIngredientPutsItBackOnceOnly(t *testing.T) {
	b := newFloorBatch(t)
	if got := b.onHand(t); got != 19 {
		t.Fatalf("on hand after mashing: %v, want 19", got)
	}
	del := connect.NewRequest(&stillhousev1.DeleteMashIngredientRequest{Id: b.ingredientID})
	_, err := b.mash.DeleteMashIngredient(b.ctx, del)
	must(t, err)
	if got := b.onHand(t); got != 25 {
		t.Errorf("on hand after removal: %v, want 25", got)
	}
	if _, err := b.mash.DeleteMashIngredient(b.ctx, del); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("second removal: %v, want not found", err)
	}
	if got := b.onHand(t); got != 25 {
		t.Errorf("on hand after removing twice: %v, want 25", got)
	}
}

func TestCorrectionsStopAtTheGauge(t *testing.T) {
	b := newFloorBatch(t)
	tank := b.tank(t, "Receiver", 0, 0)
	_, err := b.dist.RecordProductionGauge(b.ctx, connect.NewRequest(&stillhousev1.RecordProductionGaugeRequest{
		DistillationRunId: b.runID, DestinationContainerId: tank.ID.String(), VolumeL: 2, AbvPct: 60}))
	must(t, err)
	if _, err := b.mash.DeleteMashIngredient(b.ctx, connect.NewRequest(&stillhousev1.DeleteMashIngredientRequest{Id: b.ingredientID})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("ingredient of a gauged mash: %v, want failed precondition", err)
	}
	if _, err := b.dist.DeleteDistillationCharge(b.ctx, connect.NewRequest(&stillhousev1.DeleteDistillationChargeRequest{Id: b.chargeID})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("charge of a gauged run: %v, want failed precondition", err)
	}
	if got := b.onHand(t); got != 19 {
		t.Errorf("on hand moved on a refused removal: %v, want 19", got)
	}
}

func TestCorrectionsDeleteReadingsAndCharges(t *testing.T) {
	b := newFloorBatch(t)
	m, err := b.mash.AddMashMetric(b.ctx, connect.NewRequest(&stillhousev1.AddMashMetricRequest{
		MashRunId: b.mashID, Kind: stillhousev1.MashMetricKind_MASH_METRIC_KIND_WASH_VOLUME_L, Value: 21}))
	must(t, err)
	_, err = b.mash.DeleteMashMetric(b.ctx, connect.NewRequest(&stillhousev1.DeleteMashMetricRequest{Id: m.Msg.GetMetric().GetId()}))
	must(t, err)
	l, err := b.ferm.AddFermentationLog(b.ctx, connect.NewRequest(&stillhousev1.AddFermentationLogRequest{
		FermentationRunId: b.fermID, SpecificGravity: 1.05, SpecificGravitySet: true}))
	must(t, err)
	_, err = b.ferm.DeleteFermentationLog(b.ctx, connect.NewRequest(&stillhousev1.DeleteFermentationLogRequest{Id: l.Msg.GetLog().GetId()}))
	must(t, err)
	_, err = b.dist.DeleteDistillationCharge(b.ctx, connect.NewRequest(&stillhousev1.DeleteDistillationChargeRequest{Id: b.chargeID}))
	must(t, err)
}

// Another tenant's entry is not found rather than deleted.
func TestCorrectionsStayInsideTheTenant(t *testing.T) {
	theirs, ours := newFloorBatch(t), newFloorBatch(t)
	if _, err := ours.mash.DeleteMashIngredient(ours.ctx, connect.NewRequest(&stillhousev1.DeleteMashIngredientRequest{Id: theirs.ingredientID})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("another tenant's ingredient: %v, want not found", err)
	}
	if got := theirs.onHand(t); got != 19 {
		t.Errorf("another tenant's lot moved to %v", got)
	}
}

// Every kind the API defines must be recordable: wash volume was on the
// page and refused for as long as this mapping had no case for it.
func TestEveryMashMetricKindRoundTrips(t *testing.T) {
	for n, name := range stillhousev1.MashMetricKind_name {
		k := stillhousev1.MashMetricKind(n)
		if n == 0 {
			continue
		}
		db, err := mashMetricKindToDB(k)
		if err != nil {
			t.Errorf("%s: %v", name, err)
		} else if back := mashMetricKindToProto(db); back != k {
			t.Errorf("%s came back as %s", name, back)
		}
	}
}

func TestBadIdsAreNotFoundRatherThanInternal(t *testing.T) {
	b := newFloorBatch(t)
	none := uuid.NewString()
	mats, err := b.mat.ListMaterials(b.ctx, connect.NewRequest(&stillhousev1.ListMaterialsRequest{}))
	must(t, err)
	if _, err := b.mash.AddMashIngredient(b.ctx, connect.NewRequest(&stillhousev1.AddMashIngredientRequest{
		MashRunId: none, MaterialId: mats.Msg.GetMaterials()[0].GetId(), QuantityUsed: 1, Uom: "kg"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("ingredient on no mash: %v", err)
	}
	if _, err := b.ferm.CreateFermentationRun(b.ctx, connect.NewRequest(&stillhousev1.CreateFermentationRunRequest{
		MashRunId: b.mashID, FermenterLabel: "X", YeastLotId: none})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("no such yeast lot: %v", err)
	}
}
