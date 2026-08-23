package rpc

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The two properties that decide whether an importer is trustworthy: a
// dry run leaves nothing behind, and a file with any bad row imports
// none of it. Partial imports are what make people stop using
// importers — half the casks arrive, nobody knows which half, and the
// second attempt duplicates the first.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestImportIsAllOrNothing(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewImportService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tag := uuid.NewString()[:8]

	countMaterials := func(t *testing.T) int {
		t.Helper()
		rows, err := f.q.ListMaterials(f.ctx, sqlcgen.ListMaterialsParams{IncludeArchived: true})
		if err != nil {
			t.Fatalf("list materials: %v", err)
		}
		return len(rows)
	}

	run := func(t *testing.T, csv string, commit bool) *stillhousev1.RunImportResponse {
		t.Helper()
		resp, err := svc.RunImport(f.ctx, connect.NewRequest(&stillhousev1.RunImportRequest{
			Kind: stillhousev1.ImportKind_IMPORT_KIND_MATERIALS, Csv: csv, Commit: commit,
		}))
		if err != nil {
			t.Fatalf("RunImport: %v", err)
		}
		return resp.Msg
	}

	good := fmt.Sprintf(`name,kind,uom
Rye %s,grain,kg
Malted Barley %s,malt,kg
Distillers Yeast %s,yeast,kg
`, tag, tag, tag)

	t.Run("a dry run reports what would happen and writes nothing", func(t *testing.T) {
		before := countMaterials(t)
		got := run(t, good, false)
		if got.GetRowsRead() != 3 || got.GetRowsAccepted() != 3 {
			t.Errorf("read %d accepted %d, want 3 and 3", got.GetRowsRead(), got.GetRowsAccepted())
		}
		if got.GetCommitted() {
			t.Error("a dry run reported itself as committed")
		}
		if len(got.GetProblems()) != 0 {
			t.Errorf("a valid file reported problems: %v", got.GetProblems())
		}
		if after := countMaterials(t); after != before {
			t.Errorf("a dry run created %d materials", after-before)
		}
	})

	t.Run("committing writes them", func(t *testing.T) {
		before := countMaterials(t)
		got := run(t, good, true)
		if !got.GetCommitted() {
			t.Fatalf("commit did not take: %v", got.GetProblems())
		}
		if after := countMaterials(t); after != before+3 {
			t.Errorf("created %d materials, want 3", after-before)
		}
	})

	t.Run("one bad row rejects the whole file", func(t *testing.T) {
		before := countMaterials(t)
		bad := fmt.Sprintf(`name,kind,uom
Wheat %s,grain,kg
Corn %s,cereal,kg
Oats %s,grain,kg
`, tag, tag, tag)
		got := run(t, bad, true)
		if got.GetCommitted() {
			t.Fatal("a file with an invalid row was committed")
		}
		if len(got.GetProblems()) != 1 {
			t.Fatalf("got %d problems, want 1: %v", len(got.GetProblems()), got.GetProblems())
		}
		p := got.GetProblems()[0]
		if p.GetRow() != 3 {
			t.Errorf("problem reported on row %d, want 3 — the header is row 1", p.GetRow())
		}
		if p.GetColumn() != "kind" || !strings.Contains(p.GetDetail(), "cereal") {
			t.Errorf("problem %v does not name the bad value in the right column", p)
		}
		// The two valid rows either side must not have landed.
		if after := countMaterials(t); after != before {
			t.Errorf("a rejected file still created %d materials", after-before)
		}
	})

	t.Run("a name that already exists is caught by the dry run", func(t *testing.T) {
		// This is the case validation alone cannot see: the file is
		// perfectly well-formed and collides with the database. The dry
		// run has to actually attempt the write to know.
		before := countMaterials(t)
		got := run(t, good, false)
		if len(got.GetProblems()) == 0 {
			t.Fatal("re-importing existing materials reported no problem")
		}
		if !strings.Contains(got.GetProblems()[0].GetDetail(), "already exists") {
			t.Errorf("problem %q does not say the row already exists", got.GetProblems()[0].GetDetail())
		}
		if after := countMaterials(t); after != before {
			t.Errorf("the dry run created %d materials while checking", after-before)
		}
	})

	t.Run("a duplicate inside the file is caught before the database sees it", func(t *testing.T) {
		dup := fmt.Sprintf(`name,kind,uom
Spelt %s,grain,kg
Spelt %s,grain,kg
`, tag, tag)
		got := run(t, dup, false)
		if len(got.GetProblems()) != 1 {
			t.Fatalf("got %d problems, want 1: %v", len(got.GetProblems()), got.GetProblems())
		}
		if !strings.Contains(got.GetProblems()[0].GetDetail(), "row 2") {
			t.Errorf("problem %q does not point at the first occurrence",
				got.GetProblems()[0].GetDetail())
		}
	})

	t.Run("a misspelled column is reported, not ignored", func(t *testing.T) {
		got := run(t, "name,knid,uom\nx,grain,kg\n", false)
		if len(got.GetProblems()) == 0 {
			t.Fatal("a misspelled header column was accepted silently")
		}
		var named bool
		for _, p := range got.GetProblems() {
			if p.GetColumn() == "knid" || p.GetColumn() == "kind" {
				named = true
			}
		}
		if !named {
			t.Errorf("problems %v do not name the misspelled or missing column", got.GetProblems())
		}
	})
}

