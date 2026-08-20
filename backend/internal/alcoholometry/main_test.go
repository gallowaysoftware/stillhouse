package alcoholometry

import (
	"os"
	"testing"
)

// The tables aren't in the repo — they're Crown material the operator
// downloads (see load.go). Tests that need them are skipped unless
// ALC_TAB points at the ZIP or the extracted ALC_TAB.TXT. CI downloads it
// so the conformance examples run on every push; a contributor without
// the file still gets a green, if narrower, `go test ./...`.
const alcTabEnv = "ALC_TAB"

// skipReason is empty once the tables are in memory, and otherwise says
// why they aren't.
var skipReason = alcTabEnv + " is not set"

func TestMain(m *testing.M) {
	if path := os.Getenv(alcTabEnv); path != "" {
		if err := Load(path); err != nil {
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
