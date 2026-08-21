package mcp

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/rpc"
)

// The MCP server is mounted as a plain http.Handler, outside the
// ConnectRPC interceptor chain, so the role gate never runs for it. It
// authenticated the bearer token and called the service methods directly —
// and those methods carry no role checks of their own. A viewer could mint
// a token (IssueAPIToken is viewer-level by design so everyone can manage
// their own) and then fill, dump and regauge barrels through /mcp: writes
// that move dutiable alcohol onto the B266.
//
// Every tool now names the procedure it stands in for and goes through
// guard(). These tests keep it that way.

var procedureRE = regexp.MustCompile(`guard\(ctx, user, "([^"]+)"\)`)

func toolProcedures(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, f := range []string{"tools_read.go", "tools_write.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range procedureRE.FindAllStringSubmatch(string(b), -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// TestEveryToolIsGuarded: a handler that calls a service method without
// going through guard() is an ungated write. Counting withUser against
// guard catches a new tool copy-pasted from an old one.
func TestEveryToolIsGuarded(t *testing.T) {
	for _, f := range []string{"tools_read.go", "tools_write.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if n := strings.Count(string(b), "ctx = withUser(ctx, user)"); n > 0 {
			t.Errorf("%s has %d handler(s) calling withUser directly — they bypass the role "+
				"gate; use guard(ctx, user, <procedure>) instead", f, n)
		}
	}
	if len(toolProcedures(t)) == 0 {
		t.Fatal("no guarded tools found — the regexp is probably wrong")
	}
}

// TestToolProceduresAreRealAndClassified: a typo in a procedure string
// would sail through guard() only if the gate treated unknown procedures
// as allowed. It doesn't — it fails closed at owner — so a typo shows up
// as "this tool stopped working for operators". Catch it here instead.
func TestToolProceduresAreRealAndClassified(t *testing.T) {
	for _, proc := range toolProcedures(t) {
		// An operator must be able to run every tool the MCP surface
		// exposes: that is who the tools are for, wet hands at the still.
		if err := rpc.AuthorizeProcedure(proc, sqlcgen.UserRoleOperator); err != nil {
			t.Errorf("operator cannot call %s through MCP: %v", proc, err)
		}
	}
}

// TestViewerCannotWriteThroughMCP is the finding itself, stated as a test.
func TestViewerCannotWriteThroughMCP(t *testing.T) {
	writes := []string{
		"/stillhouse.v1.BarrelService/FillBarrel",
		"/stillhouse.v1.BarrelService/DumpBarrel",
		"/stillhouse.v1.BarrelService/RegaugeBarrel",
		"/stillhouse.v1.MashService/AddMashMetric",
		"/stillhouse.v1.FermentationService/AddFermentationLog",
		"/stillhouse.v1.RecipeService/SaveRecipeVersionSensory",
		"/stillhouse.v1.RecipeService/SaveRecipeVersionWhiskySensory",
	}
	for _, proc := range writes {
		if err := rpc.AuthorizeProcedure(proc, sqlcgen.UserRoleViewer); err == nil {
			t.Errorf("a viewer is allowed to call %s — read-only accounts must not move alcohol", proc)
		}
	}
}
