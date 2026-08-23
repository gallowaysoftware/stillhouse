package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// A location is where something is, not who owns it. The properties that
// matter: every tenant starts with exactly one, there is always exactly
// one default, and moving a cask between premises moves no alcohol.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestLocations(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewLocationService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tag := uuid.NewString()[:8]

	list := func(t *testing.T) []*stillhousev1.Location {
		t.Helper()
		resp, err := svc.ListLocations(f.ctx, connect.NewRequest(&stillhousev1.ListLocationsRequest{}))
		if err != nil {
			t.Fatalf("ListLocations: %v", err)
		}
		return resp.Msg.GetLocations()
	}

	t.Run("a new tenant already has one, and it is the default", func(t *testing.T) {
		// Backfilled by the migration and by tenant creation, so nothing
		// downstream has to handle "no location at all" as a case.
		got := list(t)
		if len(got) != 1 {
			t.Fatalf("a fresh tenant has %d locations, want 1", len(got))
		}
		if !got[0].GetIsDefault() {
			t.Error("the only location is not the default")
		}
	})

	t.Run("exactly one default, always", func(t *testing.T) {
		if _, err := svc.SaveLocation(f.ctx, connect.NewRequest(&stillhousev1.SaveLocationRequest{
			Name: "Rackhouse " + tag, Address: "12 Mill Road", MakeDefault: true,
		})); err != nil {
			t.Fatalf("SaveLocation: %v", err)
		}
		got := list(t)
		if len(got) != 2 {
			t.Fatalf("got %d locations, want 2", len(got))
		}
		var defaults int
		for _, l := range got {
			if l.GetIsDefault() {
				defaults++
			}
		}
		if defaults != 1 {
			t.Errorf("%d locations are marked default; there must be exactly one", defaults)
		}
		if got[0].GetName() != "Rackhouse "+tag {
			t.Errorf("the new default is %q, want the one just made default", got[0].GetName())
		}
	})

	t.Run("moving a cask between premises moves no alcohol", func(t *testing.T) {
		cask := f.barrel(t, "Located cask "+tag, 200)
		_, before := f.balance(t, cask.ID)

		var target string
		for _, l := range list(t) {
			if !l.GetIsDefault() {
				target = l.GetId()
			}
		}
		if target == "" {
			t.Fatal("no non-default location to move to")
		}
		if _, err := svc.SetContainerLocation(f.ctx, connect.NewRequest(
			&stillhousev1.SetContainerLocationRequest{
				ContainerId: cask.ID.String(), LocationId: target,
			})); err != nil {
			t.Fatalf("SetContainerLocation: %v", err)
		}
		if _, after := f.balance(t, cask.ID); after != before {
			t.Errorf("moving a cask changed its LAA from %v to %v — a location change is "+
				"not a movement of alcohol", before, after)
		}
	})

	t.Run("the retail supply report says what it cannot answer", func(t *testing.T) {
		got, err := svc.RetailSupplyReport(f.ctx, connect.NewRequest(
			&stillhousev1.RetailSupplyReportRequest{
				PeriodStart: "2026-01-01", PeriodEnd: "2026-12-31",
			}))
		if err != nil {
			t.Fatalf("RetailSupplyReport: %v", err)
		}
		// The rule is about a store's whole stock and Stillhouse cannot
		// see what else the store bought. Producing that ratio would be
		// inventing the number the rule turns on, so the caveat is the
		// load-bearing part of this response.
		if !strings.Contains(got.Msg.GetCaveat(), "cannot see") {
			t.Errorf("caveat %q does not say what the report cannot answer", got.Msg.GetCaveat())
		}
		if len(got.Msg.GetLines()) < 2 {
			t.Errorf("got %d lines, want one per location", len(got.Msg.GetLines()))
		}
	})
}
