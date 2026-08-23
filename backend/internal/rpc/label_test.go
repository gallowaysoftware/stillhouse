package rpc

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/labelcode"
)

// What a scanner actually produces, resolved back to the row it names.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestResolveLabel(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewLabelService(f.db, testLogger())

	cask := f.barrel(t, "Cask "+uuid.NewString()[:6], 200)
	product, lot := f.salesStock(t, 750, 43, 120)

	t.Run("a printed cask tag opens the cask", func(t *testing.T) {
		code := labelcode.Encode(labelcode.KindBarrel, cask.ID)
		got, err := svc.ResolveLabel(f.ctx, connect.NewRequest(
			&stillhousev1.ResolveLabelRequest{Scanned: code}))
		if err != nil {
			t.Fatalf("ResolveLabel(%s): %v", code, err)
		}
		tgt := got.Msg.GetTarget()
		if tgt == nil {
			t.Fatal("nothing resolved")
		}
		if tgt.GetId() != cask.ID.String() {
			t.Errorf("resolved to %s, want the cask %s", tgt.GetId(), cask.ID)
		}
		if got, want := tgt.GetKind(), stillhousev1.LabelKind_LABEL_KIND_BARREL; got != want {
			t.Errorf("kind = %v, want %v", got, want)
		}
		if tgt.GetCode() != code {
			t.Errorf("code round-tripped to %q, want %q", tgt.GetCode(), code)
		}
	})

	t.Run("a code read back by eye still resolves", func(t *testing.T) {
		code := labelcode.Encode(labelcode.KindLot, lot.ID)
		typed := strLower(code[:5]) + "-" + strLower(code[5:])
		got, err := svc.ResolveLabel(f.ctx, connect.NewRequest(
			&stillhousev1.ResolveLabelRequest{Scanned: typed}))
		if err != nil {
			t.Fatalf("ResolveLabel(%q): %v", typed, err)
		}
		if got.Msg.GetTarget().GetId() != lot.ID.String() {
			t.Errorf("resolved to %s, want the lot %s",
				got.Msg.GetTarget().GetId(), lot.ID)
		}
	})

	t.Run("a lot code off a bottling record resolves too", func(t *testing.T) {
		got, err := svc.ResolveLabel(f.ctx, connect.NewRequest(
			&stillhousev1.ResolveLabelRequest{Scanned: lot.LotCode}))
		if err != nil {
			t.Fatalf("ResolveLabel(%q): %v", lot.LotCode, err)
		}
		if got.Msg.GetTarget().GetId() != lot.ID.String() {
			t.Errorf("resolved to %s, want the lot", got.Msg.GetTarget().GetId())
		}
	})

	t.Run("a cask tag at the pick screen is refused, and says why", func(t *testing.T) {
		// The refusal is the feature: a picker who grabbed the wrong
		// pallet wants to be told, not redirected to a cask page.
		code := labelcode.Encode(labelcode.KindBarrel, cask.ID)
		_, err := svc.ResolveLabel(f.ctx, connect.NewRequest(
			&stillhousev1.ResolveLabelRequest{
				Scanned: code, Expect: stillhousev1.LabelKind_LABEL_KIND_LOT,
			}))
		if err == nil {
			t.Fatal("a cask tag was accepted as a packaged lot")
		}
		if !contains(err.Error(), "cask") || !contains(err.Error(), "packaged lot") {
			t.Errorf("the refusal should name both kinds, got: %v", err)
		}
	})

	t.Run("a cask code cannot resolve to a tank that shares its prefix", func(t *testing.T) {
		// The kind letter is part of the code for exactly this reason.
		tankCode := labelcode.Encode(labelcode.KindContainer, cask.ID)
		if _, err := svc.ResolveLabel(f.ctx, connect.NewRequest(
			&stillhousev1.ResolveLabelRequest{Scanned: tankCode})); err == nil {
			t.Error("a vessel code resolved to a cask")
		}
	})

	t.Run("nothing is not found", func(t *testing.T) {
		if _, err := svc.ResolveLabel(f.ctx, connect.NewRequest(
			&stillhousev1.ResolveLabelRequest{Scanned: "B00000000000A"})); err == nil {
			t.Error("an unused code resolved to something")
		}
	})

	t.Run("a printable sheet carries the figures the label needs", func(t *testing.T) {
		got, err := svc.ListLabelTargets(f.ctx, connect.NewRequest(
			&stillhousev1.ListLabelTargetsRequest{
				Kind: stillhousev1.LabelKind_LABEL_KIND_LOT,
			}))
		if err != nil {
			t.Fatalf("ListLabelTargets: %v", err)
		}
		var found *stillhousev1.LabelTarget
		for _, tgt := range got.Msg.GetTargets() {
			if tgt.GetId() == lot.ID.String() {
				found = tgt
			}
		}
		if found == nil {
			t.Fatal("a lot with stock on hand is missing from the sheet")
		}
		if found.GetJurisdiction() == "" {
			t.Error("a case label with no jurisdiction cannot say whose stamps are on it")
		}
		if found.GetBottles() != 120 {
			t.Errorf("bottles = %d, want 120", found.GetBottles())
		}
		if found.GetSubtitle() != product.Name {
			t.Errorf("subtitle = %q, want the product name %q", found.GetSubtitle(), product.Name)
		}
		// Every code on a printed sheet has to resolve, or the sheet is
		// a page of labels that scan to nothing.
		for _, tgt := range got.Msg.GetTargets() {
			back, err := svc.ResolveLabel(f.ctx, connect.NewRequest(
				&stillhousev1.ResolveLabelRequest{Scanned: tgt.GetCode()}))
			if err != nil {
				t.Errorf("printed code %q does not resolve: %v", tgt.GetCode(), err)
				continue
			}
			if back.Msg.GetTarget().GetId() != tgt.GetId() {
				t.Errorf("code %q resolves to a different row", tgt.GetCode())
			}
		}
	})
}

func strLower(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'A' && r <= 'Z' {
			out[i] = r + 32
		}
	}
	return string(out)
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
