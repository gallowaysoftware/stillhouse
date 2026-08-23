package rpc

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(os.Stderr, nil)) }

// EDM10-1-7 page 3, both halves. A customer's cask in our rackhouse is on
// our return and not on our books; our own parcel at a partner's bonded
// warehouse is on our books and not on our return.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestPossessionDecidesTheReturnAndOwnershipDecidesTheBooks(t *testing.T) {
	f := newLedgerFixture(t)
	bulk := NewBulkService(f.db, testLogger())

	cust := f.salesCustomer(t, sqlcgen.RemovalDestinationKindDutyPaidCustomer)
	ours := f.tank(t, "Ours here "+uuid.NewString()[:6], 1000, 60)     // 600 LAA
	theirs := f.tank(t, "Theirs here "+uuid.NewString()[:6], 500, 60)  // 300 LAA
	away := f.tank(t, "Ours elsewhere "+uuid.NewString()[:6], 200, 60) // 120 LAA

	if _, err := bulk.SetBulkContainerOwner(f.ctx, connect.NewRequest(
		&stillhousev1.SetBulkContainerOwnerRequest{
			Id: theirs.ID.String(), OwnerCustomerId: cust.ID.String(),
		})); err != nil {
		t.Fatalf("SetBulkContainerOwner: %v", err)
	}
	moved, err := bulk.SetBulkContainerPossession(f.ctx, connect.NewRequest(
		&stillhousev1.SetBulkContainerPossessionRequest{
			Id:              away.ID.String(),
			Possession:      stillhousev1.BulkPossession_BULK_POSSESSION_HELD_ELSEWHERE,
			HeldByName:      "Partner Distillery Ltd",
			HeldByLicenceNo: "SL-99999",
			OccurredOn:      "2026-08-10",
			Notes:           "sent for maturation",
		}))
	if err != nil {
		t.Fatalf("SetBulkContainerPossession: %v", err)
	}

	t.Run("leaving writes a reportable in-bond transfer, not a flag", func(t *testing.T) {
		mv := moved.Msg.GetMovement()
		if mv == nil {
			t.Fatal("no movement was written — the B266 walk has nothing to reconcile against")
		}
		if got, want := mv.GetReason(),
			stillhousev1.BulkMovementReason_BULK_MOVEMENT_REASON_TRANSFER_OUT_IN_BOND; got != want {
			t.Errorf("reason = %v, want %v", got, want)
		}
		if got, want := mv.GetLaa(), 120.0; !near(got, want, 1e-6) {
			t.Errorf("movement LAA = %v, want the whole balance %v", got, want)
		}
		if mv.GetCounterpartyName() != "Partner Distillery Ltd" {
			t.Errorf("counterparty = %q", mv.GetCounterpartyName())
		}
	})

	t.Run("the cask keeps its contents", func(t *testing.T) {
		// The movement says the spirits crossed a licensed boundary; the
		// balance says what is in the cask. Both are true of a cask
		// sitting in somebody else's warehouse.
		c, err := f.q.GetBulkContainer(f.ctx, away.ID)
		if err != nil {
			t.Fatalf("re-read: %v", err)
		}
		if got, want := c.CurrentLaa, 120.0; !near(got, want, 1e-6) {
			t.Errorf("balance = %v, want %v", got, want)
		}
		if c.Possession != sqlcgen.BulkPossessionHeldElsewhere {
			t.Errorf("possession = %v", c.Possession)
		}
	})

	t.Run("the two questions get two answers", func(t *testing.T) {
		res, err := bulk.ListBulkContainers(f.ctx, connect.NewRequest(
			&stillhousev1.ListBulkContainersRequest{}))
		if err != nil {
			t.Fatalf("ListBulkContainers: %v", err)
		}
		s := res.Msg.GetSummary()
		// Ours wherever it is: our tank plus our parcel away.
		if got, want := s.GetOwnedLaa(), 720.0; !near(got, want, 1e-6) {
			t.Errorf("owned = %v, want %v", got, want)
		}
		// Here whoever owns it: our tank plus the customer's.
		if got, want := s.GetHeldLaa(), 900.0; !near(got, want, 1e-6) {
			t.Errorf("held = %v, want %v", got, want)
		}
		// What could actually be bottled tomorrow.
		if got, want := s.GetAvailableLaa(), 600.0; !near(got, want, 1e-6) {
			t.Errorf("available = %v, want %v", got, want)
		}
		if got, want := s.GetHeldForOthersLaa(), 300.0; !near(got, want, 1e-6) {
			t.Errorf("held for others = %v, want %v", got, want)
		}
		if got, want := s.GetHeldElsewhereLaa(), 120.0; !near(got, want, 1e-6) {
			t.Errorf("held elsewhere = %v, want %v", got, want)
		}
	})

	t.Run("nothing may be recorded against spirits we do not hold", func(t *testing.T) {
		// You cannot gauge a cask in somebody else's warehouse, and this
		// is also what keeps the B266 walk honest — see assertHeld.
		_, err := bulk.RecordInventoryAdjustment(f.ctx, connect.NewRequest(
			&stillhousev1.RecordInventoryAdjustmentRequest{
				ContainerId:    away.ID.String(),
				CountedVolumeL: 190,
				AbvPct:         60,
				Reason:         stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT,
				Explanation:    "found less",
			}))
		if err == nil {
			t.Fatal("a gauge was recorded against spirits held by somebody else")
		}
		if !strings.Contains(err.Error(), "Partner Distillery Ltd") {
			t.Errorf("the refusal should name the holder, got: %v", err)
		}
	})

	t.Run("the third-party list is what to read before signing", func(t *testing.T) {
		res, err := bulk.ListThirdPartySpirits(f.ctx, connect.NewRequest(
			&stillhousev1.ListThirdPartySpiritsRequest{}))
		if err != nil {
			t.Fatalf("ListThirdPartySpirits: %v", err)
		}
		names := map[string]bool{}
		for _, c := range res.Msg.GetContainers() {
			names[c.GetName()] = true
		}
		if !names[theirs.Name] || !names[away.Name] {
			t.Errorf("list = %v, want both the customer's tank and the one away", names)
		}
		if names[ours.Name] {
			t.Error("a container that is simply ours-and-here should not be on this list")
		}
		if got, want := res.Msg.GetHeldForOthersLaa(), 300.0; !near(got, want, 1e-6) {
			t.Errorf("held for others = %v, want %v", got, want)
		}
	})
}

