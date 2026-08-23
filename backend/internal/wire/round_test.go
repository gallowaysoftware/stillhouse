package wire

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// QA finding F17, reproduced the way the code arrives at the figure rather
// than typed in as a literal — a test asserting on a hand-written
// 0.32549999999999996 proves only that the constant is spelled right.
//
// One 700 mL bottle at 46.5 % is 0.3255 LAA. recordRemoval computes it as
// bottles × mL ÷ 1000 × abv ÷ 100, and that lands one ulp low. Every
// removal, every bottling run and every gauge goes through arithmetic of
// the same shape.
func TestF17Figure(t *testing.T) {
	litres := float64(1) * 700 / 1000
	laa := litres * 46.5 / 100

	if strconv.FormatFloat(laa, 'g', -1, 64) == "0.3255" {
		t.Skip("this platform computes the LAA exactly; nothing to strip")
	}
	if got := strconv.FormatFloat(Round(laa), 'g', -1, 64); got != "0.3255" {
		t.Errorf("LAA renders as %s, want 0.3255", got)
	}

	// And the shape of any total summed across lines.
	if got := strconv.FormatFloat(Round(0.1+0.7+0.04), 'g', -1, 64); got != "0.84" {
		t.Errorf("summed total renders as %s, want 0.84", got)
	}
}

// Rounding must not coarsen a figure the domain actually states. The
// finest of them is an alcoholometric volume correction factor, quoted to
// five decimal places in the CRA tables.
func TestRoundKeepsDomainPrecision(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   float64
	}{
		{"volume factor", 0.99873},
		{"density kg/m3", 948.2371},
		{"specific gravity", 1.05234},
		{"unit cost CAD/kg", 0.5525},
		{"duty rate per LAA", 13.303},
		{"strength 20 °C", 43.125},
	} {
		if got := Round(tc.in); got != tc.in {
			t.Errorf("%s: %v was changed to %v", tc.name, tc.in, got)
		}
	}
}

// Values that cannot be meaningfully rounded pass through, because
// replacing a visible problem with an invisible one is worse than the
// problem.
func TestRoundLeavesTheUnroundable(t *testing.T) {
	if got := Round(math.NaN()); !math.IsNaN(got) {
		t.Errorf("NaN became %v", got)
	}
	if got := Round(math.Inf(1)); !math.IsInf(got, 1) {
		t.Errorf("+Inf became %v", got)
	}
	huge := 1.23456789012345e12
	if got := Round(huge); got != huge {
		t.Errorf("a value past the guard was changed: %v -> %v", huge, got)
	}
}

// Message has to reach nested messages and repeated fields, not just the
// top level — a B266 report carries its figures several layers down.
func TestMessageRoundsNestedAndRepeated(t *testing.T) {
	noisy := 0.1 + 0.2 // 0.30000000000000004
	msg := &stillhousev1.ListBulkContainersResponse{
		Summary: &stillhousev1.BulkSummary{TotalLaa: noisy},
		Containers: []*stillhousev1.BulkContainer{
			{Name: "Tank 1", CurrentLaa: noisy, CurrentVolumeL: noisy},
			{Name: "Tank 2", CurrentLaa: noisy},
		},
	}
	Message(msg)

	if got := msg.GetSummary().GetTotalLaa(); got != 0.3 {
		t.Errorf("nested summary LAA = %v, want 0.3", got)
	}
	for _, c := range msg.GetContainers() {
		if c.GetCurrentLaa() != 0.3 {
			t.Errorf("%s: repeated-field LAA = %v, want 0.3", c.GetName(), c.GetCurrentLaa())
		}
	}

	// And the point of the exercise: what a caller actually reads.
	b, err := protojson.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if s := string(b); containsNoise(s) {
		t.Errorf("residue survived into the JSON: %s", s)
	}
}

// Struct covers the MCP tools that assemble a plain Go struct from several
// proto responses and so never pass through Message.
func TestStructRoundsPlainGoValues(t *testing.T) {
	type dashboard struct {
		TotalBulkLAA   float64 `json:"total_bulk_laa"`
		TotalBarrelLAA float64 `json:"total_barrel_laa"`
		Count          int     `json:"count"`
		Nested         *struct {
			Litres float64 `json:"litres"`
		} `json:"nested"`
		Each []float64 `json:"each"`
	}
	noisy := 0.1 + 0.2
	d := &dashboard{TotalBulkLAA: noisy, TotalBarrelLAA: noisy, Count: 3, Each: []float64{noisy}}
	d.Nested = &struct {
		Litres float64 `json:"litres"`
	}{Litres: noisy}

	Struct(d)
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if s := string(b); containsNoise(s) {
		t.Errorf("residue survived into the JSON: %s", s)
	}
	if d.Count != 3 {
		t.Errorf("a non-float field was disturbed: %d", d.Count)
	}
}

// Nil and non-pointer inputs must not panic — Struct is called on
// whatever an MCP tool hands it.
func TestStructToleratesAwkwardInput(t *testing.T) {
	if got := Struct(nil); got != nil {
		t.Errorf("Struct(nil) = %v", got)
	}
	type v struct{ L float64 }
	out, ok := Struct(v{L: 0.1 + 0.2}).(v)
	if !ok {
		t.Fatal("Struct on a value did not return the same type")
	}
	if out.L != 0.3 {
		t.Errorf("value-typed struct = %v, want 0.3", out.L)
	}
	var nilPtr *v
	if got := Struct(nilPtr); got == nil {
		t.Error("Struct on a typed nil pointer should return it unchanged")
	}
}

// containsNoise looks for the signature of a float64 rendered at full
// precision: a decimal point followed by more than Places digits.
func containsNoise(s string) bool {
	digits := 0
	afterDot := false
	for _, r := range s {
		switch {
		case r == '.':
			afterDot, digits = true, 0
		case r >= '0' && r <= '9':
			if afterDot {
				digits++
				if digits > Places {
					return true
				}
			}
		default:
			afterDot, digits = false, 0
		}
	}
	return false
}
