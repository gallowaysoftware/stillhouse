package alcoholometry

import (
	"fmt"
	"os"
	"testing"
)

// The tables aren't in the repo — they're Crown material the operator
// downloads (see load.go). Tests that need them are skipped unless
// ALC_TAB points at the ZIP or the extracted ALC_TAB.TXT. CI downloads it
// so the conformance examples run on every push; a contributor without
// the file still gets a green, if narrower, `go test ./...`.
const alcTabEnv = "ALC_TAB"

var loadErr = fmt.Errorf("%s is not set", alcTabEnv)

func TestMain(m *testing.M) {
	if path := os.Getenv(alcTabEnv); path != "" {
		loadErr = Load(path)
	}
	os.Exit(m.Run())
}

func requireTables(t *testing.T) {
	t.Helper()
	if loadErr != nil {
		t.Skipf("alcoholometric tables unavailable: %v", loadErr)
	}
}
