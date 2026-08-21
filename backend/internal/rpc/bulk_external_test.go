package rpc

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// PLAN A3. Against EDM10-1-7 the B266's bulk section was missing most of
// page 3 — imports, receipts from other spirits licensees and licensed
// users, packaged spirits returned to bulk, deliveries out, denaturing to
// DA and SDA, exports, and bulk returned to production.
//
// It was worse than missing lines. Nothing in the application ever created
// a transfer_in_bond, transfer_out_in_bond, destruction or loss_unaccounted
// movement either: the report has had lines for all four since it was
// written and they were structurally always zero.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

func newExternalFixture(t *testing.T) (*dutyFixture, *BulkService) {
	t.Helper()
	f := newDutyFixture(t)
	return f, NewBulkService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

// Every reportable kind round-trips: it is accepted, it moves the balance
// the right way, and it lands on its own B266 line. Before this, ten of
// these had no path and four more had a line but no way to reach it.
func TestEveryReportableExternalMovementHasAPath(t *testing.T) {
	kinds := []struct {
		kind    stillhousev1.BulkExternalMovementKind
		inbound bool
		party   string
		line    func(*stillhousev1.B266Report) float64
	}{
		{stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_IMPORT, true, "Islay Distillers Ltd",
			func(r *stillhousev1.B266Report) float64 { return r.GetBulkImportedLaa() }},
		{stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_IN_BOND_IN, true, "Okanagan Spirits",
			func(r *stillhousev1.B266Report) float64 { return r.GetBulkReceivedInBondLaa() }},
		{stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_FROM_SPIRITS_LICENSEE, true, "Dillon's",
			func(r *stillhousev1.B266Report) float64 { return r.GetBulkReceivedFromLicenseeLaa() }},
		{stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_FROM_LICENSED_USER, true, "A Licensed User",
			func(r *stillhousev1.B266Report) float64 { return r.GetBulkReceivedFromLicensedUserLaa() }},

		{stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_IN_BOND_OUT, false, "Last Mountain",
			func(r *stillhousev1.B266Report) float64 { return r.GetBulkTransferredOutInBondLaa() }},
		{stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_TO_SPIRITS_LICENSEE, false, "Eau Claire",
			func(r *stillhousev1.B266Report) float64 { return r.GetBulkDeliveredToLicenseeLaa() }},
		{stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_TO_LICENSED_USER, false, "A Licensed User",
			func(r *stillhousev1.B266Report) float64 { return r.GetBulkDeliveredToLicensedUserLaa() }},
		{stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_EXPORT, false, "Importer SARL",
			func(r *stillhousev1.B266Report) float64 { return r.GetBulkExportedLaa() }},
		{stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_DENATURED_DA, false, "",
			func(r *stillhousev1.B266Report) float64 { return r.GetBulkDenaturedDaLaa() }},
		{stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_DENATURED_SDA, false, "",
			func(r *stillhousev1.B266Report) float64 { return r.GetBulkDenaturedSdaLaa() }},
		{stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_RETURNED_TO_PRODUCTION, false, "",
			func(r *stillhousev1.B266Report) float64 { return r.GetBulkReturnedToProductionLaa() }},
		{stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_DESTRUCTION, false, "",
			func(r *stillhousev1.B266Report) float64 { return r.GetBulkDestroyedLaa() }},
		{stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_UNACCOUNTED_LOSS, false, "",
			func(r *stillhousev1.B266Report) float64 { return r.GetBulkLossesLaa() }},
	}

	when := timestamppb.New(time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC))
	for _, tc := range kinds {
		t.Run(tc.kind.String(), func(t *testing.T) {
			f, svc := newExternalFixture(t)
			b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
			tank := f.tank(t, "External tank", 1000, 60) // 600 LAA
			beforeVol, beforeLAA := f.balance(t, tank.ID)

			resp, err := svc.RecordBulkExternalMovement(f.ctx, connect.NewRequest(&stillhousev1.RecordBulkExternalMovementRequest{
				ContainerId: tank.ID.String(), Kind: tc.kind,
				VolumeL: 100, AbvPct: 60,
				CounterpartyName: tc.party, CounterpartyLicenceNo: "L63A-0001",
				DocumentReference: "DOC-1", OccurredAt: when,
			}))
			if err != nil {
				t.Fatalf("RecordBulkExternalMovement: %v", err)
			}

			afterVol, afterLAA := f.balance(t, tank.ID)
			wantVol, wantLAA := beforeVol-100, beforeLAA-60
			if tc.inbound {
				wantVol, wantLAA = beforeVol+100, beforeLAA+60
			}
			if !near(afterVol, wantVol, 1e-6) || !near(afterLAA, wantLAA, 1e-6) {
				t.Errorf("balance: got %v L / %v LAA, want %v / %v", afterVol, afterLAA, wantVol, wantLAA)
			}
			// The counterparty and the document travel with the row.
			if got := resp.Msg.GetMovement().GetDocumentReference(); got != "DOC-1" {
				t.Errorf("document reference: got %q, want DOC-1", got)
			}
			if tc.party != "" && resp.Msg.GetMovement().GetCounterpartyName() != tc.party {
				t.Errorf("counterparty: got %q, want %q",
					resp.Msg.GetMovement().GetCounterpartyName(), tc.party)
			}
			// And the reason maps to something on the wire, rather than
			// showing as an unspecified movement.
			if resp.Msg.GetMovement().GetReason() ==
				stillhousev1.BulkMovementReason_BULK_MOVEMENT_REASON_UNSPECIFIED {
				t.Error("the movement's reason came back unspecified")
			}

			rep, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
				PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30",
			}))
			if err != nil {
				t.Fatalf("GenerateB266: %v", err)
			}
			r := rep.Msg.GetReport()
			if got := tc.line(r); !near(got, 60, 1e-6) {
				t.Errorf("the B266 line for this movement reads %v, want 60", got)
			}
			// The books close with the new lines in the walk.
			assertBulkBooksClose(t, r)
		})
	}
}