// The load-bearing arithmetic: the B266 closing balance walks backwards
// from container state through movements, and a change of possession is a
// state transition that produces no movement of its own. It works because
// the transition writes one — and this asserts it, by asking for the
// balance as at a moment before the cask left and after it left.
func TestB266WalkSurvivesAChangeOfPossession(t *testing.T) {
	f := newLedgerFixture(t)
	bulk := NewBulkService(f.db, testLogger())

	before := f.tank(t, "Walk "+uuid.NewString()[:6], 1000, 40) // 400 LAA
	other := f.tank(t, "Stay "+uuid.NewString()[:6], 500, 40)   // 200 LAA
	_ = other

	asOf := func(when time.Time) float64 {
		t.Helper()
		var got float64
		if err := f.db.WithTenantTx(f.ctx, f.tenant.ID,
			func(ctx context.Context, q *sqlcgen.Queries) error {
				var e error
				got, e = q.SumBulkOnHandAsOf(ctx, pgtype.Timestamptz{Valid: true, Time: when})
				return e
			}); err != nil {
			t.Fatalf("SumBulkOnHandAsOf: %v", err)
		}
		return got
	}

	departure := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	beforeDeparture := asOf(departure)

	if _, err := bulk.SetBulkContainerPossession(f.ctx, connect.NewRequest(
		&stillhousev1.SetBulkContainerPossessionRequest{
			Id:         before.ID.String(),
			Possession: stillhousev1.BulkPossession_BULK_POSSESSION_HELD_ELSEWHERE,
			HeldByName: "Partner Distillery Ltd",
			OccurredOn: "2026-08-15",
		})); err != nil {
		t.Fatalf("SetBulkContainerPossession: %v", err)
	}

	// As at the moment before the cask left, the answer must not have
	// moved: it was in our possession then, and a return already filed for
	// that period cannot change because of something that happened later.
	if got := asOf(departure); !near(got, beforeDeparture, 1e-6) {
		t.Errorf("closing balance as at %s changed from %v to %v after a later "+
			"change of possession — a filed return would no longer reconcile",
			departure.Format("2006-01-02"), beforeDeparture, got)
	}

	// And as at now, the cask is gone from the figure.
	now := time.Now().Add(time.Hour)
	if got, want := asOf(now), beforeDeparture-400; !near(got, want, 1e-6) {
		t.Errorf("closing balance now = %v, want %v — the departed cask is still "+
			"being reported", got, want)
	}

	// Bringing it back restores it, and again only from the date it
	// returned.
	if _, err := bulk.SetBulkContainerPossession(f.ctx, connect.NewRequest(
		&stillhousev1.SetBulkContainerPossessionRequest{
			Id:         before.ID.String(),
			Possession: stillhousev1.BulkPossession_BULK_POSSESSION_HELD,
			OccurredOn: time.Now().Format("2006-01-02"),
		})); err != nil {
		t.Fatalf("bring it back: %v", err)
	}
	if got := asOf(departure); !near(got, beforeDeparture, 1e-6) {
		t.Errorf("as at %s = %v, want %v after a round trip",
			departure.Format("2006-01-02"), got, beforeDeparture)
	}
}

