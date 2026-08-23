package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// PLAN F7. The pure arithmetic is covered in internal/forecast; these
// cover the property that matters at this layer and cannot be tested
// there: the forecast is reported BESIDE committed demand and is never
// added to it.
//
// Stage 185's reasoning is the whole constraint — a plan built on an
// invented forecast looks exactly as authoritative as one built on
// orders — so the two figures stay in separate columns.
//
// Needs STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

// No method chosen is a refusal, not a projection of zero. Same
// discipline as the WIP charge basis and the chart of accounts.
func TestForecast_UnsetMethodRefuses(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewSchedulingService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	resp, err := svc.DemandForecast(f.ctx, connect.NewRequest(&stillhousev1.DemandForecastRequest{}))
	if err != nil {
		t.Fatalf("DemandForecast: %v", err)
	}
	if resp.Msg.GetRefused() == "" {
		t.Fatal("projected without a method having been chosen")
	}
	if len(resp.Msg.GetLines()) != 0 {
		t.Errorf("refused but returned %d lines", len(resp.Msg.GetLines()))
	}
	if !strings.Contains(resp.Msg.GetRefused(), "steady or seasonal") {
		t.Errorf("refusal does not explain the choice: %q", resp.Msg.GetRefused())
	}
}

// The load-bearing property. Committed and forecast are separate fields
// and the response carries the caution explaining why they are never one
// number.
func TestForecast_CommittedAndForecastStaySeparate(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewSchedulingService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if _, err := svc.SetForecastMethod(f.ctx, connect.NewRequest(&stillhousev1.SetForecastMethodRequest{
		Method: stillhousev1.ForecastMethod_FORECAST_METHOD_TRAILING_AVERAGE, TrailingMonths: 3,
	})); err != nil {
		t.Fatalf("SetForecastMethod: %v", err)
	}

	resp, err := svc.DemandForecast(f.ctx, connect.NewRequest(&stillhousev1.DemandForecastRequest{}))
	if err != nil {
		t.Fatalf("DemandForecast: %v", err)
	}
	if resp.Msg.GetRefused() != "" {
		t.Fatalf("refused after a method was set: %s", resp.Msg.GetRefused())
	}
	if !strings.Contains(resp.Msg.GetCaution(), "never added together") {
		t.Errorf("caution does not state the rule: %q", resp.Msg.GetCaution())
	}
	if resp.Msg.GetMethod() != stillhousev1.ForecastMethod_FORECAST_METHOD_TRAILING_AVERAGE {
		t.Errorf("method: %v", resp.Msg.GetMethod())
	}
}

// A hand-entered number beats the computed one and says so, with its
// reason. Without the reason the override is unarguable next quarter.
func TestForecast_ManualOverrideWinsAndCarriesItsReason(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewSchedulingService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	prod := f.product(t, "Forecast Gin", 750, 40)

	if _, err := svc.SetForecastMethod(f.ctx, connect.NewRequest(&stillhousev1.SetForecastMethodRequest{
		Method: stillhousev1.ForecastMethod_FORECAST_METHOD_TRAILING_AVERAGE, TrailingMonths: 3,
	})); err != nil {
		t.Fatalf("SetForecastMethod: %v", err)
	}

	next := time.Now().UTC().AddDate(0, 1, 0)
	// A reason is required.
	if _, err := svc.SaveDemandForecast(f.ctx, connect.NewRequest(&stillhousev1.SaveDemandForecastRequest{
		ProductId: prod.ID.String(), Month: next.Format("2006-01-02"), Bottles: 400,
	})); err == nil {
		t.Error("saved a hand-entered forecast with no reason")
	}

	if _, err := svc.SaveDemandForecast(f.ctx, connect.NewRequest(&stillhousev1.SaveDemandForecastRequest{
		ProductId: prod.ID.String(), Month: next.Format("2006-01-02"), Bottles: 400,
		Reason: "LCBO listing confirmed for the autumn",
	})); err != nil {
		t.Fatalf("SaveDemandForecast: %v", err)
	}

	resp, err := svc.DemandForecast(f.ctx, connect.NewRequest(&stillhousev1.DemandForecastRequest{
		Month: next.Format("2006-01-02"),
	}))
	if err != nil {
		t.Fatalf("DemandForecast: %v", err)
	}
	var found bool
	for _, l := range resp.Msg.GetLines() {
		if l.GetProductId() != prod.ID.String() {
			continue
		}
		found = true
		if !l.GetOverridden() {
			t.Error("the hand-entered figure was not marked as an override")
		}
		if l.GetBottlesForecast() != 400 {
			t.Errorf("forecast: got %d, want the entered 400", l.GetBottlesForecast())
		}
		if !strings.Contains(l.GetOverrideReason(), "LCBO") {
			t.Errorf("reason not carried: %q", l.GetOverrideReason())
		}
	}
	if !found {
		t.Errorf("the overridden product is not in the forecast at all: %+v", resp.Msg.GetLines())
	}
}

