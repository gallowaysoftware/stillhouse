package rpc

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/testdb"
)

// PLAN J3. A cask statement leaves the building, gets kept, and is read
// by somebody with no way to check it. That makes it the last place a
// plausible-looking invented number should appear, and these tests are
// mostly about the refusals rather than the arithmetic.
//
// Needs STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

// caskWithFill seeds a cask, its wood, and a fill gauge. abv <= 0 means
// the fill was recorded without a strength, which is the case the
// statement has to refuse rather than guess around.
func caskWithFill(t *testing.T, f *dutyFixture, filledOn time.Time, volume, abv float64) uuid.UUID {
	t.Helper()
	pool := testdb.AdminPool(t)
	ctx := f.ctx
	b := f.barrel(t, "Cask "+uuid.NewString()[:8], 200)

	if _, err := pool.Exec(ctx, `
		INSERT INTO barrel_attributes (container_id, tenant_id, cooperage_supplier, wood_species,
		                               prior_use, char_level, rickhouse, fill_date)
		VALUES ($1,$2,'Canadian Oak Co','Quercus alba','first fill bourbon',3,'Rackhouse A',$3)
		ON CONFLICT (container_id) DO UPDATE SET
		  cooperage_supplier = EXCLUDED.cooperage_supplier,
		  wood_species       = EXCLUDED.wood_species,
		  prior_use          = EXCLUDED.prior_use,
		  char_level         = EXCLUDED.char_level,
		  rickhouse          = EXCLUDED.rickhouse,
		  fill_date          = EXCLUDED.fill_date`,
		b.ID, f.tenant.ID, filledOn); err != nil {
		t.Fatalf("attributes: %v", err)
	}

	laa := volume * abv / 100
	args := []any{f.tenant.ID, b.ID, filledOn, volume}
	sql := `INSERT INTO barrel_events (tenant_id, container_id, kind, event_date, volume_l, abv_pct, laa)
	        VALUES ($1,$2,'fill',$3,$4,$5,$6)`
	if abv <= 0 {
		// A fill gauge with no strength: the volume was written down and
		// the strength was not.
		sql = `INSERT INTO barrel_events (tenant_id, container_id, kind, event_date, volume_l)
		       VALUES ($1,$2,'fill',$3,$4)`
	} else {
		args = append(args, abv, laa)
	}
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("fill event: %v", err)
	}

	// Where the cask stands today.
	if abv > 0 {
		if err := f.db.WithTenantTx(ctx, f.tenant.ID, func(ctx context.Context, q *sqlcgen.Queries) error {
			_, e := q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
				ID: b.ID, CurrentVolumeL: volume * 0.9,
				CurrentAbvPct: pgtype.Float8{Float64: abv - 2, Valid: true}, CurrentLaa: volume * 0.9 * (abv - 2) / 100,
			})
			return e
		}); err != nil {
			t.Fatalf("balance: %v", err)
		}
	}
	return b.ID
}

func TestCaskStatement_ReportsTheAngelsShare(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewBarrelService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	filled := time.Now().UTC().AddDate(-2, 0, 0)
	id := caskWithFill(t, f, filled, 200, 70) // 140 LAA in

	resp, err := svc.CaskStatement(f.ctx, connect.NewRequest(&stillhousev1.CaskStatementRequest{
		ContainerId: id.String(),
	}))
	if err != nil {
		t.Fatalf("CaskStatement: %v", err)
	}
	m := resp.Msg

	if !m.GetAngelsShareKnown() {
		t.Fatalf("angel's share refused with a complete fill gauge: %q", m.GetAngelsShareMissing())
	}
	// 140 LAA in, 180 L at 68% = 122.4 out: 17.6 LAA over two years.
	if got := m.GetAngelsShareLaa(); got < 17.5 || got > 17.7 {
		t.Errorf("angel's share: got %v, want ~17.6", got)
	}
	if got := m.GetAngelsSharePctPerYear(); got < 6.0 || got > 6.6 {
		t.Errorf("annual rate: got %v%%, want ~6.3%%", got)
	}
	if m.GetDaysInWood() < 720 {
		t.Errorf("days in wood: %d", m.GetDaysInWood())
	}
	if m.GetCooperageSupplier() != "Canadian Oak Co" || m.GetPriorUse() != "first fill bourbon" {
		t.Errorf("wood detail missing: %+v", m)
	}

	// The document must say what it is. A statement kept for eight years
	// is read by somebody who was not part of this conversation.
	if !strings.Contains(m.GetBasis(), "not a certificate of age") {
		t.Errorf("basis does not disclaim a certificate: %q", m.GetBasis())
	}
	if !strings.Contains(m.GetBasis(), "EDM3-1-1") {
		t.Errorf("basis does not cite the passage: %q", m.GetBasis())
	}
}

