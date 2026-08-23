package rpc

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The register, and the rule that runs through it: a figure nobody
// recorded is absent rather than zero, and what depends on it says so
// instead of assuming.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestEquipmentRegister(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewEquipmentService(f.db, testLogger())

	still, err := svc.SaveEquipment(f.ctx, connect.NewRequest(
		&stillhousev1.SaveEquipmentRequest{
			Name:         "Wash still " + uuid.NewString()[:6],
			Kind:         stillhousev1.EquipmentKind_EQUIPMENT_KIND_STILL,
			Manufacturer: "Forsyths", Model: "1000L pot",
			CapacityL: 1000, CapacityLSet: true,
			ServiceIntervalDays: 180, ServiceIntervalDaysSet: true,
			CommissionedOn: "2020-01-01",
		}))
	if err != nil {
		t.Fatalf("SaveEquipment: %v", err)
	}
	stillID := still.Msg.GetEquipment().GetId()

	t.Run("a capacity of zero is not a capacity", func(t *testing.T) {
		_, err := svc.SaveEquipment(f.ctx, connect.NewRequest(
			&stillhousev1.SaveEquipmentRequest{
				Name:      "Zero " + uuid.NewString()[:6],
				Kind:      stillhousev1.EquipmentKind_EQUIPMENT_KIND_PUMP,
				CapacityL: 0, CapacityLSet: true,
			}))
		if err == nil {
			t.Fatal("a capacity of zero was accepted")
		}
		if !strings.Contains(err.Error(), "blank") {
			t.Errorf("the refusal should say to leave it blank, got: %v", err)
		}
	})

	t.Run("no interval means never due", func(t *testing.T) {
		// A service schedule Stillhouse invented is one nobody agreed to.
		bare, err := svc.SaveEquipment(f.ctx, connect.NewRequest(
			&stillhousev1.SaveEquipmentRequest{
				Name:           "Old pump " + uuid.NewString()[:6],
				Kind:           stillhousev1.EquipmentKind_EQUIPMENT_KIND_PUMP,
				CommissionedOn: "2005-01-01",
			}))
		if err != nil {
			t.Fatalf("SaveEquipment: %v", err)
		}
		if bare.Msg.GetEquipment().GetServiceDue() {
			t.Error("a pump with no recorded interval was called due for service")
		}
		if bare.Msg.GetEquipment().GetCapacityLSet() {
			t.Error("an unrecorded capacity reads as set")
		}
	})

	t.Run("a commissioned still past its interval is due", func(t *testing.T) {
		got, err := svc.ListEquipment(f.ctx, connect.NewRequest(
			&stillhousev1.ListEquipmentRequest{}))
		if err != nil {
			t.Fatalf("ListEquipment: %v", err)
		}
		var found *stillhousev1.Equipment
		for _, e := range got.Msg.GetEquipment() {
			if e.GetId() == stillID {
				found = e
			}
		}
		if found == nil {
			t.Fatal("the still is missing from the register")
		}
		if !found.GetServiceDue() {
			t.Errorf("commissioned in 2020 on a 180-day interval and never "+
				"serviced, but not due (%d days since)", found.GetDaysSinceService())
		}
		if got.Msg.GetServiceDue() < 1 {
			t.Error("the summary does not count it")
		}
		// The pump above has no capacity, and the register says how many
		// such items there are because scheduling cannot use them.
		if got.Msg.GetCapacityUnknown() < 1 {
			t.Error("an item with no recorded capacity is not counted as unknown")
		}
	})

	t.Run("servicing it clears the flag", func(t *testing.T) {
		if _, err := svc.RecordService(f.ctx, connect.NewRequest(
			&stillhousev1.RecordServiceRequest{
				EquipmentId: stillID,
				Description: "Descaled and reseated the swan neck",
				PerformedBy: "Forsyths", CostCad: "1450.00",
			})); err != nil {
			t.Fatalf("RecordService: %v", err)
		}
		got, err := svc.GetEquipment(f.ctx, connect.NewRequest(
			&stillhousev1.GetEquipmentRequest{Id: stillID}))
		if err != nil {
			t.Fatalf("GetEquipment: %v", err)
		}
		if got.Msg.GetEquipment().GetServiceDue() {
			t.Error("still due after being serviced today")
		}
		if len(got.Msg.GetServices()) != 1 {
			t.Fatalf("%d service records, want 1", len(got.Msg.GetServices()))
		}
		if got, want := got.Msg.GetServices()[0].GetCostCad(), "1450.00"; got != want {
			t.Errorf("cost = %s, want %s", got, want)
		}
	})

	t.Run("a service record needs to say what was done", func(t *testing.T) {
		if _, err := svc.RecordService(f.ctx, connect.NewRequest(
			&stillhousev1.RecordServiceRequest{EquipmentId: stillID})); err == nil {
			t.Error("a service record with no description was accepted")
		}
	})

	t.Run("retiring a still needs a reason", func(t *testing.T) {
		if _, err := svc.SaveEquipment(f.ctx, connect.NewRequest(
			&stillhousev1.SaveEquipmentRequest{
				Id: stillID, Name: "Wash still",
				Kind:   stillhousev1.EquipmentKind_EQUIPMENT_KIND_STILL,
				Status: stillhousev1.EquipmentStatus_EQUIPMENT_STATUS_RETIRED,
			})); err == nil {
			t.Error("a still left service with no reason recorded")
		}
	})

	t.Run("two things cannot share a name", func(t *testing.T) {
		name := "Duplicate " + uuid.NewString()[:6]
		mk := func() error {
			_, err := svc.SaveEquipment(f.ctx, connect.NewRequest(
				&stillhousev1.SaveEquipmentRequest{
					Name: name, Kind: stillhousev1.EquipmentKind_EQUIPMENT_KIND_PUMP,
				}))
			return err
		}
		if err := mk(); err != nil {
			t.Fatalf("first: %v", err)
		}
		err := mk()
		if err == nil {
			t.Fatal("two pieces of equipment share a name")
		}
		if connect.CodeOf(err) != connect.CodeAlreadyExists {
			t.Errorf("code = %v, want already_exists", connect.CodeOf(err))
		}
	})
}
