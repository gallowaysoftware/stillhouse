package rpc

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/excise"
)

// removalInput is one removal about to be written, already parsed and
// validated by whichever caller assembled it.
type removalInput struct {
	PackagedInventoryID uuid.UUID
	Bottles             int32
	RemovalDate         pgtype.Date
	CustomerID          uuid.NullUUID
	// DestinationKind is consulted only when CustomerID is unset — a named
	// customer carries its own classification. See recordRemoval.
	DestinationKind sqlcgen.RemovalDestinationKind
	DestinationName string
	Reference       string
	Notes           string
}

// removalOutcome is what the caller needs to render or link the result.
type removalOutcome struct {
	Removal           sqlcgen.PackagingRemoval
	Product           sqlcgen.Product
	Package           sqlcgen.PackagedInventory
	DutiedAtPackaging bool
}

// recordRemoval writes one removal: the period check, the release gate, the
// stock decrement, the duty decision, the number allocation and the audit
// entry. It runs inside a caller-supplied transaction and does no parsing.
//
// It exists because there are now two ways a removal comes into being — an
// operator recording one directly, and a shipment being marked shipped — and
// they must produce the same row. Not merely a similar one: the same duty
// basis, the same locking, the same audit shape, the same refusals. A second
// implementation would drift on the first change to either, and the direction
// it would drift is under-reported duty on the return.
func recordRemoval(
	ctx context.Context,
	q *sqlcgen.Queries,
	tenantID, userID uuid.UUID,
	in removalInput,
) (removalOutcome, error) {
	var out removalOutcome
	if in.Bottles <= 0 {
		return out, connect.NewError(connect.CodeInvalidArgument, errors.New("bottles_removed must be > 0"))
	}
	if e := assertDateNotInLockedPeriod(ctx, q, in.RemovalDate); e != nil {
		return out, e
	}

	dest := in.DestinationKind
	destName := in.DestinationName
	if in.CustomerID.Valid {
		cust, e := q.GetCustomer(ctx, in.CustomerID.UUID)
		if e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return out, connect.NewError(connect.CodeNotFound, errors.New("customer not found"))
			}
			return out, e
		}
		if cust.ArchivedAt.Valid {
			return out, connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("%s is archived; un-archive them or name a destination", cust.Name))
		}
		dest = sqlcgen.RemovalDestinationKind(cust.DefaultDestinationKind)
		// Copied, not joined, so the B266 and the audit trail read
		// identically whether or not a removal named a customer, and
		// no report has to know which era a row came from.
		destName = cust.Name
	}

	// Read the lot with the intent to change it. FOR UPDATE is what
	// makes the stock check below mean anything: without it two
	// removals against the same lot both read the same on-hand count,
	// both decide there are enough bottles, and both decrement. The
	// table CHECK caught the resulting negative and turned it into an
	// opaque error, which is the same lost update fixed for bulk
	// containers in stage 131 — two people on two tablets is the
	// ordinary case at any distillery with staff.
	matched, e := q.GetPackagedInventoryForUpdate(ctx, in.PackagedInventoryID)
	if e != nil {
		return out, e
	}
	// The release gate. Opt-in per tenant, because a one-person
	// distillery that signs off by looking at the bottle should not
	// be blocked by a workflow built for a QA department — and a
	// system that forces the ceremony gets the ceremony performed
	// rather than meant.
	//
	// A hold blocks regardless of the setting. Holding a lot is an
	// explicit act by a named person saying this stock must not
	// leave, and honouring it only when a tenant flag happens to be
	// on would make the act meaningless.
	tenant, e := q.GetTenantByID(ctx, tenantID)
	if e != nil {
		return out, e
	}
	if matched.HeldAt.Valid {
		reason := matched.HoldReason
		if reason == "" {
			reason = "no reason recorded"
		}
		return out, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("this lot is on hold: %s", reason))
	}
	if tenant.RequireBatchRelease && !matched.ReleasedAt.Valid {
		return out, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("this lot has not been released for sale — release it from the "+
				"bottling run, or turn off the release requirement in settings"))
	}
	if matched.BottlesOnHand < in.Bottles {
		return out, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("only %d bottles on hand", matched.BottlesOnHand))
	}
	product, e := q.GetProduct(ctx, matched.ProductID)
	if e != nil {
		return out, e
	}

	totalLitres := float64(in.Bottles) * float64(product.BottleSizeMl) / 1000
	totalLAA := totalLitres * product.TargetAbvPct / 100
	// Duty here only if this stock was not already dutied when it was
	// packaged.
	//
	// The test is the bottling run's own duty amount rather than a
	// date comparison, so the two sides can never drift: if the run
	// crystallised duty, the removal carries none, and if it did not,
	// the removal carries it. Across a duty-point cutover both cases
	// coexist — stock bottled under the old basis is still dutied on
	// its way out — and no litre is dutied twice or dutied never.
	//
	// A lot with no bottling run behind it (adopted stock, backfill)
	// counts as not dutied, which is the direction that cannot
	// under-report.
	dutiedAtPackaging := false
	if matched.BottlingRunID.Valid {
		run, re := q.GetBottlingRun(ctx, matched.BottlingRunID.UUID)
		if re != nil && !errors.Is(re, pgx.ErrNoRows) {
			return out, re
		}
		dutiedAtPackaging = re == nil && run.DutyAmountCad.Valid
	}

	var ratePerLAA, dutyCAD float64
	if !dutiedAtPackaging {
		// The rate is the one in force on the removal date, not
		// today's. A date the table cannot source refuses rather than
		// being priced at whatever the current band happens to be —
		// see internal/excise.
		ratePerLAA, dutyCAD, e = excise.Owed(in.RemovalDate.Time, totalLitres, product.TargetAbvPct)
		if e != nil {
			return out, asRateRefusal(e)
		}
	}

	// Serialise number allocation before reading the maximum — see
	// LockDocumentSequence. Without it two removals started at the
	// same moment claim the same removal_no and one dies on the
	// UNIQUE constraint with the shipment unrecorded.
	if e := q.LockDocumentSequence(ctx, "packaging_removals"); e != nil {
		return out, e
	}
	nextNo, e := q.NextRemovalNo(ctx)
	if e != nil {
		return out, e
	}
	removal, e := q.CreateRemoval(ctx, sqlcgen.CreateRemovalParams{
		TenantID:            tenantID,
		RemovalNo:           nextNo,
		PackagedInventoryID: in.PackagedInventoryID,
		RemovalDate:         in.RemovalDate,
		BottlesRemoved:      in.Bottles,
		DestinationKind:     dest,
		DestinationName:     destName,
		CustomerID:          in.CustomerID,
		Reference:           in.Reference,
		BottleSizeMl:        product.BottleSizeMl,
		BottleAbvPct:        product.TargetAbvPct,
		TotalLitres:         totalLitres,
		TotalLaa:            totalLAA,
		DutyRatePerLaa:      ratePerLAA,
		DutyAmountCad:       dutyCAD,
		Notes:               in.Notes,
	})
	if e != nil {
		return out, e
	}
	pkg, e := q.DecrementPackagedOnHand(ctx, sqlcgen.DecrementPackagedOnHandParams{
		ID:            in.PackagedInventoryID,
		BottlesOnHand: in.Bottles,
	})
	if errors.Is(e, pgx.ErrNoRows) {
		// Unreachable while the lock above is held — kept so that a
		// future caller that forgets the lock fails with a sentence an
		// operator can act on rather than a 500.
		return out, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("those bottles are no longer on hand — someone else removed them; reload and try again"))
	}
	if e != nil {
		return out, e
	}
	if e := audit.Write(ctx, q, tenantID, userID, "removal", removal.ID.String(),
		sqlcgen.AuditActionCreate, map[string]any{
			"removal_no":   removal.RemovalNo,
			"product_name": product.Name,
			"lot_code":     pkg.LotCode,
			"jurisdiction": pkg.Jurisdiction,
			"bottles":      removal.BottlesRemoved,
			"total_laa":    removal.TotalLaa,
			"duty_cad":     removal.DutyAmountCad,
			// Zero duty on a removal is a fact about the duty point,
			// not an omission — say which, so the trail explains it.
			"dutied_at_packaging": dutiedAtPackaging,
			"destination":         removal.DestinationName,
			"customer_id":         nullUUIDString(removal.CustomerID),
		}); e != nil {
		return out, e
	}
	return removalOutcome{
		Removal:           removal,
		Product:           product,
		Package:           pkg,
		DutiedAtPackaging: dutiedAtPackaging,
	}, nil
}