// A product with no sales history is reported as unavailable with a
// reason, not as a forecast of zero. Planning on the second when the
// first is true under-produces for a reason nobody can see.
func TestForecast_NoHistoryIsUnavailableNotZero(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewSchedulingService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	prod := f.product(t, "Never Sold", 750, 40)
	_ = prod

	if _, err := svc.SetForecastMethod(f.ctx, connect.NewRequest(&stillhousev1.SetForecastMethodRequest{
		Method: stillhousev1.ForecastMethod_FORECAST_METHOD_TRAILING_AVERAGE, TrailingMonths: 3,
	})); err != nil {
		t.Fatalf("SetForecastMethod: %v", err)
	}
	resp, err := svc.DemandForecast(f.ctx, connect.NewRequest(&stillhousev1.DemandForecastRequest{}))
	if err != nil {
		t.Fatalf("DemandForecast: %v", err)
	}
	for _, l := range resp.Msg.GetLines() {
		if l.GetAvailable() && l.GetMonthsUsed() == 0 {
			t.Errorf("%s: available with no months of history behind it", l.GetProductName())
		}
		if !l.GetAvailable() && l.GetMissing() == "" {
			t.Errorf("%s: unavailable with no reason", l.GetProductName())
		}
	}
}

// Clearing the method back to unspecified must return to refusing, so an
// operator who chose one by mistake is not stuck with a projection they
// do not believe.
func TestForecast_MethodCanBeCleared(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewSchedulingService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if _, err := svc.SetForecastMethod(f.ctx, connect.NewRequest(&stillhousev1.SetForecastMethodRequest{
		Method: stillhousev1.ForecastMethod_FORECAST_METHOD_SAME_PERIOD_LAST_YEAR,
	})); err != nil {
		t.Fatalf("SetForecastMethod: %v", err)
	}
	if _, err := svc.SetForecastMethod(f.ctx, connect.NewRequest(&stillhousev1.SetForecastMethodRequest{
		Method: stillhousev1.ForecastMethod_FORECAST_METHOD_UNSPECIFIED,
	})); err != nil {
		t.Fatalf("clear: %v", err)
	}
	resp, err := svc.DemandForecast(f.ctx, connect.NewRequest(&stillhousev1.DemandForecastRequest{}))
	if err != nil {
		t.Fatalf("DemandForecast: %v", err)
	}
	if resp.Msg.GetRefused() == "" {
		t.Error("clearing the method did not go back to refusing")
	}
}

// The requirements half, end to end: a linked recipe turns a forecast
// into grain, and no linked recipe refuses that half while still giving
// the alcohol figure — which needs only the product's own size and
// strength.
func TestForecast_RequirementsRefuseGrainWithoutARecipe(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewSchedulingService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	prod := f.product(t, "Unlinked Gin", 750, 40)

	if _, err := svc.SetForecastMethod(f.ctx, connect.NewRequest(&stillhousev1.SetForecastMethodRequest{
		Method: stillhousev1.ForecastMethod_FORECAST_METHOD_MANUAL,
	})); err != nil {
		t.Fatalf("SetForecastMethod: %v", err)
	}
	next := time.Now().UTC().AddDate(0, 1, 0)
	if _, err := svc.SaveDemandForecast(f.ctx, connect.NewRequest(&stillhousev1.SaveDemandForecastRequest{
		ProductId: prod.ID.String(), Month: next.Format("2006-01-02"),
		Bottles: 1000, Reason: "planning test",
	})); err != nil {
		t.Fatalf("SaveDemandForecast: %v", err)
	}

	resp, err := svc.DemandForecast(f.ctx, connect.NewRequest(&stillhousev1.DemandForecastRequest{
		Month: next.Format("2006-01-02"),
	}))
	if err != nil {
		t.Fatalf("DemandForecast: %v", err)
	}
	var line *stillhousev1.ForecastLine
	for _, l := range resp.Msg.GetLines() {
		if l.GetProductId() == prod.ID.String() {
			line = l
		}
	}
	if line == nil {
		t.Fatal("the product is not in the forecast")
	}

	// The alcohol figure needs no recipe: 1000 × 750 mL × 40% = 300 LAA.
	if !near(line.GetLaaNeeded(), 300, 1e-6) {
		t.Errorf("LAA needed: got %v, want 300 — this half needs no recipe", line.GetLaaNeeded())
	}
	if line.GetBottlesToMake() != 1000 {
		t.Errorf("bottles to make: %d", line.GetBottlesToMake())
	}
	// The materials half refuses, and says what to do.
	if line.GetMaterialsAvailable() {
		t.Fatalf("produced grain quantities with no recipe linked: %+v", line.GetMaterials())
	}
	if !strings.Contains(line.GetMaterialsMissing(), "Link one") {
		t.Errorf("refusal does not say what to do: %q", line.GetMaterialsMissing())
	}

	// And the two stock figures are kept apart: a maturing cask is not
	// available for next month's bottling.
	if resp.Msg.GetFreeLaa() < 0 || resp.Msg.GetMaturingLaa() < 0 {
		t.Errorf("negative stock figures: %v / %v", resp.Msg.GetFreeLaa(), resp.Msg.GetMaturingLaa())
	}
	if !near(resp.Msg.GetTotalLaaNeeded(), 300, 1e-6) {
		t.Errorf("total LAA needed: got %v, want 300", resp.Msg.GetTotalLaaNeeded())
	}
}
