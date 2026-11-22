package rpc

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// These bounds catch a figure in the wrong scale: a gravity in points or
// Plato, a pH that lost its decimal point, a temperature that cannot be a
// liquid's. They say nothing about what makes a good mash. The bench does
// that, with its sources.
const (
	minSpecificGravity = 0.9
	maxSpecificGravity = 1.2
	maxPH              = 14
	minReadingTempC    = -50
	maxReadingTempC    = 200
)

// futureSkew forgives a phone whose clock runs ahead. Past it, a reading
// would outrank every later one, since the latest is what counts.
const futureSkew = 10 * time.Minute

func checkObservedAt(name string, ts *timestamppb.Timestamp) error {
	if ts == nil {
		return nil
	}
	if t := ts.AsTime(); t.After(time.Now().Add(futureSkew)) {
		return invalidReading(fmt.Errorf("%s is in the future: %s", name, t.Format(time.RFC3339)))
	}
	return nil
}

func checkSpecificGravity(name string, v float64) error {
	if err := validateFinite(name, v); err != nil {
		return invalidReading(err)
	}
	if v >= minSpecificGravity && v <= maxSpecificGravity {
		return nil
	}
	if v >= minSpecificGravity*1000 && v <= maxSpecificGravity*1000 {
		return invalidReading(fmt.Errorf("%s of %g is missing its decimal point: did you mean %g?", name, v, v/1000))
	}
	return invalidReading(fmt.Errorf(
		"%s must be a specific gravity such as 1.050, got %g; convert a reading in °P or points first", name, v))
}

func checkPH(name string, v float64) error {
	if err := validateFinite(name, v); err != nil {
		return invalidReading(err)
	}
	if v < 0 || v > maxPH {
		return invalidReading(fmt.Errorf("%s must be between 0 and 14, got %g", name, v))
	}
	return nil
}

func checkTemperatureC(name string, v float64) error {
	if err := validateFinite(name, v); err != nil {
		return invalidReading(err)
	}
	if v < minReadingTempC || v > maxReadingTempC {
		return invalidReading(fmt.Errorf("%s must be in °C, between %d and %d, got %g", name, minReadingTempC, maxReadingTempC, v))
	}
	return nil
}

func checkVolumeL(name string, v float64) error {
	if err := validateFinite(name, v); err != nil {
		return invalidReading(err)
	}
	if v < 0 {
		return invalidReading(fmt.Errorf("%s cannot be negative, got %g", name, v))
	}
	return nil
}

func checkStrength(name string, v float64) error {
	if err := validateAbvPct(name, v); err != nil {
		return invalidReading(err)
	}
	return nil
}

func invalidReading(err error) error { return connect.NewError(connect.CodeInvalidArgument, err) }

// checkMashMetric checks the value against its kind, and the unit against
// the one the kind is recorded in: the unit is kept as a label, so 148 °F
// would be stored as 148 °C.
func checkMashMetric(kind stillhousev1.MashMetricKind, value float64, unit string) error {
	var check func(string, float64) error
	var name, own string
	var accepted []string
	switch kind {
	case stillhousev1.MashMetricKind_MASH_METRIC_KIND_ORIGINAL_GRAVITY:
		check, name, own, accepted = checkSpecificGravity, "original gravity", "SG", []string{"sg"}
	case stillhousev1.MashMetricKind_MASH_METRIC_KIND_MASH_PH:
		check, name, own, accepted = checkPH, "mash pH", "pH", []string{"ph"}
	case stillhousev1.MashMetricKind_MASH_METRIC_KIND_MASH_TEMP_C:
		check, name, own, accepted = checkTemperatureC, "mash temperature", "°C", celsius
	case stillhousev1.MashMetricKind_MASH_METRIC_KIND_STRIKE_TEMP_C:
		check, name, own, accepted = checkTemperatureC, "strike temperature", "°C", celsius
	case stillhousev1.MashMetricKind_MASH_METRIC_KIND_WATER_VOLUME_L:
		check, name, own, accepted = checkVolumeL, "water volume", "L", litres
	case stillhousev1.MashMetricKind_MASH_METRIC_KIND_WASH_VOLUME_L:
		check, name, own, accepted = checkVolumeL, "wash volume", "L", litres
	default:
		if err := validateFinite("value", value); err != nil {
			return invalidReading(err)
		}
		return nil
	}
	if u := strings.ToLower(strings.TrimSpace(unit)); u != "" && !slices.Contains(accepted, u) {
		return invalidReading(fmt.Errorf("%s is recorded in %s, not %s", name, own, strings.TrimSpace(unit)))
	}
	return check(name, value)
}

var (
	celsius = []string{"°c", "c", "degc", "deg c", "celsius"}
	litres  = []string{"l", "litre", "litres", "liter", "liters"}
)

func checkCut(volumeL, abvPct float64, observed *timestamppb.Timestamp) error {
	if err := checkVolumeL("volume_l", volumeL); err != nil {
		return err
	}
	if err := checkStrength("abv_pct", abvPct); err != nil {
		return err
	}
	return checkObservedAt("observed_at", observed)
}

func checkFermentationLog(in *stillhousev1.AddFermentationLogRequest) error {
	if !in.GetSpecificGravitySet() && !in.GetPhSet() && !in.GetTemperatureCSet() && strings.TrimSpace(in.GetNotes()) == "" {
		return invalidReading(errors.New("a reading records something: a gravity, a pH, a temperature, or a note"))
	}
	if in.GetSpecificGravitySet() {
		if err := checkSpecificGravity("specific gravity", in.GetSpecificGravity()); err != nil {
			return err
		}
	}
	if in.GetPhSet() {
		if err := checkPH("pH", in.GetPh()); err != nil {
			return err
		}
	}
	if in.GetTemperatureCSet() {
		if err := checkTemperatureC("temperature", in.GetTemperatureC()); err != nil {
			return err
		}
	}
	return checkObservedAt("observed_at", in.GetObservedAt())
}
