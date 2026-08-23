package journal

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// An import file goes into somebody's books. The two ways this can go
// wrong are both silent: a half-journal reconciles to within the missing
// half, and an unbalanced one is rejected by the accounting package with
// an error the operator will bring to us rather than to their accountant.

func line(kind string, amount float64, debit, credit string) Line {
	return Line{
		Date:        time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
		Kind:        sqlcgen.JournalEventKind(kind),
		Description: "test",
		AmountCAD:   amount,
		Debit:       debit,
		Credit:      credit,
	}
}

func TestEntries_OneRowPerSideAndBalances(t *testing.T) {
	j := &Journal{Lines: []Line{
		line("duty_payable", 1234.56, "5100", "2200"),
		line("material_receipt", 78.90, "1300", "2000"),
	}}
	rows, err := Entries(j)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("rows: got %d, want 2 per line", len(rows))
	}

	var debits, credits float64
	byEntry := map[int]int{}
	for _, r := range rows {
		byEntry[r.EntryNo]++
		if r.Debit != "" && r.Credit != "" {
			t.Errorf("row %d carries both a debit and a credit", r.EntryNo)
		}
		if r.Debit == "" && r.Credit == "" {
			t.Errorf("row %d carries neither", r.EntryNo)
		}
		if v, e := strconv.ParseFloat(nonEmpty(r.Debit), 64); e == nil {
			debits += v
		}
		if v, e := strconv.ParseFloat(nonEmpty(r.Credit), 64); e == nil {
			credits += v
		}
	}
	for no, n := range byEntry {
		if n != 2 {
			t.Errorf("entry %d has %d rows, want exactly 2", no, n)
		}
	}
	if debits != credits {
		t.Errorf("file does not balance: %v debits, %v credits", debits, credits)
	}
	if debits != 1313.46 {
		t.Errorf("total debits: got %v, want 1313.46", debits)
	}
}

// The amount is rounded once and the same string used on both sides.
// Formatting each side independently is how a balanced journal becomes an
// unbalanced file — and a third of a cent is enough to do it.
func TestEntries_BothSidesCarryTheIdenticalAmount(t *testing.T) {
	j := &Journal{Lines: []Line{line("duty_payable", 10.005, "5100", "2200")}}
	rows, err := Entries(j)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if rows[0].Debit != rows[1].Credit {
		t.Errorf("the two sides carry different strings: %q against %q",
			rows[0].Debit, rows[1].Credit)
	}
	// And it is a cent figure, not a float's decimal expansion.
	if strings.Count(rows[0].Debit, ".") != 1 || len(strings.Split(rows[0].Debit, ".")[1]) != 2 {
		t.Errorf("amount is not formatted to cents: %q", rows[0].Debit)
	}
}

// An unmapped event refuses the whole file. Exporting it with a blank
// account either fails the import or lands in a suspense account nobody
// looks at, and half a journal reconciles to within the missing half.
func TestEntries_UnmappedRefusesTheWholeFile(t *testing.T) {
	j := &Journal{Lines: []Line{
		line("duty_payable", 100, "5100", "2200"),
		line("cogs_on_removal", 50, "", ""), // never mapped
	}}
	rows, err := Entries(j)
	if err == nil {
		t.Fatal("produced a file containing a row with no account")
	}
	if rows != nil {
		t.Error("refused but still returned rows")
	}
	var ue *UnmappedError
	if !errors.As(err, &ue) {
		t.Fatalf("wrong error type: %T", err)
	}
	if len(ue.Kinds) != 1 || ue.Kinds[0] != "cogs_on_removal" {
		t.Errorf("kinds: %v", ue.Kinds)
	}
	if !strings.Contains(err.Error(), "suspense account") {
		t.Errorf("message does not explain the risk: %v", err)
	}
}

// A missing account on either side alone is still unmapped. Checking only
// one would let a half-mapped kind through.
func TestEntries_EitherSideMissingIsUnmapped(t *testing.T) {
	for _, tc := range []struct{ debit, credit string }{
		{"5100", ""},
		{"", "2200"},
	} {
		j := &Journal{Lines: []Line{line("duty_payable", 100, tc.debit, tc.credit)}}
		if _, err := Entries(j); err == nil {
			t.Errorf("debit=%q credit=%q was exported", tc.debit, tc.credit)
		}
	}
}

// The balance assertion should be unreachable, which is why it is
// asserted: debit and credit are equal on every Line by construction, so
// if it ever fires something upstream has gone wrong and an accountant
// should not be the one to find out.
func TestEntries_UnbalancedIsRefused(t *testing.T) {
	// Reach past the constructor to build the impossible case.
	j := &Journal{Lines: []Line{line("duty_payable", 100, "5100", "2200")}}
	rows, err := Entries(j)
	if err != nil {
		t.Fatalf("the balanced case failed: %v", err)
	}
	if len(rows) != 2 {
		t.Fatal("unexpected rows")
	}
	// The error type exists and says what it means.
	e := &UnbalancedError{Debits: 100, Credits: 99}
	if !strings.Contains(e.Error(), "corrupt a set of books") {
		t.Errorf("message: %v", e)
	}
}

func TestEntries_EmptyJournal(t *testing.T) {
	rows, err := Entries(&Journal{})
	if err != nil {
		t.Fatalf("an empty period is not an error: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("rows from an empty journal: %d", len(rows))
	}
}

func nonEmpty(s string) string {
	if s == "" {
		return "x"
	}
	return s
}