// assertBulkBooksClose is the identity an auditor checks first, over the
// full page-3 vocabulary.
func assertBulkBooksClose(t *testing.T, r *stillhousev1.B266Report) {
	t.Helper()
	receipts := r.GetBulkProductionLaa() + r.GetBulkReceivedInBondLaa() +
		r.GetBulkAdjustmentsIncreaseLaa() + r.GetBulkImportedLaa() +
		r.GetBulkReceivedFromLicenseeLaa() + r.GetBulkReceivedFromLicensedUserLaa() +
		r.GetBulkPackagedReturnedToBulkLaa()
	withdrawals := r.GetBulkTransferredToPackagingLaa() + r.GetBulkTransferredOutInBondLaa() +
		r.GetBulkLossesLaa() + r.GetBulkDestroyedLaa() + r.GetBulkAdjustmentsDecreaseLaa() +
		r.GetBulkDeliveredToLicenseeLaa() + r.GetBulkDeliveredToLicensedUserLaa() +
		r.GetBulkExportedLaa() + r.GetBulkDenaturedDaLaa() + r.GetBulkDenaturedSdaLaa() +
		r.GetBulkReturnedToProductionLaa()
	if got := r.GetBulkOpeningLaa() + receipts - withdrawals; !near(got, r.GetBulkClosingLaa(), 1e-6) {
		t.Errorf("bulk books don't close: opening %v + receipts %v - withdrawals %v = %v, closing %v",
			r.GetBulkOpeningLaa(), receipts, withdrawals, got, r.GetBulkClosingLaa())
	}
}

// A quantity with no counterparty cannot be reconciled against the other
// party's return, which is the point of these lines being reportable at
// both ends.
func TestExternalMovementRequiresACounterpartyWhereTheFormDoes(t *testing.T) {
	f, svc := newExternalFixture(t)
	tank := f.tank(t, "Anonymous tank", 1000, 60)

	_, err := svc.RecordBulkExternalMovement(f.ctx, connect.NewRequest(&stillhousev1.RecordBulkExternalMovementRequest{
		ContainerId: tank.ID.String(),
		Kind:        stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_IN_BOND_OUT,
		VolumeL:     100, AbvPct: 60,
	}))
	if err == nil {
		t.Fatal("an in-bond transfer out was accepted with no counterparty")
	}
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want invalid_argument (err: %v)", got, err)
	}

	// Denaturing has no counterparty, and must not demand one.
	if _, err := svc.RecordBulkExternalMovement(f.ctx, connect.NewRequest(&stillhousev1.RecordBulkExternalMovementRequest{
		ContainerId: tank.ID.String(),
		Kind:        stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_DENATURED_SDA,
		VolumeL:     100, AbvPct: 60,
	})); err != nil {
		t.Errorf("denaturing demanded a counterparty: %v", err)
	}
}

