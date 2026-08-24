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

// The load-bearing one, end to end: a report whose every rate came from
// the programme's own material is a number somebody can pay against.
//
// The fixture bottles 750 mL to Ontario, which puts it in the upper of
// ODRP's two bands — the boundary is 630 mL, so a standard bottle is the
// 20¢ one. Before the schedule landed this line read 20¢ for every size
// by accident rather than by band, and a 375 mL bottle read 20¢ too.
func TestContainerDeposit_SourcedReportIsRemittableEndToEnd(t *testing.T) {
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

	var found bool
	for _, l := range resp.Msg.GetLines() {
		if l.GetJurisdiction() != "CA-ON" {
			continue
		}
		found = true
		if l.GetRateProvenance() != "sourced" {
			t.Errorf("CA-ON provenance: got %q, want sourced", l.GetRateProvenance())
		}
		if l.GetBottleSizeMl() != 750 {
			t.Fatalf("fixture bottle size changed to %d mL; this test is about the "+
				"630 mL band boundary and no longer straddles it", l.GetBottleSizeMl())
		}
		if l.GetDepositPerContainerCad() != 0.20 {
			t.Errorf("750 mL into Ontario: %.2f a container, want 0.20 — the band "+
				"boundary is 630 mL, so a standard bottle is in the upper band",
				l.GetDepositPerContainerCad())
		}
		if !l.GetAmountAvailable() {
			t.Errorf("a sourced rate produced no figure: %s", l.GetAmountMissing())
		}
		if l.GetRateSource() == "" {
			t.Error("a rate travelled with no source")
		}
	}
	if !found {
		t.Fatalf("no CA-ON line: %+v", resp.Msg.GetLines())
	}

	if !resp.Msg.GetRemittable() {
		t.Errorf("every rate is sourced but the report is not remittable; blocked on %v",
			resp.Msg.GetNeedsASourcedRate())
	}
	if len(resp.Msg.GetNeedsASourcedRate()) != 0 {
		t.Errorf("remittable, yet %v named as needing a rate", resp.Msg.GetNeedsASourcedRate())
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

// An indicative rate is a planning figure, not a remittance, and the
// report has to say so rather than presenting a total somebody pays.
// PEI carries one: nobody has confirmed it against the province's own
// schedule, and PEI charges a non-refundable environment fee beside the
// deposit that the figure does not include.
func TestContainerDeposit_IndicativeRateIsNotRemittable(t *testing.T) {
	// Pinned against the package's own scale rather than a literal, so
	// this keeps meaning what it says if the scale grows.
	if pricing.Sourced <= pricing.Indicative {
		t.Fatal("the provenance scale no longer ranks sourced above indicative")
	}
	j := pricing.Find("CA-PE")
	if j == nil {
		t.Fatal("CA-PE not in Jurisdictions")
	}
	if got := j.ContainerDeposit.For(750).Provenance; got != pricing.Indicative {
		t.Skipf("CA-PE's deposit rate is now %v — move this test to a jurisdiction "+
			"that is still indicative, or delete it once none are", got)
	}

	line := &stillhousev1.ContainerDepositLine{
		Jurisdiction: "CA-PE", BottleSizeMl: 750, ContainersNet: 10,
	}
	needs := map[string]bool{}
	applyDepositRate(line, needs)

	// The amount is still computed: indicative is usable for planning,
	// which is what this report is for until it is not.
	if !line.GetAmountAvailable() {
		t.Errorf("an indicative rate produced no figure at all: %s", line.GetAmountMissing())
	}
	if !needs["CA-PE"] {
		t.Error("an indicative rate was not named as blocking a remittance — that is " +
			"quoting an aggregator to a stewardship programme")
	}
	if line.GetRateNote() == "" {
		t.Error("an indicative rate travelled with nothing saying why it is only indicative")
	}
}

// The rate follows the bottle, not just the province. Each programme
// bands at its own boundary, and the same 750 mL bottle lands on
// different sides of Alberta's and Ontario's.
func TestContainerDeposit_RateFollowsBottleSize(t *testing.T) {
	cases := []struct {
		code   string
		sizeML int32
		want   float64
	}{
		{"CA-AB", 750, 0.10},  // under Alberta's 1 L boundary
		{"CA-AB", 1500, 0.25}, // over it
		{"CA-ON", 375, 0.10},  // under Ontario's 630 mL boundary
		{"CA-ON", 750, 0.20},  // over it
	}
	for _, c := range cases {
		line := &stillhousev1.ContainerDepositLine{
			Jurisdiction: c.code, BottleSizeMl: c.sizeML, ContainersNet: 100,
		}
		applyDepositRate(line, map[string]bool{})
		if !line.GetAmountAvailable() {
			t.Errorf("%s %d mL: no figure: %s", c.code, c.sizeML, line.GetAmountMissing())
			continue
		}
		if line.GetDepositPerContainerCad() != c.want {
			t.Errorf("%s %d mL: %.2f a container, want %.2f",
				c.code, c.sizeML, line.GetDepositPerContainerCad(), c.want)
		}
		if want := c.want * 100; line.GetDepositTotalCad() != want {
			t.Errorf("%s %d mL: total %.2f over 100 containers, want %.2f",
				c.code, c.sizeML, line.GetDepositTotalCad(), want)
		}
	}
}

// The stewardship fee is reported beside the deposit and never inside
// it. They are collected over the same bottles and owed to different
// bodies, and only one of them comes back to anybody — remitting the fee
// to the deposit programme is a real mistake, so the total must not
// invite it.
func TestContainerDeposit_StewardshipFeeStaysOutOfTheDeposit(t *testing.T) {
	j := pricing.Find("CA-BC")
	if j == nil {
		t.Fatal("CA-BC not in Jurisdictions")
	}
	if !j.ContainerRecyclingFeeCAD.Known() {
		t.Skip("CA-BC no longer carries a stewardship fee")
	}

	line := &stillhousev1.ContainerDepositLine{
		Jurisdiction: "CA-BC", BottleSizeMl: 750, ContainersNet: 100,
	}
	applyDepositRate(line, map[string]bool{})

	if !line.GetRecyclingFeeAvailable() {
		t.Fatalf("no fee reported: %s", line.GetRecyclingFeeMissing())
	}
	// BC: 10¢ deposit, 13¢ fee. Distinct on purpose — if the deposit
	// total ever picks up the fee it lands on one of these.
	if line.GetDepositPerContainerCad() != 0.10 {
		t.Errorf("deposit %.2f, want 0.10", line.GetDepositPerContainerCad())
	}
	if line.GetRecyclingFeePerContainerCad() != 0.13 {
		t.Errorf("fee %.2f, want 0.13", line.GetRecyclingFeePerContainerCad())
	}
	if line.GetDepositTotalCad() != 10 {
		t.Errorf("deposit total %.2f over 100 containers, want 10.00 — the fee has "+
			"leaked into the deposit", line.GetDepositTotalCad())
	}
	if line.GetRecyclingFeeTotalCad() != 13 {
		t.Errorf("fee total %.2f over 100 containers, want 13.00", line.GetRecyclingFeeTotalCad())
	}
	// A fee with no source is the thing this whole package exists to
	// prevent, and it is charged per bottle whether or not anyone
	// returns it.
	if line.GetRecyclingFeeProvenance() != "sourced" || line.GetRecyclingFeeSource() == "" {
		t.Errorf("fee provenance %q source %q",
			line.GetRecyclingFeeProvenance(), line.GetRecyclingFeeSource())
	}
}

// Quebec's glass expansion was postponed to 2027, so Stillhouse cannot
// answer without knowing the container material — and it refuses rather
// than picking one. The count still stands.
func TestContainerDeposit_QuebecRefusesAndKeepsTheCount(t *testing.T) {
	line := &stillhousev1.ContainerDepositLine{
		Jurisdiction: "CA-QC", BottleSizeMl: 750, ContainersNet: 88,
	}
	needs := map[string]bool{}
	applyDepositRate(line, needs)

	if line.GetAmountAvailable() {
		t.Errorf("Quebec produced a deposit of %.2f a container; glass spirits are not "+
			"under deposit there until 2027", line.GetDepositPerContainerCad())
	}
	if line.GetContainersNet() != 88 {
		t.Error("the count was lost with the rate")
	}
	if !needs["CA-QC"] {
		t.Error("Quebec was not named as blocking the remittance")
	}
	if !strings.Contains(line.GetAmountMissing(), "2027") {
		t.Errorf("the refusal does not say when this changes: %q", line.GetAmountMissing())
	}
}
