// Package stamps reconciles an excise stamp order against everything
// that happened to the stamps in it.
//
// The question this exists to answer is not "how many stamps are left" —
// three counters on the order already answered that. It is the one CRA
// actually asks, which is "where did stamp ABC00457 go". Answering it
// means walking the issued serial range end to end and saying of every
// serial: applied to this bottling run, disposed of for this reason, or
// still on hand. A serial that is none of those is unaccounted for, and
// that number is the point of the whole exercise.
package stamps

import (
	"fmt"
	"sort"
	"strconv"
)

// Range is a contiguous run of serials, inclusive at both ends.
type Range struct {
	Start string
	End   string
	Count int64
}

// Allocation is what happened to one range.
type Allocation struct {
	Range
	// What it went to, in words: "run 14 — Wolfhead Rye", "lost".
	Purpose string
	// Which of the three buckets it falls in.
	Kind AllocationKind
	// Set when the range could not be derived — a disposition recorded
	// without serials, most often. The count still counts; it just
	// cannot be placed on the number line.
	Unplaced bool
}

type AllocationKind string

const (
	Applied  AllocationKind = "applied"
	Disposed AllocationKind = "disposed"
	OnHand   AllocationKind = "on_hand"
	// Unaccounted is the residual: serials inside the issued range that
	// nothing claims. Not an error in itself — an order part-way through
	// its life has plenty — but it is what the reconciliation is for.
	Unaccounted AllocationKind = "unaccounted"
)

// Reconciliation is an order's full account.
type Reconciliation struct {
	Issued Range
	// IssuedKnown is false when the order has no serial range recorded,
	// in which case only the counts can be reconciled and every
	// allocation is unplaced. Said plainly rather than silently
	// producing an empty number line.
	IssuedKnown bool

	Allocations []Allocation

	// The counters, so a caller can see the arithmetic agree — or not.
	ReceivedCount    int64
	AppliedCount     int64
	DisposedCount    int64
	UnaccountedCount int64

	// Discrepancies are the ways the account fails to close. Each is a
	// sentence, because a number without one is not actionable.
	Discrepancies []string
}

// Input is what the reconciliation needs, so this package depends on no
// database types and is testable on its own.
type Input struct {
	SerialStart      string
	SerialEnd        string
	QuantityReceived int64
	Applications     []Claim
	Dispositions     []Claim
}

// Claim is one asserted use of stamps.
type Claim struct {
	SerialStart string
	SerialEnd   string
	Count       int64
	Purpose     string
}

