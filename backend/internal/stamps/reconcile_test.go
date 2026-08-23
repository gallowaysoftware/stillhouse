package stamps

import "testing"

// The question this package exists to answer is "where did stamp
// ABC00457 go", so the tests ask it in the forms an auditor would.
func TestReconcileAccountsForEverySerial(t *testing.T) {
	r := Reconcile(Input{
		SerialStart: "ABC00001", SerialEnd: "ABC00100", QuantityReceived: 100,
		Applications: []Claim{
			{SerialStart: "ABC00001", SerialEnd: "ABC00040", Count: 40, Purpose: "run 1"},
			{SerialStart: "ABC00041", SerialEnd: "ABC00060", Count: 20, Purpose: "run 2"},
		},
		Dispositions: []Claim{
			{SerialStart: "ABC00061", SerialEnd: "ABC00065", Count: 5, Purpose: "spoiled"},
		},
	})
	if !r.IssuedKnown {
		t.Fatal("a well-formed serial range was not recognised")
	}
	if r.Issued.Count != 100 {
		t.Errorf("issued count %d, want 100", r.Issued.Count)
	}
	if r.AppliedCount != 60 || r.DisposedCount != 5 {
		t.Errorf("applied %d disposed %d, want 60 and 5", r.AppliedCount, r.DisposedCount)
	}
	// The remaining 35 should come back as one contiguous on-hand run,
	// not as thirty-five serials.
	var onHand []Allocation
	for _, a := range r.Allocations {
		if a.Kind == OnHand {
			onHand = append(onHand, a)
		}
	}
	if len(onHand) != 1 {
		t.Fatalf("got %d on-hand runs, want 1 contiguous range", len(onHand))
	}
	if onHand[0].Start != "ABC00066" || onHand[0].End != "ABC00100" || onHand[0].Count != 35 {
		t.Errorf("on hand %s–%s (%d), want ABC00066–ABC00100 (35)",
			onHand[0].Start, onHand[0].End, onHand[0].Count)
	}
	if len(r.Discrepancies) != 0 {
		t.Errorf("a fully reconciled order reported discrepancies: %v", r.Discrepancies)
	}
}

// A gap in the middle is the shape a lost roll makes: stamps either side
// of it were used, and the ones between are on hand only if somebody can
// point at them.
func TestReconcileReportsGapsAsSeparateRuns(t *testing.T) {
	r := Reconcile(Input{
		SerialStart: "ABC00001", SerialEnd: "ABC00010", QuantityReceived: 10,
		Applications: []Claim{
			{SerialStart: "ABC00001", SerialEnd: "ABC00002", Count: 2, Purpose: "run 1"},
			{SerialStart: "ABC00007", SerialEnd: "ABC00008", Count: 2, Purpose: "run 2"},
		},
	})
	var runs [][2]string
	for _, a := range r.Allocations {
		if a.Kind == OnHand {
			runs = append(runs, [2]string{a.Start, a.End})
		}
	}
	if len(runs) != 2 {
		t.Fatalf("got %d on-hand runs, want 2 (either side of the used block)", len(runs))
	}
	if runs[0] != [2]string{"ABC00003", "ABC00006"} {
		t.Errorf("first run %v, want ABC00003–ABC00006", runs[0])
	}
	if runs[1] != [2]string{"ABC00009", "ABC00010"} {
		t.Errorf("second run %v, want ABC00009–ABC00010", runs[1])
	}
}

// Two claims on one serial is the kind of thing a manual serial entry
// produces, and it must be surfaced rather than silently collapsed —
// the total would still add up, which is exactly why it is dangerous.
func TestReconcileCatchesOverlappingClaims(t *testing.T) {
	r := Reconcile(Input{
		SerialStart: "ABC00001", SerialEnd: "ABC00010", QuantityReceived: 10,
		Applications: []Claim{
			{SerialStart: "ABC00001", SerialEnd: "ABC00005", Count: 5, Purpose: "run 1"},
			{SerialStart: "ABC00004", SerialEnd: "ABC00008", Count: 5, Purpose: "run 2"},
		},
	})
	if len(r.Discrepancies) == 0 {
		t.Fatal("overlapping serial claims produced no discrepancy")
	}
	var mentionsOverlap bool
	for _, d := range r.Discrepancies {
		if len(d) > 0 && contains(d, "claimed by both") {
			mentionsOverlap = true
		}
	}
	if !mentionsOverlap {
		t.Errorf("discrepancies %v do not name the overlap", r.Discrepancies)
	}
}

// The counters and the range have to agree, and when they don't that is
// the finding.
func TestReconcileCatchesRangeCountMismatch(t *testing.T) {
	r := Reconcile(Input{
		SerialStart: "ABC00001", SerialEnd: "ABC00100", QuantityReceived: 90,
	})
	if len(r.Discrepancies) == 0 {
		t.Fatal("a 100-serial range recorded as 90 received produced no discrepancy")
	}
}

func TestReconcileCatchesOverAccounting(t *testing.T) {
	r := Reconcile(Input{
		SerialStart: "ABC00001", SerialEnd: "ABC00010", QuantityReceived: 10,
		Applications: []Claim{{SerialStart: "ABC00001", SerialEnd: "ABC00010", Count: 10, Purpose: "run 1"}},
		Dispositions: []Claim{{Count: 5, Purpose: "lost"}},
	})
	if len(r.Discrepancies) == 0 {
		t.Fatal("accounting for more stamps than were received produced no discrepancy")
	}
}

// An order with no serial range still reconciles by count. Plenty of
// real orders are recorded that way, and refusing to say anything about
// them would be less useful than saying what can be said.
func TestReconcileWithoutSerialsStillCounts(t *testing.T) {
	r := Reconcile(Input{
		QuantityReceived: 100,
		Applications:     []Claim{{Count: 60, Purpose: "run 1"}},
		Dispositions:     []Claim{{Count: 5, Purpose: "spoiled"}},
	})
	if r.IssuedKnown {
		t.Error("an order with no serial range was treated as having one")
	}
	if r.AppliedCount != 60 || r.DisposedCount != 5 {
		t.Errorf("applied %d disposed %d, want 60 and 5", r.AppliedCount, r.DisposedCount)
	}
	for _, a := range r.Allocations {
		if !a.Unplaced {
			t.Errorf("allocation %q was placed on a range that does not exist", a.Purpose)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