func TestPossessionRefusals(t *testing.T) {
	f := newLedgerFixture(t)
	bulk := NewBulkService(f.db, testLogger())
	c := f.tank(t, "Refuse "+uuid.NewString()[:6], 100, 50)

	t.Run("leaving without naming the holder is refused", func(t *testing.T) {
		if _, err := bulk.SetBulkContainerPossession(f.ctx, connect.NewRequest(
			&stillhousev1.SetBulkContainerPossessionRequest{
				Id:         c.ID.String(),
				Possession: stillhousev1.BulkPossession_BULK_POSSESSION_HELD_ELSEWHERE,
			})); err == nil {
			t.Error("stock left the return with nowhere recorded that it went")
		}
	})

	t.Run("saying it is where it already is, is refused", func(t *testing.T) {
		if _, err := bulk.SetBulkContainerPossession(f.ctx, connect.NewRequest(
			&stillhousev1.SetBulkContainerPossessionRequest{
				Id:         c.ID.String(),
				Possession: stillhousev1.BulkPossession_BULK_POSSESSION_HELD,
			})); err == nil {
			t.Error("a no-op transition wrote a movement onto the return")
		}
	})

	t.Run("an unknown customer cannot own a cask", func(t *testing.T) {
		if _, err := bulk.SetBulkContainerOwner(f.ctx, connect.NewRequest(
			&stillhousev1.SetBulkContainerOwnerRequest{
				Id: c.ID.String(), OwnerCustomerId: uuid.NewString(),
			})); err == nil {
			t.Error("a cask was assigned to a customer that does not exist")
		}
	})

	t.Run("an empty container changing hands writes no movement", func(t *testing.T) {
		empty := f.tank(t, "Empty "+uuid.NewString()[:6], 0, 0)
		res, err := bulk.SetBulkContainerPossession(f.ctx, connect.NewRequest(
			&stillhousev1.SetBulkContainerPossessionRequest{
				Id:         empty.ID.String(),
				Possession: stillhousev1.BulkPossession_BULK_POSSESSION_HELD_ELSEWHERE,
				HeldByName: "Partner Distillery Ltd",
			}))
		if err != nil {
			t.Fatalf("SetBulkContainerPossession: %v", err)
		}
		if res.Msg.GetMovement() != nil {
			t.Error("an empty cask changing hands put a zero-LAA in-bond transfer " +
				"line on the return, saying nothing crossed")
		}
	})
}
