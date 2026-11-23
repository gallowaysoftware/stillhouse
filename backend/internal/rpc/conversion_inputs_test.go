package rpc

import (
	"context"
	"math"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/units"
)

func TestKgOfIsTheUnitsTable(t *testing.T) {
	b := newFloorBatch(t)
	for uom, f := range units.MassUnits() {
		for _, spelt := range []string{uom, " " + uom + " ", "X" + uom[1:]} {
			if spelt[0] == 'X' {
				spelt = string(uom[0]-32) + uom[1:]
			}
			var got *float64
			must(t, b.pool.QueryRow(b.ctx, "SELECT kg_of(2, $1)", spelt).Scan(&got))
			if got == nil || math.Abs(*got-2*f) > 1e-12 {
				t.Errorf("kg_of(2, %q) = %v, want %v", spelt, got, 2*f)
			}
		}
	}
	var bag *float64
	must(t, b.pool.QueryRow(b.ctx, "SELECT kg_of(2, 'bag')").Scan(&bag))
	if bag != nil {
		t.Errorf("a bag is %v kg", *bag)
	}
}

// The benchmark's own-mash figure is the bench's: grain and malt in
// kilograms, all with extract, over a measured wash.
func TestConversionInputsAreTheBenchs(t *testing.T) {
	b := newFloorBatch(t)
	mash := uuid.MustParse(b.mashID)
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := b.pool.Exec(b.ctx, sql, args...)
		must(t, err)
	}
	inputs := func() []sqlcgen.MyConversionInputsRow {
		t.Helper()
		var rows []sqlcgen.MyConversionInputsRow
		must(t, b.db.WithTenantTx(b.ctx, b.tenant.ID, func(ctx context.Context, q *sqlcgen.Queries) error {
			var e error
			rows, e = q.MyConversionInputs(ctx)
			return e
		}))
		return rows
	}
	metric := func(kind string, v float64) {
		exec("INSERT INTO mash_metrics (tenant_id, mash_run_id, kind, value) VALUES ($1, $2, $3, $4)", b.tenant.ID, mash, kind, v)
	}
	metric("original_gravity", 1.050)
	metric("water_volume_l", 30)
	exec("UPDATE materials SET extract_fraction = 0.8 WHERE tenant_id = $1 AND name = 'Malt'", b.tenant.ID)
	if rows := inputs(); len(rows) != 0 {
		t.Errorf("water read as wash: %+v", rows)
	}
	metric("wash_volume_l", 28)

	spec := b.material(t, "Special", "g")
	must(t, b.add(spec, "", 500, "g"))
	if rows := inputs(); len(rows) != 0 {
		t.Errorf("a malt with no extract figure left out of the bill: %+v", rows)
	}
	exec("UPDATE materials SET extract_fraction = 0.7 WHERE id = $1", uuid.MustParse(spec))
	hulls, err := b.mat.CreateMaterial(b.ctx, connect.NewRequest(&stillhousev1.CreateMaterialRequest{
		Name: "Hulls", Kind: stillhousev1.MaterialKind_MATERIAL_KIND_OTHER, Uom: "kg"}))
	must(t, err)
	must(t, b.add(hulls.Msg.GetMaterial().GetId(), "", 1, "kg"))

	rows := inputs()
	if len(rows) != 1 || math.Abs(rows[0].ExtractAvailableKg-(6*0.8+0.5*0.7)) > 1e-9 || rows[0].WashVolumeL != 28 {
		t.Errorf("got %+v, want 5.15 kg of extract in 28 L", rows)
	}
}