// The figure a cask owner actually reads, and the one most easily faked:
// subtracting today's LAA from a fill LAA nobody recorded produces a
// number that looks exactly like a real one.
func TestCaskStatement_FillWithoutStrengthRefusesTheLoss(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewBarrelService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	id := caskWithFill(t, f, time.Now().UTC().AddDate(-2, 0, 0), 200, 0)

	resp, err := svc.CaskStatement(f.ctx, connect.NewRequest(&stillhousev1.CaskStatementRequest{
		ContainerId: id.String(),
	}))
	if err != nil {
		t.Fatalf("CaskStatement: %v", err)
	}
	m := resp.Msg
	if m.GetAngelsShareKnown() {
		t.Fatalf("computed a loss of %v against a fill with no strength recorded", m.GetAngelsShareLaa())
	}
	if !strings.Contains(m.GetAngelsShareMissing(), "strength") {
		t.Errorf("refusal does not say why: %q", m.GetAngelsShareMissing())
	}
	if m.GetAngelsShareLaa() != 0 {
		t.Errorf("refused but still reported %v", m.GetAngelsShareLaa())
	}
}

// A cask with no fill gauge at all is a different gap with a different
// fix, and must not read as "no loss".
func TestCaskStatement_NoFillGaugeSaysSo(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewBarrelService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	b := f.barrel(t, "Ungauged cask", 200)

	resp, err := svc.CaskStatement(f.ctx, connect.NewRequest(&stillhousev1.CaskStatementRequest{
		ContainerId: b.ID.String(),
	}))
	if err != nil {
		t.Fatalf("CaskStatement: %v", err)
	}
	if resp.Msg.GetAngelsShareKnown() {
		t.Error("computed a loss for a cask with no fill gauge")
	}
	if !strings.Contains(resp.Msg.GetAngelsShareMissing(), "no fill gauge") {
		t.Errorf("refusal: %q", resp.Msg.GetAngelsShareMissing())
	}
}

// A voided gauge is one the distillery has already said did not stand.
// Printing it on a customer's statement shows them a measurement that was
// withdrawn.
func TestCaskStatement_VoidedGaugesAreNotShown(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewBarrelService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	filled := time.Now().UTC().AddDate(-1, 0, 0)
	id := caskWithFill(t, f, filled, 200, 70)

	pool := testdb.AdminPool(t)
	if _, err := pool.Exec(f.ctx, `
		INSERT INTO barrel_events (tenant_id, container_id, kind, event_date, volume_l, abv_pct, laa, voided_at, notes)
		VALUES ($1,$2,'regauge',$3,190,69,131.1,NOW(),'withdrawn')`,
		f.tenant.ID, id, filled.AddDate(0, 6, 0)); err != nil {
		t.Fatalf("voided regauge: %v", err)
	}

	resp, err := svc.CaskStatement(f.ctx, connect.NewRequest(&stillhousev1.CaskStatementRequest{
		ContainerId: id.String(),
	}))
	if err != nil {
		t.Fatalf("CaskStatement: %v", err)
	}
	for _, g := range resp.Msg.GetGauges() {
		if g.GetNotes() == "withdrawn" {
			t.Error("a voided gauge appears on a customer's statement")
		}
	}
}

// A tank is not a cask, and a statement about one would be a document
// about something that does not age.
func TestCaskStatement_RefusesANonCask(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewBarrelService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tank := f.tank(t, "Not a cask", 0, 0)

	_, err := svc.CaskStatement(f.ctx, connect.NewRequest(&stillhousev1.CaskStatementRequest{
		ContainerId: tank.ID.String(),
	}))
	if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("got %v, want invalid_argument", err)
	}
}
