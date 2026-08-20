package rpc

import (
	"os"
	"testing"

	"github.com/gallowaysoftware/stillhouse/backend/internal/alcoholometry"
)

// The Canadian Alcoholometric Tables aren't in the repo — they're Crown
// material each operator downloads (see NOTICE). Tests that exercise
// temperature correction need a copy; point ALC_TAB at the CRA ZIP or the
// ALC_TAB.TXT inside it. CI does exactly that. Without it those tests
// skip, so a contributor who hasn't downloaded anything still gets a
// green `go test ./...`.
// skipReason is empty once the tables are in memory, and otherwise says
// why they aren't.
var skipReason = "ALC_TAB is not set"

func TestMain(m *testing.M) {
	if path := os.Getenv("ALC_TAB"); path != "" {
		if err := alcoholometry.Load(path); err != nil {
			skipReason = err.Error()
		} else {
			skipReason = ""
		}
	}
	os.Exit(m.Run())
}

func requireTables(t *testing.T) {
	t.Helper()
	if skipReason != "" {
		t.Skipf("alcoholometric tables unavailable: %s", skipReason)
	}
}
