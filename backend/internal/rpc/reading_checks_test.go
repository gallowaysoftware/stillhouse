package rpc

import (
	"math"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

func TestCheckMashMetric(t *testing.T) {
	const (
		og    = stillhousev1.MashMetricKind_MASH_METRIC_KIND_ORIGINAL_GRAVITY
		ph    = stillhousev1.MashMetricKind_MASH_METRIC_KIND_MASH_PH
		temp  = stillhousev1.MashMetricKind_MASH_METRIC_KIND_MASH_TEMP_C
		water = stillhousev1.MashMetricKind_MASH_METRIC_KIND_WATER_VOLUME_L
		wash  = stillhousev1.MashMetricKind_MASH_METRIC_KIND_WASH_VOLUME_L
		other = stillhousev1.MashMetricKind_MASH_METRIC_KIND_OTHER
	)
	for _, tc := range []struct {
		name  string
		kind  stillhousev1.MashMetricKind
		value float64
		unit  string
		ok    bool
	}{
		{"gravity", og, 1.062, "", true},
		{"gravity, unit named", og, 1.062, "SG", true},
		{"gravity in points", og, 1062, "", false},
		{"gravity in Plato", og, 15.2, "", false},
		{"unit is Plato", og, 1.062, "°P", false},
		{"gravity not a number", og, math.NaN(), "", false},
		{"pH", ph, 5.4, "pH", true},
		{"pH without its decimal", ph, 54, "", false},
		{"temperature", temp, 64, "°C", true},
		{"temperature in °F", temp, 148, "°F", false},
		{"temperature off the scale", temp, 6500, "", false},
		{"a cooled mash", temp, 18, "c", true},
		{"volume", water, 83, "L", true},
		{"volume in gallons", water, 22, "gal", false},
		{"negative volume", wash, -94, "", false},
		{"zero volume", wash, 0, "", true},
		{"other takes any unit", other, -3, "furlongs", true},
		{"other is still a number", other, math.Inf(1), "", false},
	} {
		if err := checkMashMetric(tc.kind, tc.value, tc.unit); (err == nil) != tc.ok {
			t.Errorf("%s: %g %q: %v", tc.name, tc.value, tc.unit, err)
		}
	}
}

func TestReadingsRefuseWhatCannotBeOne(t *testing.T) {
	b := newFloorBatch(t)
	refused := func(name string, err error) {
		t.Helper()
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v, want invalid_argument", name, err)
		}
	}
	log := func(m *stillhousev1.AddFermentationLogRequest) error {
		m.FermentationRunId = b.fermID
		_, err := b.ferm.AddFermentationLog(b.ctx, connect.NewRequest(m))
		return err
	}
	refused("an empty reading", log(&stillhousev1.AddFermentationLogRequest{}))
	refused("gravity in points", log(&stillhousev1.AddFermentationLogRequest{SpecificGravity: 1052, SpecificGravitySet: true}))
	refused("pH 45", log(&stillhousev1.AddFermentationLogRequest{Ph: 45, PhSet: true}))
	refused("a reading from next year", log(&stillhousev1.AddFermentationLogRequest{
		SpecificGravity: 1.02, SpecificGravitySet: true, ObservedAt: timestamppb.New(time.Now().AddDate(1, 0, 0))}))
	must(t, log(&stillhousev1.AddFermentationLogRequest{SpecificGravity: 0.998, SpecificGravitySet: true}))
	must(t, log(&stillhousev1.AddFermentationLogRequest{Notes: "krausen falling"}))

	_, err := b.ferm.CreateFermentationRun(b.ctx, connect.NewRequest(&stillhousev1.CreateFermentationRunRequest{
		MashRunId: b.mashID, FermenterLabel: "FV2", TargetFinalGravity: 998, TargetFinalGravitySet: true}))
	refused("target gravity in points", err)
	_, err = b.ferm.CreateFermentationRun(b.ctx, connect.NewRequest(&stillhousev1.CreateFermentationRunRequest{
		MashRunId: b.mashID, FermenterLabel: "FV2", InitialVolumeL: -5, InitialVolumeLSet: true}))
	refused("a negative volume", err)

	_, err = b.dist.AddDistillationCharge(b.ctx, connect.NewRequest(&stillhousev1.AddDistillationChargeRequest{
		DistillationRunId: b.runID, FermentationRunId: b.fermID, VolumeChargedL: math.NaN(), AbvPct: 8}))
	refused("a charge of NaN litres", err)
	cut := func(vol, abv float64) error {
		_, err := b.dist.AddDistillationCut(b.ctx, connect.NewRequest(&stillhousev1.AddDistillationCutRequest{
			DistillationRunId: b.runID, Kind: stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_HEARTS, VolumeL: vol, AbvPct: abv}))
		return err
	}
	refused("a cut at 680 %", cut(5, 680))
	refused("a cut of -5 L", cut(-5, 68))
	refused("a cut of NaN litres", cut(math.NaN(), 68))
	must(t, cut(5, 68))

	_, err = b.dist.AddDistillationReading(b.ctx, connect.NewRequest(&stillhousev1.AddDistillationReadingRequest{
		DistillationRunId: b.runID, PotTemperatureC: -900, PotTemperatureCSet: true}))
	refused("a pot at -900 °C", err)
}
