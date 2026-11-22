package rpc

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

func TestAMaterialCanBeCorrected(t *testing.T) {
	b := newFloorBatch(t)
	unused := b.material(t, "Rye", "lb")
	update := func(id, name, uom string, extract float64) error {
		_, err := b.mat.UpdateMaterial(b.ctx, connect.NewRequest(&stillhousev1.UpdateMaterialRequest{
			Id: id, Name: name, Uom: uom, ExtractFraction: extract, ExtractFractionSet: true}))
		return err
	}
	must(t, update(unused, "Rye malt", "kg", 0.78))
	must(t, func() error {
		_, err := b.mat.RecordMaterialReceipt(b.ctx, connect.NewRequest(&stillhousev1.RecordMaterialReceiptRequest{
			MaterialId: unused, QuantityReceived: 25, SupplierLot: "R1", ReceivedAt: timestamppb.New(time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))}))
		return err
	}())
	refusedWith(t, "a unit that has stock counted in it", update(unused, "Rye malt", "g", 0.78), connect.CodeFailedPrecondition)
	must(t, update(unused, "Rye malt", "kg", 0.756))
	refusedWith(t, "another material's name", update(unused, "Malt", "kg", 0.756), connect.CodeAlreadyExists)
	_, err := b.mat.ArchiveMaterial(b.ctx, connect.NewRequest(&stillhousev1.ArchiveMaterialRequest{Id: unused, Archived: true}))
	must(t, err)

	var n int
	must(t, b.pool.QueryRow(b.ctx, "SELECT COUNT(*) FROM audit_events WHERE entity_type = 'material' AND entity_id = $1 AND action = 'update'", unused).Scan(&n))
	if n != 3 {
		t.Errorf("%d updates audited, want the two corrections and the archiving", n)
	}

	_, err = b.mat.CreateMaterial(b.ctx, connect.NewRequest(&stillhousev1.CreateMaterialRequest{
		Name: "  Malt ", Kind: stillhousev1.MaterialKind_MATERIAL_KIND_MALT, Uom: "kg"}))
	refusedWith(t, "a name that is taken", err, connect.CodeAlreadyExists)
	_, err = b.mat.RecordMaterialReceipt(b.ctx, connect.NewRequest(&stillhousev1.RecordMaterialReceiptRequest{
		MaterialId: unused, QuantityReceived: 25, UnitCostCad: -3, UnitCostCadSet: true}))
	refusedWith(t, "a negative cost", err, connect.CodeInvalidArgument)
}

func TestGrainIsCountedByMass(t *testing.T) {
	const invalid = connect.CodeInvalidArgument
	b := newFloorBatch(t)
	create := func(name, uom string, kind stillhousev1.MaterialKind) error {
		_, err := b.mat.CreateMaterial(b.ctx, connect.NewRequest(&stillhousev1.CreateMaterialRequest{Name: name, Kind: kind, Uom: uom}))
		return err
	}
	refusedWith(t, "malt by the bag", create("Bags", "bag", stillhousev1.MaterialKind_MATERIAL_KIND_MALT), invalid)
	must(t, create("Corks", "each", stillhousev1.MaterialKind_MATERIAL_KIND_PACKAGING))
	must(t, create("Enzyme", "mL", stillhousev1.MaterialKind_MATERIAL_KIND_OTHER))

	kgs := b.material(t, "Wheat", "kgs")
	must(t, b.add(kgs, "", 2, "kg"))
	_, err := b.mat.UpdateMaterial(b.ctx, connect.NewRequest(&stillhousev1.UpdateMaterialRequest{Id: kgs, Name: "Wheat", Uom: "kg"}))
	must(t, err)
	_, err = b.mat.UpdateMaterial(b.ctx, connect.NewRequest(&stillhousev1.UpdateMaterialRequest{Id: kgs, Name: "Wheat", Uom: "lb"}))
	refusedWith(t, "a different unit once used", err, connect.CodeFailedPrecondition)
}

// A delivery is dated by the day, and this morning's is today's.
func TestAReceiptDatedTodayIsNotInTheFuture(t *testing.T) {
	b := newFloorBatch(t)
	receive := func(at time.Time) error {
		_, err := b.mat.RecordMaterialReceipt(b.ctx, connect.NewRequest(&stillhousev1.RecordMaterialReceiptRequest{
			MaterialId: b.material(t, "M "+at.String(), "kg"), QuantityReceived: 1, ReceivedAt: timestamppb.New(at)}))
		return err
	}
	loc := b.db.Location(b.ctx, b.tenant.ID)
	y, m, d := time.Now().In(loc).Date()
	must(t, receive(time.Date(y, m, d, 23, 59, 0, 0, loc)))
	refusedWith(t, "tomorrow", receive(time.Date(y, m, d+1, 0, 1, 0, 0, loc)), connect.CodeInvalidArgument)
}
