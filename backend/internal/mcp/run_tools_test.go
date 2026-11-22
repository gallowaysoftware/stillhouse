package mcp

import (
	"math"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	pb "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/rpc"
)

// Every status the API defines has a name, and nothing else does: an
// unrecognised word must error rather than move a run somewhere.
func TestEnumFromName(t *testing.T) {
	checkEnum[pb.MashStatus](t)
	checkEnum[pb.FermentationStatus](t)
	checkEnum[pb.DistillationStatus](t)
	checkEnum[pb.DistillationCutKind](t)
	checkEnum[pb.MaterialKind](t)
	checkEnum[pb.Cereal](t)
	checkEnum[pb.SpiritKind](t)

	if got, err := enumFromName[pb.MashStatus](" In Progress "); err != nil || got != pb.MashStatus_MASH_STATUS_IN_PROGRESS {
		t.Errorf("in progress: %v, %v", got, err)
	}
	if got, err := enumFromName[pb.MashStatus]("canceled"); err != nil || got != pb.MashStatus_MASH_STATUS_CANCELLED {
		t.Errorf("canceled: %v, %v", got, err)
	}
}

func checkEnum[E protoEnum](t *testing.T) {
	t.Helper()
	for _, bad := range []string{"", "paused", "unspecified"} {
		if got, err := enumFromName[E](bad); err == nil || got != 0 {
			t.Errorf("%T: %q was accepted as %v", got, bad, got)
		}
	}
	var zero E
	values := zero.Descriptor().Values()
	prefix := strings.TrimSuffix(string(values.ByNumber(0).Name()), "UNSPECIFIED")
	for i := range values.Len() {
		v := values.Get(i)
		if v.Number() == 0 {
			continue
		}
		got, err := enumFromName[E](strings.ToLower(strings.TrimPrefix(string(v.Name()), prefix)))
		if err != nil || got != E(v.Number()) {
			t.Errorf("%s gave %v, %v", v.Name(), got, err)
		}
	}
}

func TestPickNeverChoosesBetweenTwo(t *testing.T) {
	a := candidate{"a", "FV1 (mash 1)", []string{"FV1", "mash 1"}}
	b := candidate{"b", "FV1 (mash 2)", []string{"FV1", "mash 2"}}
	c := candidate{"c", "FV2 (mash 3)", []string{"FV2", "mash 3"}}
	for _, tc := range []struct {
		name, ref string
		open      []candidate
		want      string
	}{
		{"nothing said, one open", "", []candidate{b}, "b"},
		{"nothing said, two open", "", []candidate{b, c}, ""},
		{"nothing said, none open", "", nil, ""},
		{"an open run wins over a finished namesake", "fv1", []candidate{b}, "b"},
		{"two open namesakes", "FV1", []candidate{a, b}, ""},
		{"a finished run by its own name", "Mash #1", []candidate{c}, "a"},
		{"no such thing", "FV9", []candidate{c}, ""},
		{"a fragment is not a name", "fv", []candidate{c}, ""},
		{"a uuid passes through", "0b0f3f0a-0000-4000-8000-000000000001", nil, "0b0f3f0a-0000-4000-8000-000000000001"},
	} {
		got, err := pick("fermentation", tc.ref, []candidate{a, b, c}, tc.open)
		if got != tc.want || (tc.want == "") != (err != nil) {
			t.Errorf("%s: got %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestObservedAt(t *testing.T) {
	now := time.Date(2026, 11, 22, 15, 0, 0, 0, time.UTC)
	if got, err := observedAt("", nil, now); got != nil || err != nil {
		t.Errorf("now: %v, %v", got, err)
	}
	if got, err := observedAt("", ptr(20.0), now); err != nil || !got.AsTime().Equal(now.Add(-20*time.Minute)) {
		t.Errorf("minutes_ago: %v, %v", got, err)
	}
	if got, err := observedAt("2026-11-22T10:30:00-04:00", nil, now); err != nil || !got.AsTime().Equal(now.Add(-30*time.Minute)) {
		t.Errorf("at: %v, %v", got, err)
	}
	for _, bad := range []struct {
		at  string
		ago *float64
	}{
		{"2026-11-22T10:30:00-04:00", ptr(5.0)}, {"10:30", nil}, {"2026-11-22T15:10:00Z", nil},
		{"2025-11-22T14:00:00Z", nil}, {"", ptr(-5.0)}, {"", ptr(1e9)}, {"", ptr(math.NaN())},
	} {
		if got, err := observedAt(bad.at, bad.ago, now); err == nil {
			t.Errorf("%q %v was accepted as %v", bad.at, bad.ago, got)
		}
	}
}

func TestFloorToolset(t *testing.T) {
	if _, ok := toolset("flor"); ok {
		t.Error("an unknown toolset was served")
	}
	names := func(only map[string]bool) map[string]*mcpsdk.Tool {
		ct, st := mcpsdk.NewInMemoryTransports()
		if _, err := newSessionServer(Deps{}, sqlcgen.User{}, only).Connect(t.Context(), st, nil); err != nil {
			t.Fatal(err)
		}
		cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "t", Version: "0"}, nil).Connect(t.Context(), ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = cs.Close() }()
		out := map[string]*mcpsdk.Tool{}
		for tool, err := range cs.Tools(t.Context(), nil) {
			if err != nil {
				t.Fatal(err)
			}
			out[tool.Name] = tool
		}
		return out
	}
	all, floor := names(nil), names(floorTools)
	for name := range floorTools {
		if floor[name] == nil {
			t.Errorf("the floor toolset names %s, which does not exist", name)
		}
	}
	if len(floor) != len(floorTools) {
		t.Errorf("floor serves %d tools, want %d", len(floor), len(floorTools))
	}
	for _, name := range []string{"fill_barrel", "dump_barrel", "regauge_barrel"} {
		if floor[name] != nil {
			t.Errorf("%s moves alcohol in the ledger and is in the floor toolset", name)
		}
	}
	if len(all) <= len(floor) {
		t.Errorf("the full set has %d tools and the floor %d", len(all), len(floor))
	}
	if a := all["get_mash"].Annotations; a == nil || !a.ReadOnlyHint {
		t.Error("get_mash is not marked read-only")
	}
	if a := all["start_mash"].Annotations; a != nil && a.ReadOnlyHint {
		t.Error("start_mash is marked read-only")
	}
}

// A viewer is the read-only role, so a read tool a viewer may not call is
// one that reaches a write.
func TestReadToolsAreReadOnly(t *testing.T) {
	b, err := os.ReadFile("tools_read.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`guard\(ctx, user, "([^"]+)"\)`).FindAllStringSubmatch(string(b), -1) {
		if err := rpc.AuthorizeProcedure(m[1], sqlcgen.UserRoleViewer); err != nil {
			t.Errorf("a read tool reaches %s, which a viewer may not call: %v", m[1], err)
		}
	}
}

// The figures an operator signs for stay in the web UI.
func TestSignedFiguresStayOffMCP(t *testing.T) {
	for _, proc := range toolProcedures(t) {
		for _, forbidden := range []string{"RecordProductionGauge", "VoidDistillationRun", "CreateBottlingRun", "CreateRemoval", "CreateInstrument", "GenerateB266"} {
			if strings.HasSuffix(proc, "/"+forbidden) {
				t.Errorf("%s reached the MCP surface", proc)
			}
		}
	}
}
