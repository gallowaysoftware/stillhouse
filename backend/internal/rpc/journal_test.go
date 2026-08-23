package rpc

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The journal's one discipline: a figure it cannot stand behind does not
// become a number. An unpriced material lot produces no line and a
// warning, never a zero — a zero balances perfectly, reconciles, and
// silently understates inventory by exactly what the lot cost.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestJournalRefusesToInventFigures(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewJournalService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	day := time.Now().UTC().AddDate(0, 0, -1)
	iso := day.Format("2006-01-02")

	material, err := f.q.CreateMaterial(f.ctx, sqlcgen.CreateMaterialParams{
		TenantID: f.tenant.ID, Name: "Journal Rye " + uuid.NewString()[:8],
		Kind: sqlcgen.MaterialKindGrain, Uom: "kg",
	})
	if err != nil {
		t.Fatalf("create material: %v", err)
	}
	// One lot with a cost, one without.
	priced, err := f.q.CreateMaterialLot(f.ctx, sqlcgen.CreateMaterialLotParams{
		TenantID: f.tenant.ID, MaterialID: material.ID, SupplierLot: "PRICED",
		QuantityReceived: 1000,
		ReceivedAt:       pgtype.Timestamptz{Valid: true, Time: day},
		UnitCostCad:      pgtype.Float8{Float64: 0.55, Valid: true},
	})
	if err != nil {
		t.Fatalf("create priced lot: %v", err)
	}
	if _, err := f.q.CreateMaterialLot(f.ctx, sqlcgen.CreateMaterialLotParams{
		TenantID: f.tenant.ID, MaterialID: material.ID, SupplierLot: "UNPRICED",
		QuantityReceived: 500,
		ReceivedAt:       pgtype.Timestamptz{Valid: true, Time: day},
	}); err != nil {
		t.Fatalf("create unpriced lot: %v", err)
	}

	preview := func(t *testing.T) *stillhousev1.PreviewJournalResponse {
		t.Helper()
		resp, err := svc.PreviewJournal(f.ctx, connect.NewRequest(&stillhousev1.PreviewJournalRequest{
			PeriodStart: iso, PeriodEnd: iso,
		}))
		if err != nil {
			t.Fatalf("PreviewJournal: %v", err)
		}
		return resp.Msg
	}

	t.Run("the priced lot posts and the unpriced one warns", func(t *testing.T) {
		msg := preview(t)
		var receipts []*stillhousev1.JournalLine
		for _, l := range msg.GetLines() {
			if l.GetKind() == stillhousev1.JournalEventKind_JOURNAL_EVENT_KIND_MATERIAL_RECEIPT {
				receipts = append(receipts, l)
			}
		}
		if len(receipts) != 1 {
			t.Fatalf("got %d material-receipt lines, want 1 (the unpriced lot must not post)", len(receipts))
		}
		// 1000 kg × 0.55.
		if got := receipts[0].GetAmountCad(); got != 550 {
			t.Errorf("receipt amount %v, want 550", got)
		}
		if receipts[0].GetReference() != "PRICED" {
			t.Errorf("the line that posted is %q, want the priced lot", receipts[0].GetReference())
		}
		var warned bool
		for _, w := range msg.GetWarnings() {
			if w.GetKind() == string(sqlcgen.JournalEventKindMaterialReceipt) {
				warned = true
			}
		}
		if !warned {
			t.Error("an unpriced lot produced no warning; inventory is short and nothing says so")
		}
	})

	t.Run("every line says how its amount was arrived at", func(t *testing.T) {
		for _, l := range preview(t).GetLines() {
			if l.GetBasis() == "" {
				t.Errorf("line %q (%v) carries no basis", l.GetDescription(), l.GetKind())
			}
		}
	})

	t.Run("unmapped kinds warn rather than posting to an invented account", func(t *testing.T) {
		msg := preview(t)
		for _, l := range msg.GetLines() {
			if l.GetKind() != stillhousev1.JournalEventKind_JOURNAL_EVENT_KIND_MATERIAL_RECEIPT {
				continue
			}
			if l.GetDebitAccount() != "" || l.GetCreditAccount() != "" {
				t.Errorf("with no mapping set, a line carried accounts %q/%q",
					l.GetDebitAccount(), l.GetCreditAccount())
			}
		}
		var warned bool
		for _, w := range msg.GetWarnings() {
			if w.GetDetail() != "" && w.GetKind() == string(sqlcgen.JournalEventKindMaterialReceipt) {
				warned = true
			}
		}
		if !warned {
			t.Error("no warning about the missing account mapping")
		}
	})

	t.Run("once mapped, the accounts appear", func(t *testing.T) {
		if _, err := svc.SetJournalAccount(f.ctx, connect.NewRequest(&stillhousev1.SetJournalAccountRequest{
			Mapping: &stillhousev1.JournalAccountMapping{
				Kind:          stillhousev1.JournalEventKind_JOURNAL_EVENT_KIND_MATERIAL_RECEIPT,
				DebitAccount:  "1300",
				DebitName:     "Raw materials inventory",
				CreditAccount: "2000",
				CreditName:    "Accounts payable",
				MemoPrefix:    "STLH",
			},
		})); err != nil {
			t.Fatalf("SetJournalAccount: %v", err)
		}
		for _, l := range preview(t).GetLines() {
			if l.GetKind() != stillhousev1.JournalEventKind_JOURNAL_EVENT_KIND_MATERIAL_RECEIPT {
				continue
			}
			if l.GetDebitAccount() != "1300" || l.GetCreditAccount() != "2000" {
				t.Errorf("mapped line has accounts %q/%q, want 1300/2000",
					l.GetDebitAccount(), l.GetCreditAccount())
			}
			if l.GetMemo() == "" || l.GetMemo()[:4] != "STLH" {
				t.Errorf("memo %q does not carry the configured prefix", l.GetMemo())
			}
		}
	})

	_ = priced // the priced lot is asserted on by reference, not by id
}