// Reconcile walks the issued range against every claim on it.
func Reconcile(in Input) Reconciliation {
	r := Reconciliation{ReceivedCount: in.QuantityReceived}

	prefix, first, pad := parseSerial(in.SerialStart)
	_, last, endPad := parseSerial(in.SerialEnd)
	r.IssuedKnown = pad > 0 && endPad > 0 && last >= first
	if r.IssuedKnown {
		r.Issued = Range{Start: in.SerialStart, End: in.SerialEnd, Count: last - first + 1}
	}

	// claimed marks every serial some claim accounts for, so overlaps are
	// visible rather than double-counted away.
	claimed := map[int64]string{}
	var overlaps []string

	place := func(c Claim, kind AllocationKind) {
		alloc := Allocation{
			Range:   Range{Start: c.SerialStart, End: c.SerialEnd, Count: c.Count},
			Purpose: c.Purpose,
			Kind:    kind,
		}
		cs, cFirst, cPad := parseSerial(c.SerialStart)
		_, cLast, cEndPad := parseSerial(c.SerialEnd)
		if !r.IssuedKnown || cPad == 0 || cEndPad == 0 || cs != prefix || cLast < cFirst {
			alloc.Unplaced = true
			r.Allocations = append(r.Allocations, alloc)
			return
		}
		for n := cFirst; n <= cLast; n++ {
			if prev, dup := claimed[n]; dup {
				overlaps = append(overlaps, fmt.Sprintf(
					"serial %s is claimed by both %q and %q",
					formatSerial(prefix, n, pad), prev, c.Purpose))
				continue
			}
			claimed[n] = c.Purpose
		}
		r.Allocations = append(r.Allocations, alloc)
	}

	for _, c := range in.Applications {
		r.AppliedCount += c.Count
		place(c, Applied)
	}
	for _, c := range in.Dispositions {
		r.DisposedCount += c.Count
		place(c, Disposed)
	}

	// Whatever is left of the issued range, in contiguous runs, so the
	// output is a handful of ranges rather than ten thousand serials.
	if r.IssuedKnown {
		for _, run := range gaps(first, last, claimed) {
			r.Allocations = append(r.Allocations, Allocation{
				Range: Range{
					Start: formatSerial(prefix, run[0], pad),
					End:   formatSerial(prefix, run[1], pad),
					Count: run[1] - run[0] + 1,
				},
				Purpose: "not yet used",
				Kind:    OnHand,
			})
		}
	}

	// The arithmetic, checked rather than assumed.
	expectedRemaining := in.QuantityReceived - r.AppliedCount - r.DisposedCount
	if expectedRemaining < 0 {
		r.Discrepancies = append(r.Discrepancies, fmt.Sprintf(
			"%d more stamps are accounted for than were received (%d applied + %d disposed of "+
				"against %d received)",
			-expectedRemaining, r.AppliedCount, r.DisposedCount, in.QuantityReceived))
	}
	if r.IssuedKnown && r.Issued.Count != in.QuantityReceived {
		r.Discrepancies = append(r.Discrepancies, fmt.Sprintf(
			"the serial range %s–%s covers %d stamps but %d were recorded as received",
			r.Issued.Start, r.Issued.End, r.Issued.Count, in.QuantityReceived))
	}
	r.Discrepancies = append(r.Discrepancies, overlaps...)

	// Unaccounted is a count question, not a range one: it is the
	// received total less everything claimed, and it survives even when
	// no serial range was recorded.
	if expectedRemaining > 0 {
		onHandPlaced := int64(0)
		for _, a := range r.Allocations {
			if a.Kind == OnHand {
				onHandPlaced += a.Count
			}
		}
		if r.IssuedKnown && onHandPlaced != expectedRemaining {
			r.UnaccountedCount = expectedRemaining - onHandPlaced
			if r.UnaccountedCount > 0 {
				r.Discrepancies = append(r.Discrepancies, fmt.Sprintf(
					"%d stamp%s cannot be placed on the serial range — either a use was "+
						"recorded without serials, or serials outside the issued range were used",
					r.UnaccountedCount, plural(r.UnaccountedCount)))
			}
		}
	}
	sort.SliceStable(r.Allocations, func(i, j int) bool {
		return r.Allocations[i].Start < r.Allocations[j].Start
	})
	return r
}

// gaps returns the contiguous runs of [first, last] not present in used.
func gaps(first, last int64, used map[int64]string) [][2]int64 {
	var out [][2]int64
	var runStart int64 = -1
	for n := first; n <= last; n++ {
		_, taken := used[n]
		switch {
		case !taken && runStart < 0:
			runStart = n
		case taken && runStart >= 0:
			out = append(out, [2]int64{runStart, n - 1})
			runStart = -1
		}
	}
	if runStart >= 0 {
		out = append(out, [2]int64{runStart, last})
	}
	return out
}

// parseSerial splits a CRA stamp serial into its alpha prefix and
// trailing number. "ABC00457" → ("ABC", 457, 5).
func parseSerial(s string) (prefix string, num int64, padWidth int) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	if i == len(s) {
		return s, 0, 0
	}
	prefix = s[:i]
	num, _ = strconv.ParseInt(s[i:], 10, 64)
	padWidth = len(s) - i
	return
}

func formatSerial(prefix string, num int64, padWidth int) string {
	if padWidth <= 0 {
		return prefix
	}
	return fmt.Sprintf("%s%0*d", prefix, padWidth, num)
}

func plural(n int64) string {
	if n == 1 {
		return ""
	}
	return "s"
}