// You cannot ship, denature or destroy alcohol that is not there.
func TestExternalDispositionCannotOverdrawTheContainer(t *testing.T) {
	f, svc := newExternalFixture(t)
	tank := f.tank(t, "Shallow tank", 100, 60) // 60 LAA

	_, err := svc.RecordBulkExternalMovement(f.ctx, connect.NewRequest(&stillhousev1.RecordBulkExternalMovementRequest{
		ContainerId: tank.ID.String(),
		Kind:        stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_EXPORT,
		VolumeL:     500, AbvPct: 60, CounterpartyName: "Importer SARL",
	}))
	if err == nil {
		t.Fatal("500 L was exported out of a tank holding 100")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
	}
	if vol, _ := f.balance(t, tank.ID); !near(vol, 100, 1e-9) {
		t.Errorf("a refused disposition moved the balance: %v L", vol)
	}
}

// Packaged spirits returned to bulk take their quantity from the bottles,
// not from a typed volume. Letting the two disagree would credit bulk with
// more than packaged gave up — LAA created out of a keystroke.
func TestPackagedReturnedToBulkTakesItsQuantityFromTheBottles(t *testing.T) {
	f, svc := newExternalFixture(t)
	f.stamps(t, "CA-ON", 1000)
	source := f.tank(t, "Unpackage source", 2000, 70)
	dest := f.tank(t, "Unpackage destination", 0, 0)
	prod := f.product(t, "Unpackaged Gin", 750, 40)

	bottled, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId: prod.ID.String(), SourceContainerId: source.ID.String(),
		DestinationJurisdiction: "CA-ON", BottleCount: 400,
		LotCode: "UNPK-" + uuid.NewString()[:8],
	}))
	if err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}
	piID := bottled.Msg.GetPackaged().GetId()

	// The typed volume is deliberately wrong; the bottles are what counts.
	resp, err := svc.RecordBulkExternalMovement(f.ctx, connect.NewRequest(&stillhousev1.RecordBulkExternalMovementRequest{
		ContainerId: dest.ID.String(),
		Kind:        stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_PACKAGED_RETURNED_TO_BULK,
		VolumeL:     9999, AbvPct: 99,
		PackagedInventoryId: piID, BottlesUnpackaged: 100,
	}))
	if err != nil {
		t.Fatalf("RecordBulkExternalMovement: %v", err)
	}

	// 100 × 750 mL at 40% = 75 L, 30 LAA.
	if got := resp.Msg.GetMovement().GetLaa(); !near(got, 30, 1e-6) {
		t.Errorf("LAA returned to bulk: got %v, want 30 (from the bottles, not the typed volume)", got)
	}
	if vol, laa := f.balance(t, dest.ID); !near(vol, 75, 1e-6) || !near(laa, 30, 1e-6) {
		t.Errorf("destination: got %v L / %v LAA, want 75 / 30", vol, laa)
	}
	// And packaged inventory gave up exactly those bottles.
	if got, want := f.lot(t, uuid.MustParse(piID)).BottlesOnHand, int32(300); got != want {
		t.Errorf("bottles on hand: got %d, want %d", got, want)
	}
}

// Unpackaging more bottles than the lot holds is refused, and the lock is
// the same one removals take (stage 140).
func TestPackagedReturnedToBulkCannotOverdrawTheLot(t *testing.T) {
	f, svc := newExternalFixture(t)
	f.stamps(t, "CA-ON", 1000)
	source := f.tank(t, "Overdraw source", 2000, 70)
	dest := f.tank(t, "Overdraw destination", 0, 0)
	prod := f.product(t, "Overdrawn Gin", 750, 40)

	bottled, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId: prod.ID.String(), SourceContainerId: source.ID.String(),
		DestinationJurisdiction: "CA-ON", BottleCount: 50,
		LotCode: "OVR-" + uuid.NewString()[:8],
	}))
	if err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}
	_, err = svc.RecordBulkExternalMovement(f.ctx, connect.NewRequest(&stillhousev1.RecordBulkExternalMovementRequest{
		ContainerId: dest.ID.String(),
		Kind:        stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_PACKAGED_RETURNED_TO_BULK,
		VolumeL:     1, AbvPct: 40,
		PackagedInventoryId: bottled.Msg.GetPackaged().GetId(), BottlesUnpackaged: 51,
	}))
	if err == nil {
		t.Fatal("51 bottles were unpackaged from a lot of 50")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
	}
	if vol, _ := f.balance(t, dest.ID); !near(vol, 0, 1e-9) {
		t.Errorf("a refused unpackaging credited the destination: %v L", vol)
	}
}

