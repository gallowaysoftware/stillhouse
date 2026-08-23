package rpc

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// A legal hold is only a hold if it actually stops a deletion.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestLegalHoldStopsDeletion(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewRetentionService(f.db, testLogger())
	equip := NewEquipmentService(f.db, testLogger())

	made, err := equip.SaveEquipment(f.ctx, connect.NewRequest(
		&stillhousev1.SaveEquipmentRequest{
			Name: "Doomed pump " + uuid.NewString()[:6],
			Kind: stillhousev1.EquipmentKind_EQUIPMENT_KIND_PUMP,
		}))
	if err != nil {
		t.Fatalf("SaveEquipment: %v", err)
	}
	id := made.Msg.GetEquipment().GetId()

	held, err := svc.PlaceLegalHold(f.ctx, connect.NewRequest(
		&stillhousev1.PlaceLegalHoldRequest{
			Reason:       "CRA audit of the 2026 fiscal year",
			InstructedBy: "K. Galloway", Reference: "CRA-2026-0041",
		}))
	if err != nil {
		t.Fatalf("PlaceLegalHold: %v", err)
	}
	holdID := held.Msg.GetHold().GetId()

	t.Run("deletion is refused while the hold is open", func(t *testing.T) {
		_, err := equip.DeleteEquipment(f.ctx, connect.NewRequest(
			&stillhousev1.DeleteEquipmentRequest{Id: id}))
		if err == nil {
			t.Fatal("a row was deleted under a legal hold")
		}
		if !strings.Contains(err.Error(), "legal hold") {
			t.Errorf("the refusal should say what stopped it, got: %v", err)
		}
	})

	t.Run("a hold needs a reason, and so does lifting one", func(t *testing.T) {
		if _, err := svc.PlaceLegalHold(f.ctx, connect.NewRequest(
			&stillhousev1.PlaceLegalHoldRequest{})); err == nil {
			t.Error("a hold with no reason was placed")
		}
		if _, err := svc.ReleaseLegalHold(f.ctx, connect.NewRequest(
			&stillhousev1.ReleaseLegalHoldRequest{Id: holdID})); err == nil {
			t.Error("a hold was lifted with no reason recorded")
		}
	})

	if _, err := svc.ReleaseLegalHold(f.ctx, connect.NewRequest(
		&stillhousev1.ReleaseLegalHoldRequest{
			Id: holdID, Reason: "audit closed, letter on file",
		})); err != nil {
		t.Fatalf("ReleaseLegalHold: %v", err)
	}

	t.Run("and permitted once it is lifted", func(t *testing.T) {
		if _, err := equip.DeleteEquipment(f.ctx, connect.NewRequest(
			&stillhousev1.DeleteEquipmentRequest{Id: id})); err != nil {
			t.Errorf("deletion still refused after the hold was released: %v", err)
		}
	})

	t.Run("releasing twice is refused", func(t *testing.T) {
		if _, err := svc.ReleaseLegalHold(f.ctx, connect.NewRequest(
			&stillhousev1.ReleaseLegalHoldRequest{Id: holdID, Reason: "again"},
		)); err == nil {
			t.Error("a released hold was released again")
		}
	})
}

// The policy is the licensee's, and an unstated one is reported as
// unstated rather than defaulted to six years.
func TestRetentionPolicyIsNotAssumed(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewRetentionService(f.db, testLogger())

	got, err := svc.RetentionStatus(f.ctx, connect.NewRequest(
		&stillhousev1.RetentionStatusRequest{}))
	if err != nil {
		t.Fatalf("RetentionStatus: %v", err)
	}
	if got.Msg.GetPolicy().GetRetentionYearsSet() {
		t.Error("a tenant that has stated no retention window reports one")
	}
	if got.Msg.GetPolicy().GetReviewed() {
		t.Error("an unreviewed policy reports as reviewed")
	}
	if got.Msg.GetBasis() == "" {
		t.Error("it must say what Stillhouse does and does not guarantee")
	}
	// Every class it would be asked for is listed, even empty ones —
	// "we have nothing" is an answer.
	if len(got.Msg.GetCoverage()) == 0 {
		t.Fatal("no record classes reported")
	}
	var sawRemovals bool
	for _, c := range got.Msg.GetCoverage() {
		if c.GetRecordClass() == "Removals" {
			sawRemovals = true
			if c.GetRows() == 0 && c.GetOldest() != "" {
				t.Error("an empty class reported an oldest record")
			}
		}
	}
	if !sawRemovals {
		t.Error("removals are not among the classes reported")
	}

	t.Run("zero years is not a policy", func(t *testing.T) {
		if _, err := svc.SaveRetentionPolicy(f.ctx, connect.NewRequest(
			&stillhousev1.SaveRetentionPolicyRequest{
				RetentionYears: 0, RetentionYearsSet: true,
			})); err == nil {
			t.Error("a retention window of zero years was accepted")
		}
	})

	t.Run("saving without a review date does not re-date the review", func(t *testing.T) {
		if _, err := svc.SaveRetentionPolicy(f.ctx, connect.NewRequest(
			&stillhousev1.SaveRetentionPolicyRequest{
				RetentionYears: 6, RetentionYearsSet: true,
				ReviewedOn:    "2026-01-15",
				BackupCadence: "nightly to the NAS, weekly offsite",
			})); err != nil {
			t.Fatalf("SaveRetentionPolicy: %v", err)
		}
		if _, err := svc.SaveRetentionPolicy(f.ctx, connect.NewRequest(
			&stillhousev1.SaveRetentionPolicyRequest{
				RetentionYears: 7, RetentionYearsSet: true,
			})); err != nil {
			t.Fatalf("SaveRetentionPolicy: %v", err)
		}
		got, err := svc.RetentionStatus(f.ctx, connect.NewRequest(
			&stillhousev1.RetentionStatusRequest{}))
		if err != nil {
			t.Fatalf("RetentionStatus: %v", err)
		}
		p := got.Msg.GetPolicy()
		if got, want := p.GetReviewedOn(), "2026-01-15"; got != want {
			t.Errorf("review date = %s, want %s — editing the window is not a "+
				"review, and silently re-dating one makes the date meaningless",
				got, want)
		}
		if got, want := p.GetRetentionYears(), int32(7); got != want {
			t.Errorf("years = %d, want %d", got, want)
		}
	})
}
