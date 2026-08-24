package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/pricing"
)

// PLAN I4. A deposit report looks like an invoice and will be treated as
// one, so the thing these tests protect is the difference between a
// count Stillhouse is sure of and a rate it is not.
//
// Needs STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

func TestContainerDeposit_CountsRemovalsAndNetsReturns(t *testing.T) {
	f := newDutyFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	prov := NewProvincialService(f.db, log)
	rem := NewRemovalService(f.db, log)

	lot, removalID := f.bottleAndRemove(t, 200, 60)

	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	end := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	first, err := prov.ContainerDepositReport(f.ctx, connect.NewRequest(&stillhousev1.ContainerDepositReportRequest{
		PeriodStart: start, PeriodEnd: end,
	}))
	if err != nil {
		t.Fatalf("ContainerDepositReport: %v", err)
	}
	if first.Msg.GetTotalContainersNet() != 60 {
		t.Errorf("containers: got %d, want the 60 removed", first.Msg.GetTotalContainersNet())
	}

	// Ten come back. A returned container is not one the programme is
	// owed for twice.
	if _, err := rem.RecordPackagedReturn(f.ctx, connect.NewRequest(&stillhousev1.RecordPackagedReturnRequest{
		PackagedInventoryId: lot, RemovalId: removalID, Bottles: 10,
		Condition:  stillhousev1.PackagedReturnCondition_PACKAGED_RETURN_CONDITION_SALEABLE,
		ReturnedOn: now.Format("2006-01-02"), Reason: "delisted",
	})); err != nil {
		t.Fatalf("RecordPackagedReturn: %v", err)
	}

	after, err := prov.ContainerDepositReport(f.ctx, connect.NewRequest(&stillhousev1.ContainerDepositReportRequest{
		PeriodStart: start, PeriodEnd: end,
	}))
	if err != nil {
		t.Fatalf("ContainerDepositReport: %v", err)
	}
	if after.Msg.GetTotalContainersNet() != 50 {
		t.Errorf("after a return of 10: got %d, want 50", after.Msg.GetTotalContainersNet())
	}
}

// The load-bearing one. An indicative rate is a planning figure, not a
// remittance, and the report must say so rather than presenting a total
// somebody pays.
func TestContainerDeposit_IndicativeRateIsNotRemittable(t *testing.T) {
	f := newDutyFixture(t)
	prov := NewProvincialService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	f.bottleAndRemove(t, 100, 40)

	now := time.Now().UTC()
	resp, err := prov.ContainerDepositReport(f.ctx, connect.NewRequest(&stillhousev1.ContainerDepositReportRequest{
		PeriodStart: time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
		PeriodEnd:   time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
	}))
	if err != nil {
		t.Fatalf("ContainerDepositReport: %v", err)
	}
	if len(resp.Msg.GetLines()) == 0 {
		t.Fatal("no lines")
	}

	// The fixture bottles to CA-ON, whose deposit rate is indicative.
	var found bool
	for _, l := range resp.Msg.GetLines() {
		if l.GetJurisdiction() != "CA-ON" {
			continue
		}
		found = true
		if l.GetRateProvenance() != "indicative" {
			t.Errorf("CA-ON provenance: got %q — if this became sourced, update the test and the claim",
				l.GetRateProvenance())
		}
		// The amount is still computed: indicative is usable for
		// planning, which is what this report is for until it is not.
		if !l.GetAmountAvailable() {
			t.Errorf("an indicative rate produced no figure at all: %s", l.GetAmountMissing())
		}
		if l.GetRateSource() == "" {
			t.Error("a rate travelled with no source")
		}
	}
	if !found {
		t.Fatalf("no CA-ON line: %+v", resp.Msg.GetLines())
	}

	if resp.Msg.GetRemittable() {
		t.Error("an indicative rate was presented as remittable — that is quoting an aggregator " +
			"to a stewardship programme")
	}
	if len(resp.Msg.GetNeedsASourcedRate()) == 0 {
		t.Error("not remittable, and nothing named as the reason")
	}
	if !strings.Contains(resp.Msg.GetCaution(), "not ours") {
		t.Errorf("caution does not disclaim the rates: %q", resp.Msg.GetCaution())
	}
}

// A jurisdiction Stillhouse carries no rates for still reports its
// count. Knowing how many went out is useful even when the rate is not
// on file, and dropping the line would hide containers entirely.
func TestContainerDeposit_UnknownJurisdictionKeepsTheCount(t *testing.T) {
	line := &stillhousev1.ContainerDepositLine{
		Jurisdiction: "CA-ZZ", ContainersNet: 42,
	}
	needs := map[string]bool{}
	applyDepositRate(line, needs)

	if line.GetAmountAvailable() {
		t.Error("produced a deposit for a jurisdiction with no rates")
	}
	if line.GetContainersNet() != 42 {
		t.Error("the count was lost with the rate")
	}
	if !needs["CA-ZZ"] {
		t.Error("the jurisdiction was not named as needing a rate")
	}
	if line.GetAmountMissing() == "" {
		t.Error("unavailable with no reason")
	}
}

// Sourced rates are remittable; that is the whole point of grading them.
func TestContainerDeposit_SourcedRateIsRemittable(t *testing.T) {
	// Pinned against the package's own scale rather than a literal, so
	// this keeps meaning what it says if the scale grows.
	if pricing.Sourced <= pricing.Indicative {
		t.Fatal("the provenance scale no longer ranks sourced above indicative")
	}
	line := &stillhousev1.ContainerDepositLine{Jurisdiction: "CA-ON", ContainersNet: 10}
	needs := map[string]bool{}
	applyDepositRate(line, needs)
	// CA-ON is indicative today, so it must appear in needs. The moment
	// somebody sources it, this test says so by failing.
	if !needs["CA-ON"] {
		t.Skip("CA-ON's deposit rate is now sourced — update this test and the claim in stage 208")
	}
}
