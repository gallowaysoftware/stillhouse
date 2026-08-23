package journal

import (
	"fmt"
	"math"
	"sort"
	"strconv"
)

// Row is one side of one journal entry: the shape QuickBooks Online and
// Xero both import.
//
// The human-readable export puts both accounts on one row, which is
// right for reading and wrong for importing — every accounting package
// wants one row per side, tied together by an entry number.
type Row struct {
	EntryNo     int
	Date        string
	Account     string
	AccountName string
	Debit       string // decimal string, empty on the credit side
	Credit      string // decimal string, empty on the debit side
	Description string
	Memo        string
	Reference   string
}

// UnmappedError is returned when an event in the period has no account.
//
// This refuses rather than exporting the row with a blank account, and
// the reason is 000040's: an unmapped event must be reported as unmapped
// rather than posted to an invented account. A blank account in an import
// file either fails the import — the good case — or lands in a suspense
// account nobody looks at again, which is the bad one. Half a journal is
// worse than none, because it reconciles to within the missing half and
// nobody notices the gap.
type UnmappedError struct {
	Kinds []string
}

func (e *UnmappedError) Error() string {
	return fmt.Sprintf(
		"these event kinds have no account mapping, so an import file cannot be produced: %v. "+
			"A row with no account either fails the import or lands in a suspense account nobody looks at. "+
			"Map them under Settings → Accounting.", e.Kinds)
}

// UnbalancedError is the last check before a file leaves.
//
// Debit and credit are equal on every Line by construction, so this
// should be impossible — which is exactly why it is asserted. A journal
// that does not balance is rejected by both QBO and Xero, and an
// arithmetic slip that produced one would otherwise be discovered by an
// accountant rather than by us.
type UnbalancedError struct {
	Debits, Credits float64
}

func (e *UnbalancedError) Error() string {
	return fmt.Sprintf(
		"the journal does not balance: %.2f in debits against %.2f in credits. "+
			"Stillhouse will not emit an import file that would corrupt a set of books.",
		e.Debits, e.Credits)
}

// Entries expands a journal into importable rows, or refuses.
//
// Amounts are formatted to cents once and the same string is used on both
// sides, so the two can never round apart. Formatting each side
// independently is how a balanced journal becomes an unbalanced file.
func Entries(j *Journal) ([]Row, error) {
	if j == nil {
		return nil, nil
	}
	if unmapped := unmappedKinds(j); len(unmapped) > 0 {
		return nil, &UnmappedError{Kinds: unmapped}
	}

	rows := make([]Row, 0, len(j.Lines)*2)
	var debits, credits float64
	for i, l := range j.Lines {
		// One rounding, used twice.
		cents := math.Round(l.AmountCAD*100) / 100
		amount := strconv.FormatFloat(cents, 'f', 2, 64)
		no := i + 1

		rows = append(rows,
			Row{
				EntryNo: no, Date: l.Date.Format("2006-01-02"),
				Account: l.Debit, AccountName: l.DebitName, Debit: amount,
				Description: l.Description, Memo: l.Memo, Reference: l.Reference,
			},
			Row{
				EntryNo: no, Date: l.Date.Format("2006-01-02"),
				Account: l.Credit, AccountName: l.CreditName, Credit: amount,
				Description: l.Description, Memo: l.Memo, Reference: l.Reference,
			},
		)
		debits += cents
		credits += cents
	}

	// Compared to the cent, because that is the unit the file is in.
	if math.Abs(debits-credits) >= 0.005 {
		return nil, &UnbalancedError{Debits: debits, Credits: credits}
	}
	return rows, nil
}

// unmappedKinds names every kind that produced lines and has no account,
// sorted so the message is stable.
func unmappedKinds(j *Journal) []string {
	seen := map[string]bool{}
	for _, l := range j.Lines {
		if l.Debit == "" || l.Credit == "" {
			seen[string(l.Kind)] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