// An external movement is a determination: receiving spirit in bond means
// gauging it, and that gauge is subject to the same instrument approval as
// any other (EDM3-1-1 ¶24).
func TestExternalMovementChecksItsInstruments(t *testing.T) {
	f, svc := newExternalFixture(t)
	instSvc := NewInstrumentService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tank := f.tank(t, "Instrumented tank", 1000, 60)

	unapproved, err := instSvc.CreateInstrument(f.ctx, connect.NewRequest(&stillhousev1.CreateInstrumentRequest{
		Kind:  stillhousev1.InstrumentKind_INSTRUMENT_KIND_HYDROMETER,
		Label: "Unapproved receiving hydro", SerialNo: "EXT-" + uuid.NewString()[:8],
	}))
	if err != nil {
		t.Fatalf("CreateInstrument: %v", err)
	}

	_, err = svc.RecordBulkExternalMovement(f.ctx, connect.NewRequest(&stillhousev1.RecordBulkExternalMovementRequest{
		ContainerId: tank.ID.String(),
		Kind:        stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_IN_BOND_IN,
		VolumeL:     100, AbvPct: 60, CounterpartyName: "Okanagan Spirits",
		Instruments: &stillhousev1.InstrumentRefs{
			StrengthInstrumentId: unapproved.Msg.GetInstrument().GetId(),
		},
	}))
	if err == nil {
		t.Fatal("a receiving gauge was accepted against an unapproved instrument")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
	}
}

// Every ledger reason must map to a wire value. The switch this replaced
// had already drifted — opening_inventory (stage 124) was never added, so
// adopted stock displayed in the UI as an unspecified movement — and the
// ten reasons added for page 3 would have drifted the same way.
func TestEveryMovementReasonMapsToTheWire(t *testing.T) {
	for _, r := range []sqlcgen.BulkMovementReason{
		sqlcgen.BulkMovementReasonProductionGauge,
		sqlcgen.BulkMovementReasonInterTankTransfer,
		sqlcgen.BulkMovementReasonBlend,
		sqlcgen.BulkMovementReasonTransferInBond,
		sqlcgen.BulkMovementReasonTransferOutInBond,
		sqlcgen.BulkMovementReasonTransferToPackaging,
		sqlcgen.BulkMovementReasonLossEvaporation,
		sqlcgen.BulkMovementReasonLossUnaccounted,
		sqlcgen.BulkMovementReasonRegaugeCorrection,
		sqlcgen.BulkMovementReasonDestruction,
		sqlcgen.BulkMovementReasonOpeningInventory,
		sqlcgen.BulkMovementReasonAdjustmentIncrease,
		sqlcgen.BulkMovementReasonAdjustmentDecrease,
		sqlcgen.BulkMovementReasonImportReceived,
		sqlcgen.BulkMovementReasonReceivedFromSpiritsLicensee,
		sqlcgen.BulkMovementReasonReceivedFromLicensedUser,
		sqlcgen.BulkMovementReasonPackagedReturnedToBulk,
		sqlcgen.BulkMovementReasonDeliveredToSpiritsLicensee,
		sqlcgen.BulkMovementReasonDeliveredToLicensedUser,
		sqlcgen.BulkMovementReasonExported,
		sqlcgen.BulkMovementReasonDenaturedDa,
		sqlcgen.BulkMovementReasonDenaturedSda,
		sqlcgen.BulkMovementReasonReturnedToProduction,
	} {
		if got := bulkMovementReasonToProto(r); got ==
			stillhousev1.BulkMovementReason_BULK_MOVEMENT_REASON_UNSPECIFIED {
			t.Errorf("%q has no wire value — it will display as an unspecified movement", r)
		}
	}
	// And no two reasons collide on one wire value, which would make two
	// different lines on the return indistinguishable in the UI.
	seen := map[stillhousev1.BulkMovementReason]sqlcgen.BulkMovementReason{}
	for r, p := range bulkMovementReasonProto {
		if prev, dup := seen[p]; dup {
			t.Errorf("%q and %q both map to %v", prev, r, p)
		}
		seen[p] = r
	}
}
