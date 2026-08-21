package rpc

import (
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
	bulkClosingLAA        float64
	packagedClosingLAA    float64
	packagedClosingBottle int32

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
}

// laa returns the LAA summed against a bulk movement reason, or zero if
// the period saw no movement of that kind.
func (t b266Totals) laa(reason string) float64 { return t.byReason[reason] }

// projectB266 turns gathered totals into the return. Pure: same inputs,
// same report, no clock and no database.
func projectB266(t b266Totals, periodStart, periodEnd, generatedAt time.Time) *stillhousev1.B266Report {
	report := &stillhousev1.B266Report{
		PeriodStart: periodStart.Format("2006-01-02"),
		PeriodEnd:   periodEnd.Format("2006-01-02"),

		BulkProductionLaa:             t.laa("production_gauge"),
		BulkReceivedInBondLaa:         t.laa("transfer_in_bond"),
		BulkBlendInLaa:                t.laa("blend"),
		BulkTransferredToPackagingLaa: t.laa("transfer_to_packaging"),
		BulkTransferredOutInBondLaa:   t.laa("transfer_out_in_bond"),
		BulkLossesLaa:                 t.laa("loss_evaporation") + t.laa("loss_unaccounted"),
		BulkDestroyedLaa:              t.laa("destruction"),
		BulkClosingLaa:                round4(t.bulkClosingLAA),
		// Adopted stock is reported but deliberately NOT counted among the
		// receipts below, so the reverse-walk puts it in the opening
		// balance. It was in the warehouse before the period; only the
		// bookkeeping is new. Counting it as a receipt would overstate what
		// the distillery made, on a return CRA reads.
		BulkOpeningInventoryAdoptedLaa: round4(t.laa("opening_inventory")),

		BulkAdjustmentsLaa:         round4(t.adjustmentsNetLAA),
		BulkAdjustmentsIncreaseLaa: round4(t.adjustmentsIncreaseLAA),
		BulkAdjustmentsDecreaseLaa: round4(t.adjustmentsDecreaseLAA),
		BulkAdjustmentsCount:       t.adjustmentsCount,

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
		DutyPayableCad:         round2cents(t.packagedDutyCAD + t.removedDutyCAD),
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
		report.BulkAdjustmentsIncreaseLaa
	bulkWithdrawals := report.BulkTransferredToPackagingLaa + report.BulkTransferredOutInBondLaa +
		report.BulkLossesLaa + report.BulkDestroyedLaa + report.BulkAdjustmentsDecreaseLaa
	report.BulkOpeningLaa = round4(report.BulkClosingLaa - bulkReceipts + bulkWithdrawals)

	// Packaged inventory only ever received what became bottles. Walking
	// back with the tank-gauge figure instead — which includes what was
	// spilled on the way — subtracts alcohol that never arrived, and a
	// first-ever return came out with a negative opening balance.
	report.PackagedOpeningLaa = round4(report.PackagedClosingLaa - report.PackagedPackagedLaa + report.PackagedRemovedDutyPaidLaa)

	return report
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
