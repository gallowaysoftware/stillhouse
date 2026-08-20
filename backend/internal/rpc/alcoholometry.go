package rpc

import (
	"context"
	"encoding/hex"
	"errors"
	"log/slog"

	"connectrpc.com/connect"

	"github.com/gallowaysoftware/stillhouse/backend/internal/alcoholometry"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// ReferenceTemperatureC is the temperature alcoholic strength is defined
// at for Canadian excise purposes.
const ReferenceTemperatureC = 20.0

// tablesSourceURL is where the embedded tables came from. Surfaced through
// TablesInfo so an auditor can confirm the basis of a filing.
const tablesSourceURL = "https://www.canada.ca/en/revenue-agency/services/tax/technical-information/excise-duty/tables-alcoholometry/canadian-alcoholometric-tables-1980.html"

type AlcoholometryService struct {
	logger *slog.Logger
}

func NewAlcoholometryService(logger *slog.Logger) *AlcoholometryService {
	return &AlcoholometryService{logger: logger}
}

// resolvedStrength is the outcome of correcting one measurement to 20 °C.
// Every write path that records a strength funnels through resolveStrength
// so the rules live in exactly one place.
type resolvedStrength struct {
	// StrengthPct20C is what gets stored in abv_pct — the legally
	// meaningful strength.
	StrengthPct20C float64
	// VolumeL20C is observedVolumeL × C.
	VolumeL20C float64
	// VolumeFactorC is the factor applied. 1.0 when uncorrected.
	VolumeFactorC float64
	Source        stillhousev1.StrengthSource
}

// LAA returns litres of absolute alcohol at 20 °C.
func (r resolvedStrength) LAA() float64 { return r.VolumeL20C * r.StrengthPct20C / 100 }

// strengthInput is the measurement trio every gauging call accepts.
type strengthInput struct {
	ObservedVolumeL float64
	// AbvPct is the strength the caller asserts is already at 20 °C.
	AbvPct float64
	// DensityKgM3 is a raw hydrometer indication; when set it wins over
	// AbvPct, because it is the reading CRA's approved instrument gives.
	DensityKgM3    float64
	DensityIsSet   bool
	TemperatureC   float64
	TemperatureSet bool
}

// resolveStrength corrects a measurement to 20 °C against the Canadian
// Alcoholometric Tables.
//
// Three paths, in descending order of rigour:
//
//   - density + temperature → the tables give both the strength and the
//     volume factor. This is CRA's approved determination.
//   - strength + temperature → the caller's instrument already corrected
//     the strength; the tables supply only the volume factor.
//   - strength alone → nothing can be corrected. Recorded as uncorrected
//     so it is never mistaken for a determination.
func resolveStrength(in strengthInput) (resolvedStrength, error) {
	switch {
	case in.DensityIsSet && in.TemperatureSet:
		r, err := alcoholometry.Lookup(in.TemperatureC, in.DensityKgM3)
		if err != nil {
			return resolvedStrength{}, err
		}
		return resolvedStrength{
			StrengthPct20C: r.StrengthPct,
			VolumeL20C:     in.ObservedVolumeL * r.VolumeFactor,
			VolumeFactorC:  r.VolumeFactor,
			Source:         stillhousev1.StrengthSource_STRENGTH_SOURCE_TABLE_DENSITY,
		}, nil

	case in.DensityIsSet && !in.TemperatureSet:
		// A hydrometer indication without a temperature cannot be
		// resolved at all — the whole point of the reading is that it is
		// paired with one.
		return resolvedStrength{}, errMissingTemperature

	case in.TemperatureSet:
		r, err := alcoholometry.LookupByStrength(in.TemperatureC, in.AbvPct)
		if err != nil {
			return resolvedStrength{}, err
		}
		return resolvedStrength{
			StrengthPct20C: in.AbvPct,
			VolumeL20C:     in.ObservedVolumeL * r.VolumeFactor,
			VolumeFactorC:  r.VolumeFactor,
			Source:         stillhousev1.StrengthSource_STRENGTH_SOURCE_TABLE_STRENGTH,
		}, nil

	default:
		return resolvedStrength{
			StrengthPct20C: in.AbvPct,
			VolumeL20C:     in.ObservedVolumeL,
			VolumeFactorC:  1.0,
			Source:         stillhousev1.StrengthSource_STRENGTH_SOURCE_UNCORRECTED,
		}, nil
	}
}

// errMissingTemperature is returned when a caller sends a hydrometer
// indication with no temperature. Typed rather than ad-hoc so the handlers'
// catch-all cannot swallow it as an internal error — see stage 115, where
// exactly that swallowed a precondition failure.
var errMissingTemperature = errors.New("a hydrometer indication needs the temperature it was read at")

// alcoholometryError maps a bad reading to InvalidArgument — an
// out-of-range or incomplete reading is the operator's input being wrong,
// not the server failing.
func alcoholometryError(err error) error {
	var re *alcoholometry.RangeError
	if errors.As(err, &re) || errors.Is(err, errMissingTemperature) {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	// The request is fine; the install isn't. FailedPrecondition tells the
	// caller that retrying won't help but fixing the deployment will.
	if errors.Is(err, alcoholometry.ErrNotLoaded) {
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}

func (s *AlcoholometryService) ResolveStrength(
	ctx context.Context,
	req *connect.Request[stillhousev1.ResolveStrengthRequest],
) (*connect.Response[stillhousev1.ResolveStrengthResponse], error) {
	if _, ok := CurrentUser(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	if !in.GetDensityKgM3Set() && !in.GetStrengthPct_20CSet() {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("supply either density_kg_m3 or strength_pct_20c"))
	}

	res, err := resolveStrength(strengthInput{
		ObservedVolumeL: in.GetObservedVolumeL(),
		AbvPct:          in.GetStrengthPct_20C(),
		DensityKgM3:     in.GetDensityKgM3(),
		DensityIsSet:    in.GetDensityKgM3Set(),
		TemperatureC:    in.GetTemperatureC(),
		// A resolve request always carries a temperature — the caller is
		// asking precisely what the correction does.
		TemperatureSet: true,
	})
	if err != nil {
		return nil, alcoholometryError(err)
	}

	out := &stillhousev1.ResolveStrengthResponse{
		Reading: &stillhousev1.AlcoholometryReading{
			StrengthPct_20C: res.StrengthPct20C,
			VolumeFactorC:   res.VolumeFactorC,
		},
		Source:         res.Source,
		VolumeResolved: in.GetObservedVolumeLSet(),
	}
	if in.GetObservedVolumeLSet() {
		out.VolumeL_20C = res.VolumeL20C
		out.Laa = res.LAA()
	}
	// A (litres per kilogram) only comes back off a density lookup.
	if in.GetDensityKgM3Set() {
		if r, err := alcoholometry.Lookup(in.GetTemperatureC(), in.GetDensityKgM3()); err == nil {
			out.Reading.LitresPerKg = r.LitresPerKg
		}
	}
	if lo, hi, err := alcoholometry.DensitySpan(in.GetTemperatureC()); err == nil {
		out.DensityMinKgM3, out.DensityMaxKgM3 = lo, hi
	}
	return connect.NewResponse(out), nil
}

func (s *AlcoholometryService) TablesInfo(
	ctx context.Context,
	req *connect.Request[stillhousev1.TablesInfoRequest],
) (*connect.Response[stillhousev1.TablesInfoResponse], error) {
	if _, ok := CurrentUser(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	// Not an error when the tables are absent — that's a setup state the
	// UI is expected to render, not a failure. Name and source URL come
	// back regardless so the page can say what to go and download.
	out := &stillhousev1.TablesInfoResponse{
		Name:                  "Canadian Alcoholometric Tables 1980",
		SourceUrl:             tablesSourceURL,
		ReferenceTemperatureC: ReferenceTemperatureC,
	}
	if !alcoholometry.Loaded() {
		return connect.NewResponse(out), nil
	}
	sum, err := alcoholometry.SourceSHA256()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	lo, hi, err := alcoholometry.TemperatureRange()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out.Loaded = true
	out.SourceSha256 = hex.EncodeToString(sum[:])
	out.TemperatureMinC, out.TemperatureMaxC = lo, hi
	out.FileName = alcoholometry.SourceName()
	out.RowCount = int64(alcoholometry.RowCount())
	return connect.NewResponse(out), nil
}

// strengthSourceToDB / strengthSourceToProto bridge the proto enum and the
// Postgres enum. Unspecified maps to uncorrected: a caller that says
// nothing has, by definition, not corrected anything.
func strengthSourceToDB(s stillhousev1.StrengthSource) sqlcgen.StrengthSource {
	switch s {
	case stillhousev1.StrengthSource_STRENGTH_SOURCE_TABLE_DENSITY:
		return sqlcgen.StrengthSourceTableDensity
	case stillhousev1.StrengthSource_STRENGTH_SOURCE_TABLE_STRENGTH:
		return sqlcgen.StrengthSourceTableStrength
	default:
		return sqlcgen.StrengthSourceUncorrected
	}
}

func strengthSourceToProto(s sqlcgen.StrengthSource) stillhousev1.StrengthSource {
	switch s {
	case sqlcgen.StrengthSourceTableDensity:
		return stillhousev1.StrengthSource_STRENGTH_SOURCE_TABLE_DENSITY
	case sqlcgen.StrengthSourceTableStrength:
		return stillhousev1.StrengthSource_STRENGTH_SOURCE_TABLE_STRENGTH
	default:
		return stillhousev1.StrengthSource_STRENGTH_SOURCE_UNCORRECTED
	}
}

// PlanReduction computes a proofing-down plan. See the alcoholometry
// package for why the water figure is a mass balance rather than a
// subtraction.
func (s *AlcoholometryService) PlanReduction(
	ctx context.Context,
	req *connect.Request[stillhousev1.PlanReductionRequest],
) (*connect.Response[stillhousev1.PlanReductionResponse], error) {
	if _, ok := CurrentUser(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	// A weighed charge is the better input, so it wins when both arrive.
	var (
		plan alcoholometry.Reduction
		err  error
	)
	if in.GetFromMassKgSet() {
		plan, err = alcoholometry.PlanReductionFromMass(
			in.GetFromMassKg(), in.GetFromStrengthPct(), in.GetToStrengthPct())
	} else {
		plan, err = alcoholometry.PlanReduction(
			in.GetFromVolumeL(), in.GetFromStrengthPct(), in.GetToStrengthPct())
	}
	if err != nil {
		// Every failure here is the operator asking for something
		// impossible (water can't raise strength) or out of range.
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&stillhousev1.PlanReductionResponse{
		FinalVolumeL: round4(plan.FinalVolumeL),
		WaterToAddL:  round4(plan.WaterToAddL),
		NaiveWaterL:  round4(plan.NaiveWaterL),
		ContractionL: round4(plan.ContractionL),
		Laa:          round4(plan.LAA),
		FromMassKg:   round4(plan.FromMassKg),
		FinalMassKg:  round4(plan.FinalMassKg),
		WaterToAddKg: round4(plan.WaterToAddKg),
	}), nil
}

// PlanBlend vats parcels together. See the alcoholometry package for why
// the result is neither the sum of the volumes nor the weighted mean of
// the strengths.
func (s *AlcoholometryService) PlanBlend(
	ctx context.Context,
	req *connect.Request[stillhousev1.PlanBlendRequest],
) (*connect.Response[stillhousev1.PlanBlendResponse], error) {
	if _, ok := CurrentUser(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	sources := make([]alcoholometry.BlendSource, 0, len(req.Msg.GetSources()))
	for _, in := range req.Msg.GetSources() {
		sources = append(sources, alcoholometry.BlendSource{
			Label:       in.GetLabel(),
			VolumeL:     in.GetVolumeL(),
			StrengthPct: in.GetStrengthPct(),
		})
	}
	plan, err := alcoholometry.PlanBlend(sources, req.Msg.GetTargetStrengthPct())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out := &stillhousev1.PlanBlendResponse{
		TotalLaa:         round4(plan.TotalLAA),
		TotalMassKg:      round4(plan.TotalMassKg),
		BlendVolumeL:     round4(plan.BlendVolumeL),
		BlendStrengthPct: round2(plan.BlendStrengthPct),
		NaiveVolumeL:     round4(plan.NaiveVolumeL),
		ContractionL:     round4(plan.ContractionL),
		ReductionSet:     plan.ReductionSet,
	}
	if plan.ReductionSet {
		out.WaterToAddL = round4(plan.Reduction.WaterToAddL)
		out.WaterToAddKg = round4(plan.Reduction.WaterToAddKg)
		out.FinalVolumeL = round4(plan.Reduction.FinalVolumeL)
	}
	return connect.NewResponse(out), nil
}
