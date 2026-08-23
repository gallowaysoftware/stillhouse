package rpc

import (
	"fmt"
	"math"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/excise"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// b266Totals is every figure the B266 projection reads, gathered in one
// struct so that the arithmetic which turns them into a return can be
// exercised without a database.
//
// The split matters more than it looks. projectB266 is the highest
// consequence code in the product — it is what CRA reads — and until this
// struct existed the only way to run a single line of it was to stand up
// Postgres, seed a tenant, and assert on the far end. That is why the
// reverse-walk bugs in stages 109 and 134 were found by hand against a
// live system instead of by a test.
type b266Totals struct {
	// byReason is bulk_movements.laa summed per reason over the period.
	// Missing keys read as zero, which is what a period with no movement
	// of that kind should project.
	byReason map[string]float64

	// Closing balances as at the end of the period — not as at the moment
	// the report is generated. The gather step walks the running totals
	// back over everything that moved after the period closed, so
	// generating May's return in August reports May's balance (stage 141).
	bulkClosingLAA float64
	// Current facts about ownership and possession, not walked back to the
	// period end — see BulkOwnershipSplitAsOf. Shown so the operator can
	// see what the closing balance is made of.
	heldForOthersLAA       float64
	heldElsewhereLAA       float64
	thirdPartyElsewhereLAA float64

	// Marked special containers: the third column of the packaging split,
	// and what left in them.
	markedPackagedLAA     float64
	markedPackagedLitres  float64
	markedPackagedCount   int32
	markedDeliveredLAA    float64
	markedDeliveredLitres float64
	markedDeliveredCount  int32
	markedDeliveredDuty   float64

	// Line D's packaged half.
	packagedAdjNetLAA      float64
	packagedAdjIncreaseLAA float64
	packagedAdjDecreaseLAA float64
	packagedAdjCount       int32
	packagedClosingLAA     float64
	packagedClosingBottle  int32

	// Bottling runs in the period, voided runs excluded.
	//
	// Three quantities that must not be conflated: drawnLAA is the tank
	// gauge (what left bulk), packagedLAA is what became sealed bottles,
	// and lossLAA is the difference. Packaged inventory only ever
	// received packagedLAA.
	bottlingDrawnLAA    float64
	bottlingPackagedLAA float64
	bottlingLossLAA     float64
	bottlingBottles     int32

	// Removals in the period, voided removals excluded, split by rate
	// band because the two bands are not charged in the same unit.
	removedLAA           float64
	removedBottles       int32
	removedDutyCAD       float64
	removedOver7LAA      float64
	removedOver7DutyCAD  float64
	removedOver7Bottles  int32
	removedUnder7Litres  float64
	removedUnder7DutyCAD float64
	removedUnder7Bottles int32

	// The duty rates in force over the period, and the CRA notice they
	// were read from. Quoted on the return so each band's line can be
	// checked against the quantity it is charged on, and so an operator
	// can see which notice the figures came from rather than trusting
	// that whatever is compiled in is current.
	//
	// Resolved by the gather step from the period's dates rather than
	// read from a constant: a return for a period before the last
	// indexation must quote that period's rate, not today's.
	dutyBand excise.Band

	// The tenant's duty point and the date it started governing. On the
	// return because the figures cannot be checked without knowing which
	// event crystallised them.
	dutyPoint     sqlcgen.DutyPoint
	dutyPointFrom time.Time

	// Duty crystallised at packaging during the period, split by the same
	// two rate bands as removals, plus the quantity packaged duty-paid.
	packagedDutyCAD          float64
	packagedDutyOver7LAA     float64
	packagedDutyOver7CAD     float64
	packagedDutyUnder7Litres float64
	packagedDutyUnder7CAD    float64
	packagedDutyPaidLAA      float64
	packagedDutyPaidBottles  int32

	// Line D: reason-coded adjustments reconciling book inventory to
	// physical. Both directions, because a period that found 3 LAA in one
	// tank and lost 3 in another nets to zero, and a line showing only the
	// net would say nothing happened.
	adjustmentsNetLAA      float64
	adjustmentsIncreaseLAA float64
	adjustmentsDecreaseLAA float64
	adjustmentsCount       int32

	// Losses split by duty treatment (EDM3-4-1). The three always sum to
	// the losses total; destructions carry a treatment too but are
	// reported on their own line, so they are held separately.
	lossesRelievedLAA        float64
	lossesDutiableLAA        float64
	lossesUnclassifiedLAA    float64
	lossesUnclassifiedCount  int32
	destroyedUnclassifiedLAA float64
	destroyedUnclassifiedN   int32
	// Duty on the dutiable losses, at the rate in force over the period.
	dutyOnLossesCAD float64

	// When this return falls due, and how long there is. Zero time means
	// the fiscal calendar could not place the period.
	dueOn time.Time
	// Set when the period's dates are not the one the licensee elected to
	// file. Not a refusal — a draft over an odd range to look at the
	// figures is legitimate — so it becomes a blocker instead.
	electionMismatch string
	// Set when the period spans an excise indexation. Not an error — a
	// semi-annual period always does — but the single rate the form asks
	// to be quoted cannot describe the whole period.
	rateChangeNote string

	// Continuity inputs: the closing balances of the last period actually
	// filed, and the entries booked into it after it was filed.
	//
	// Gathered rather than derived because they need the database, but
	// compared in projectB266, because what they are compared against is
	// the opening balance projectB266 itself reverse-walks. Splitting it
	// that way keeps the comparison testable without a database, which is
	// the whole reason the projection is a pure function.
	//
	// priorFiled false means there was no filed period before this one —
	// a first return, or one whose predecessors are all still drafts.
	priorFiled           bool
	priorStart           time.Time
	priorEnd             time.Time
	priorBulkClosing     float64
	priorPackagedClosing float64
	// Entries whose occurred_at falls inside the prior filed period but
	// whose created_at is after it was submitted, largest effect first,
	// capped at backdatedListLimit. backdatedCount and backdatedNetLAA
	// describe the whole set, not the capped list.
	backdated       []*stillhousev1.B266BackdatedEntry
	backdatedCount  int32
	backdatedNetLAA float64
}

// backdatedListLimit caps how many offending entries a return names. The
// count and the net effect are always for the whole set; this only bounds
// the list, so a period with hundreds of late entries reports the total
// honestly and names the ones worth looking at.
const backdatedListLimit = 20

// continuityToleranceLAA is the width of "these agree". Both sides are
// round4 figures, so a comparison at exact equality would report a break
// on floating-point noise alone. One part in ten thousand of a litre is
// below anything a gauge can resolve.
const continuityToleranceLAA = 0.0001

// laa returns the LAA summed against a bulk movement reason, or zero if
// the period saw no movement of that kind.
func (t b266Totals) laa(reason string) float64 { return t.byReason[reason] }

// projectB266 turns gathered totals into the return. Pure: same inputs,
// same report, no clock and no database.
func projectB266(t b266Totals, periodStart, periodEnd, generatedAt time.Time) *stillhousev1.B266Report {
	report := &stillhousev1.B266Report{
		PeriodStart: periodStart.Format("2006-01-02"),
		PeriodEnd:   periodEnd.Format("2006-01-02"),

		BulkProductionLaa:                t.laa("production_gauge"),
		BulkReceivedInBondLaa:            t.laa("transfer_in_bond"),
		BulkBlendInLaa:                   t.laa("blend"),
		BulkTransferredToPackagingLaa:    t.laa("transfer_to_packaging"),
		BulkTransferredOutInBondLaa:      t.laa("transfer_out_in_bond"),
		BulkLossesLaa:                    t.laa("loss_evaporation") + t.laa("loss_unaccounted"),
		BulkDestroyedLaa:                 t.laa("destruction"),
		BulkClosingLaa:                   round4(t.bulkClosingLAA),
		BulkClosingHeldForOthersLaa:      round4(t.heldForOthersLAA),
		BulkHeldElsewhereLaa:             round4(t.heldElsewhereLAA),
		BulkThirdPartyElsewhereLaa:       round4(t.thirdPartyElsewhereLAA),
		PackagedMarkedContainersLaa:      round4(t.markedPackagedLAA),
		PackagedMarkedContainersLitres:   round4(t.markedPackagedLitres),
		PackagedMarkedContainersCount:    t.markedPackagedCount,
		DeliveredMarkedContainersLaa:     round4(t.markedDeliveredLAA),
		DeliveredMarkedContainersLitres:  round4(t.markedDeliveredLitres),
		DeliveredMarkedContainersCount:   t.markedDeliveredCount,
		DeliveredMarkedContainersDutyCad: round2(t.markedDeliveredDuty),
		PackagedAdjustmentsNetLaa:        round4(t.packagedAdjNetLAA),
		PackagedAdjustmentsIncreaseLaa:   round4(t.packagedAdjIncreaseLAA),
		PackagedAdjustmentsDecreaseLaa:   round4(t.packagedAdjDecreaseLAA),
		PackagedAdjustmentsCount:         t.packagedAdjCount,
		// Adopted stock is reported but deliberately NOT counted among the
		// receipts below, so the reverse-walk puts it in the opening
		// balance. It was in the warehouse before the period; only the
		// bookkeeping is new. Counting it as a receipt would overstate what
		// the distillery made, on a return CRA reads.
		BulkOpeningInventoryAdoptedLaa: round4(t.laa("opening_inventory")),

		// The rest of EDM10-1-7 page 3. Four of these lines existed on the
		// report from the beginning and were structurally always zero,
		// because nothing in the application ever wrote the movement:
		// received in bond, transferred out in bond, destroyed, and
		// unaccounted loss. They have a path now (stage 146).
		BulkImportedLaa:                 t.laa("import_received"),
		BulkReceivedFromLicenseeLaa:     t.laa("received_from_spirits_licensee"),
		BulkReceivedFromLicensedUserLaa: t.laa("received_from_licensed_user"),
		BulkPackagedReturnedToBulkLaa:   t.laa("packaged_returned_to_bulk"),
		BulkDeliveredToLicenseeLaa:      t.laa("delivered_to_spirits_licensee"),
		BulkDeliveredToLicensedUserLaa:  t.laa("delivered_to_licensed_user"),
		BulkExportedLaa:                 t.laa("exported"),
		BulkDenaturedDaLaa:              t.laa("denatured_da"),
		BulkDenaturedSdaLaa:             t.laa("denatured_sda"),
		BulkReturnedToProductionLaa:     t.laa("returned_to_production"),

		BulkAdjustmentsLaa:         round4(t.adjustmentsNetLAA),
		BulkAdjustmentsIncreaseLaa: round4(t.adjustmentsIncreaseLAA),
		BulkAdjustmentsDecreaseLaa: round4(t.adjustmentsDecreaseLAA),
		BulkAdjustmentsCount:       t.adjustmentsCount,

		BulkLossesRelievedLaa:     round4(t.lossesRelievedLAA),
		BulkLossesDutiableLaa:     round4(t.lossesDutiableLAA),
		BulkLossesUnclassifiedLaa: round4(t.lossesUnclassifiedLAA),
		DutyOnLossesCad:           round2cents(t.dutyOnLossesCAD),

		PackagedPackagedLaa:            round4(t.bottlingPackagedLAA),
		PackagedPackagingLossLaa:       round4(t.bottlingLossLAA),
		PackagedPackagedBottles:        t.bottlingBottles,
		PackagedRemovedDutyPaidLaa:     round4(t.removedLAA),
		PackagedRemovedDutyPaidBottles: t.removedBottles,
		PackagedClosingLaa:             round4(t.packagedClosingLAA),
		PackagedClosingBottles:         t.packagedClosingBottle,

		PackagedRemovedOver7Laa:      round4(t.removedOver7LAA),
		PackagedRemovedOver7DutyCad:  round2cents(t.removedOver7DutyCAD),
		PackagedRemovedOver7Bottles:  t.removedOver7Bottles,
		PackagedRemovedUnder7Litres:  round4(t.removedUnder7Litres),
		PackagedRemovedUnder7DutyCad: round2cents(t.removedUnder7DutyCAD),
		PackagedRemovedUnder7Bottles: t.removedUnder7Bottles,

		// Both rates travel with the return so each band's line can be
		// checked against the quantity it is charged on. Quoting only the
		// per-LAA rate beside a blended LAA total made the arithmetic fail
		// for any period holding both bands.
		// Packaging split by duty treatment. An at-packaging licensee
		// cannot hold non-duty-paid packaged spirits at all, so everything
		// it bottles is duty-paid the moment it is packaged; the
		// non-duty-paid line is the remainder, which is what an
		// at-removal tenant's whole production is.
		PackagedDutyPaidLaa:        round4(t.packagedDutyPaidLAA),
		PackagedDutyPaidBottles:    t.packagedDutyPaidBottles,
		PackagedNonDutyPaidLaa:     round4(t.bottlingPackagedLAA - t.packagedDutyPaidLAA),
		PackagedNonDutyPaidBottles: t.bottlingBottles - t.packagedDutyPaidBottles,

		PackagedDutiedOver7Laa:      round4(t.packagedDutyOver7LAA),
		PackagedDutiedOver7DutyCad:  round2cents(t.packagedDutyOver7CAD),
		PackagedDutiedUnder7Litres:  round4(t.packagedDutyUnder7Litres),
		PackagedDutiedUnder7DutyCad: round2cents(t.packagedDutyUnder7CAD),

		DutyRatePerLaa:         t.dutyBand.PerLAAOver7Pct,
		DutyRatePerLitreUnder7: t.dutyBand.PerLitreAtOrUnder7,
		// Both sources. For any one tenant in any one period only one is
		// normally populated — but across a duty-point cutover both are,
		// because stock packaged before the change is still dutied on its
		// way out. Summing rather than choosing is what makes that period
		// add up.
		// Duty on losses that were ruled dutiable belongs here too. Under
		// EDM3-4-1 spirits that cannot be accounted for are duty-payable,
		// and a return that leaves them out understates what is owed.
		DutyPayableCad:         round2cents(t.packagedDutyCAD + t.removedDutyCAD + t.dutyOnLossesCAD),
		DutyPoint:              dutyPointToProto(t.dutyPoint),
		DutyPointEffectiveFrom: t.dutyPointFrom.Format("2006-01-02"),
		GeneratedAt:            timestamppb.New(generatedAt),
	}

	// Reverse-walk opening balances.
	// Blending is an internal move — alcohol goes from one of the
	// distillery's vessels into another, and nothing enters or leaves the
	// premises. It was being added to receipts with no matching
	// withdrawal, so a blend left the closing balance untouched (correctly)
	// while driving the reverse-walked opening balance DOWN by the blended
	// LAA and reporting a receipt that never happened. Barrel fills and
	// dumps, which are the same kind of internal move, are already in
	// neither column; blend was the outlier. BulkBlendInLaa is still
	// reported, for information.
	//
	// Adjustments are on both sides, by direction. An adjustment is not a
	// residual and never was — it is a deliberate, attributable entry — so
	// it belongs in the walk explicitly rather than being absorbed by the
	// opening balance the way an unreported movement is.
	bulkReceipts := report.BulkProductionLaa + report.BulkReceivedInBondLaa +
		report.BulkAdjustmentsIncreaseLaa +
		report.BulkImportedLaa + report.BulkReceivedFromLicenseeLaa +
		report.BulkReceivedFromLicensedUserLaa + report.BulkPackagedReturnedToBulkLaa
	bulkWithdrawals := report.BulkTransferredToPackagingLaa + report.BulkTransferredOutInBondLaa +
		report.BulkLossesLaa + report.BulkDestroyedLaa + report.BulkAdjustmentsDecreaseLaa +
		report.BulkDeliveredToLicenseeLaa + report.BulkDeliveredToLicensedUserLaa +
		report.BulkExportedLaa + report.BulkDenaturedDaLaa + report.BulkDenaturedSdaLaa +
		report.BulkReturnedToProductionLaa
	report.BulkOpeningLaa = round4(report.BulkClosingLaa - bulkReceipts + bulkWithdrawals)

	// Packaged inventory only ever received what became bottles. Walking
	// back with the tank-gauge figure instead — which includes what was
	// spilled on the way — subtracts alcohol that never arrived, and a
	// first-ever return came out with a negative opening balance.
	report.PackagedOpeningLaa = round4(report.PackagedClosingLaa - report.PackagedPackagedLaa + report.PackagedRemovedDutyPaidLaa)

	if !t.dueOn.IsZero() {
		report.DueOn = t.dueOn.Format("2006-01-02")
		report.DaysUntilDue = int32(t.dueOn.Sub(dayStart(generatedAt)).Hours() / 24)
	}
	report.Continuity = continuity(t, report)
	report.FilingBlockers = filingBlockers(t, report.Continuity)
	return report
}

// continuity compares this return's opening balances against the closing
// balances of the last return actually filed.
//
// This is the only independent check the return has. Every other figure on
// it is derived from the same ledger it is being checked against, and the
// opening balance in particular is reverse-walked from closing — so the
// return balances against itself no matter what is missing. The prior
// period's closing balance is different in kind: it is a number the
// licensee already sent CRA, and it does not move when the ledger does.
//
// A break means one of three things, and Stillhouse does not guess which:
// a movement was entered against the filed period after it was filed
// (named in backdated, which is the common case and the one that is
// fixable), a movement is missing entirely, or the prior return was wrong.
func continuity(t b266Totals, report *stillhousev1.B266Report) *stillhousev1.B266Continuity {
	c := &stillhousev1.B266Continuity{
		BulkOpeningLaa:     report.BulkOpeningLaa,
		PackagedOpeningLaa: report.PackagedOpeningLaa,
	}
	if !t.priorFiled {
		return c
	}
	c.Checked = true
	c.PriorPeriodStart = t.priorStart.Format("2006-01-02")
	c.PriorPeriodEnd = t.priorEnd.Format("2006-01-02")
	c.PriorBulkClosingLaa = round4(t.priorBulkClosing)
	c.PriorPackagedClosingLaa = round4(t.priorPackagedClosing)
	c.BulkDiscrepancyLaa = round4(report.BulkOpeningLaa - c.PriorBulkClosingLaa)
	c.PackagedDiscrepancyLaa = round4(report.PackagedOpeningLaa - c.PriorPackagedClosingLaa)
	c.Backdated = t.backdated
	c.BackdatedNetLaa = round4(t.backdatedNetLAA)
	if n := t.backdatedCount - int32(len(t.backdated)); n > 0 {
		c.BackdatedTruncated = n
	}

	// A gap is not an error on its own — a licensee with nothing to report
	// still has a continuous ledger across the span — but the comparison
	// reaches across it, so anything that moved in between reads as a
	// break. Saying so is the difference between a useful signal and a
	// false alarm the operator learns to ignore.
	if day := t.priorEnd.AddDate(0, 0, 1); !day.Equal(periodStartOf(report)) {
		c.Gap = true
		c.GapNote = fmt.Sprintf(
			"The last filed return ended %s and this one starts %s, so %s is covered by neither. Any movement in that span shows up here as a discrepancy.",
			c.PriorPeriodEnd, report.PeriodStart, describeGap(day, periodStartOf(report)))
	}
	return c
}

// periodStartOf re-parses the report's own formatted start date, so the
// comparison is made against the date the return states rather than
// against a separate value that could disagree with it.
func periodStartOf(report *stillhousev1.B266Report) time.Time {
	d, err := time.Parse("2006-01-02", report.PeriodStart)
	if err != nil {
		return time.Time{}
	}
	return d
}

// describeGap names the uncovered span in days, in the operator's words.
func describeGap(from, to time.Time) string {
	days := int(to.Sub(from).Hours() / 24)
	switch {
	case days <= 0:
		return "an overlapping span"
	case days == 1:
		return "one day"
	default:
		return fmt.Sprintf("%d days", days)
	}
}

// The `int(x*N + 0.5)` idiom these used to spell rounds correctly only for
// positive numbers: int() truncates toward zero, so on a negative the +0.5
// bias pushes the result the wrong way. round4(-0.12345) came out -0.1234
// against +0.1235 for the positive, and round4(-1.5) came out -1.4999.
//
// Negatives do reach here — a cask's strength drift is negative in a cool,
// humid warehouse, which is the normal Canadian case. math.Round rounds
// half away from zero symmetrically and is identical for positives, so
// nothing already recorded moves.
func round2cents(x float64) float64 {
	return math.Round(x*100) / 100
}

func round4(x float64) float64 {
	return math.Round(x*10000) / 10000
}

// filingBlockers lists, in the operator's words, what stops this period
// being filed as it stands.
//
// An empty list is not a promise the figures are right — only that nothing
// is outstanding. The distinction matters: Stillhouse never files, and a
// green light it cannot honestly give would be worse than no light at all.
func filingBlockers(t b266Totals, c *stillhousev1.B266Continuity) []string {
	var out []string
	out = append(out, continuityBlockers(c)...)
	if t.electionMismatch != "" {
		out = append(out, t.electionMismatch)
	}
	if t.rateChangeNote != "" {
		out = append(out, t.rateChangeNote)
	}
	if t.lossesUnclassifiedCount > 0 {
		out = append(out, fmt.Sprintf(
			"%d loss%s totalling %.4f LAA have no duty treatment. Under EDM3-4-1 a relieved loss and one that cannot be accounted for are charged differently, and Stillhouse will not guess which these are.",
			t.lossesUnclassifiedCount, plural(t.lossesUnclassifiedCount), t.lossesUnclassifiedLAA))
	}
	if t.destroyedUnclassifiedN > 0 {
		out = append(out, fmt.Sprintf(
			"%d destruction%s totalling %.4f LAA have no duty treatment. A destruction is relieved only where CRA approved it, and the approval reference has to be on file.",
			t.destroyedUnclassifiedN, pluralS(t.destroyedUnclassifiedN), t.destroyedUnclassifiedLAA))
	}
	return out
}

// continuityBlockers turns a continuity break into the sentences an
// operator can act on.
//
// A break is reported as a blocker rather than a warning because of what
// it means: the opening balance on this return contradicts the closing
// balance on one already filed. One of the two is wrong, and filing the
// second without resolving that puts a figure in front of CRA that the
// licensee's own prior return disagrees with.
func continuityBlockers(c *stillhousev1.B266Continuity) []string {
	if c == nil || !c.Checked {
		return nil
	}
	var out []string
	broken := math.Abs(c.BulkDiscrepancyLaa) > continuityToleranceLAA ||
		math.Abs(c.PackagedDiscrepancyLaa) > continuityToleranceLAA
	if !broken {
		return nil
	}
	for _, d := range []struct {
		what string
		by   float64
		from float64
		to   float64
	}{
		{"Bulk", c.BulkDiscrepancyLaa, c.PriorBulkClosingLaa, c.BulkOpeningLaa},
		{"Packaged", c.PackagedDiscrepancyLaa, c.PriorPackagedClosingLaa, c.PackagedOpeningLaa},
	} {
		if math.Abs(d.by) <= continuityToleranceLAA {
			continue
		}
		out = append(out, fmt.Sprintf(
			"%s opening balance is %.4f LAA but the return filed for %s to %s closed at %.4f — a difference of %.4f LAA. The opening balance here is walked back from what is on hand now, so the two disagreeing means the ledger changed after that return was filed.",
			d.what, d.to, c.PriorPeriodStart, c.PriorPeriodEnd, d.from, d.by))
	}
	if n := c.BackdatedTruncated + int32(len(c.Backdated)); n > 0 {
		explained := ""
		if math.Abs(c.BackdatedNetLaa-c.BulkDiscrepancyLaa) <= continuityToleranceLAA {
			explained = " That accounts for the bulk difference exactly."
		}
		out = append(out, fmt.Sprintf(
			"%d entr%s dated inside that filed period were recorded after it was filed, moving %.4f LAA in total.%s Either amend the filed return or move these to the period they belong in.",
			n, pluralY(n), c.BackdatedNetLaa, explained))
	} else if c.Gap {
		out = append(out, c.GapNote)
	} else {
		out = append(out, "Nothing was recorded against the filed period after it was filed, so the difference is not explained by a late entry. Check that the prior return's closing balance was right.")
	}
	return out
}

// plural is the "es" ending, for words like loss. QA found it appended to
// "destruction" and to "line", producing "destructiones" and "linees" —
// one of them on a B266 filing blocker, which is the last place to look
// careless. A helper that only knows one ending has to be named for it.
func plural(n int32) string {
	if n == 1 {
		return ""
	}
	return "es"
}

// pluralY is the "y"/"ies" ending, for entry. A third of these is a sign
// the next one should be a real inflector, but two words in a filing
// blocker is not the place to introduce one.
func pluralY(n int32) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// pluralS is the ordinary "s" ending.
func pluralS(n int32) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// dayStart drops the time of day, so "days until due" counts whole days
// and does not flip on the hour a report happens to be generated.
func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