// Barrels are the headline case — real casks in from a spreadsheet — and
// the one where the project's strength discipline has to survive contact
// with a bulk import.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestImportBarrelsSaysWhatItDidNotCorrect(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewImportService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tag := uuid.NewString()[:8]

	csv := fmt.Sprintf(`name,capacity l,volume l,abv,fill date,prior use
Cask %s-1,200,180,62.5,2024-03-01,ex-bourbon
Cask %s-2,200,175,61.8,2024-03-02,virgin
`, tag, tag)

	resp, err := svc.RunImport(f.ctx, connect.NewRequest(&stillhousev1.RunImportRequest{
		Kind: stillhousev1.ImportKind_IMPORT_KIND_BARRELS, Csv: csv, Commit: true,
	}))
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	if !resp.Msg.GetCommitted() {
		t.Fatalf("barrel import failed: %v", resp.Msg.GetProblems())
	}

	// The note is the point: no temperature was given, so the strengths
	// went in as supplied and the operator is told rather than left to
	// assume they were corrected.
	var warned bool
	for _, n := range resp.Msg.GetNotes() {
		if strings.Contains(n, "uncorrected") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("notes %v do not say the strengths were recorded uncorrected", resp.Msg.GetNotes())
	}

	barrels, err := f.q.ListBarrels(f.ctx, true)
	if err != nil {
		t.Fatalf("list barrels: %v", err)
	}
	var found int
	for _, b := range barrels {
		if !strings.HasPrefix(b.Name, "Cask "+tag) {
			continue
		}
		found++
		if b.CurrentVolumeL != 180 && b.CurrentVolumeL != 175 {
			t.Errorf("cask %s has volume %v", b.Name, b.CurrentVolumeL)
		}
		// LAA has to be right, because everything downstream is built on it.
		wantLAA := b.CurrentVolumeL * b.CurrentAbvPct.Float64 / 100
		if diff := b.CurrentLaa - wantLAA; diff > 0.001 || diff < -0.001 {
			t.Errorf("cask %s LAA %v, want %v", b.Name, b.CurrentLaa, wantLAA)
		}
		if !b.FillDate.Valid {
			t.Errorf("cask %s has no fill date; the maturation clock will not run", b.Name)
		}
	}
	if found != 2 {
		t.Errorf("found %d imported casks, want 2", found)
	}

	// Over-filling is refused rather than stored as an impossible cask.
	over := fmt.Sprintf("name,capacity l,volume l,abv\nCask %s-3,200,250,62\n", tag)
	got, err := svc.RunImport(f.ctx, connect.NewRequest(&stillhousev1.RunImportRequest{
		Kind: stillhousev1.ImportKind_IMPORT_KIND_BARRELS, Csv: over, Commit: true,
	}))
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	if got.Msg.GetCommitted() {
		t.Error("a cask holding more than its capacity was imported")
	}
}
