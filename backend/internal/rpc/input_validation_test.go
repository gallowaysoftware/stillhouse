package rpc

import (
	"strings"
	"testing"

	"connectrpc.com/connect"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// Every case here is a mistake QA made while driving the system as a real
// operator would at six in the morning: a decimal point in the wrong place,
// a percentage typed where a fraction belongs, a field left blank. The
// system's job is to say which field and why — not to return a 500, not to
// blame a different field, and above all not to accept it.

func TestResolveAdoptedStockRejectsBadInput(t *testing.T) {
	requireTables(t)
	cases := []struct {
		name string
		in   *stillhousev1.AdoptOpeningInventoryRequest
		want string // substring the message must carry
	}{{
		// Reported "supply either mass_kg or volume_l" — but it WAS
		// supplied. `<= 0` was folded into "not provided", so the operator
		// was sent looking at the wrong thing.
		name: "negative mass names the field it dislikes",
		in:   &stillhousev1.AdoptOpeningInventoryRequest{MassKg: -50, MassKgSet: true},
		want: "mass_kg",
	}, {
		name: "negative volume names the field it dislikes",
		in:   &stillhousev1.AdoptOpeningInventoryRequest{VolumeL: -10, VolumeLSet: true},
		want: "volume_l",
	}, {
		// Reached the database and came back a 500 from a check
		// constraint. Distillation and barrel fills both validate this.
		name: "strength above 100%",
		in: &stillhousev1.AdoptOpeningInventoryRequest{
			VolumeL: 100, VolumeLSet: true, AbvPct: 150,
		},
		want: "abv_pct",
	}, {
		name: "negative strength",
		in: &stillhousev1.AdoptOpeningInventoryRequest{
			VolumeL: 100, VolumeLSet: true, AbvPct: -5,
		},
		want: "abv_pct",
	}, {
		name: "nothing supplied at all",
		in:   &stillhousev1.AdoptOpeningInventoryRequest{AbvPct: 50},
		want: "mass_kg",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveAdoptedStock(tc.in)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("message %q does not mention %q", err.Error(), tc.want)
			}
			// And it must reach the caller as a bad request, not a server
			// fault: adopt's validation errors used to arrive as
			// code=internal, so the UI showed a generic failure and MCP
			// reported a Stillhouse bug.
			if got := connect.CodeOf(alcoholometryError(err)); got != connect.CodeInvalidArgument {
				t.Errorf("code = %v, want invalid_argument", got)
			}
		})
	}
}

// The one input that must still be accepted, so the guards above can't be
// satisfied by refusing everything.
func TestResolveAdoptedStockAcceptsAWeighedCharge(t *testing.T) {
	requireTables(t)
	got, err := resolveAdoptedStock(&stillhousev1.AdoptOpeningInventoryRequest{
		MassKg: 20135, MassKgSet: true,
		DensityKgM3: 922.6, DensityKgM3Set: true,
		TemperatureC: 20, TemperatureCSet: true,
	})
	if err != nil {
		t.Fatalf("CRA's own worked example was refused: %v", err)
	}
	if got.StrengthPct20C != 53.7 {
		t.Errorf("strength = %v, want 53.7", got.StrengthPct20C)
	}
}

func TestValidateFractionRejectsPercentages(t *testing.T) {
	// extract_fraction and moisture_fraction are fractions, and say so in
	// their names as of stage 169 — they used to be called _pct, which
	// were never range-checked. 78 typed for 0.78 gave a 1077% ABV wash.
	for _, v := range []float64{78, 1.5, -0.1, 100} {
		if err := validateFraction("extract_fraction", v); err == nil {
			t.Errorf("validateFraction(%v) = nil, want an error", v)
		}
	}
	for _, v := range []float64{0, 0.78, 1} {
		if err := validateFraction("extract_fraction", v); err != nil {
			t.Errorf("validateFraction(%v) = %v, want nil", v, err)
		}
	}
	if err := validateFraction("extract_fraction", 78); err == nil ||
		!strings.Contains(err.Error(), "0.78") {
		t.Errorf("message should show the operator what they meant, got %v", err)
	}
}

func TestValidateCapacity(t *testing.T) {
	// A negative capacity was accepted; a zero one was too, which then made
	// the overflow guard refuse every fill into that vessel.
	for _, v := range []float64{-5, 0} {
		if err := validateCapacityL(v); err == nil {
			t.Errorf("validateCapacityL(%v) = nil, want an error", v)
		}
	}
	if err := validateCapacityL(200); err != nil {
		t.Errorf("validateCapacityL(200) = %v, want nil", err)
	}
}
