package rpc

import (
	"errors"
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
var alcTabErr = errors.New("ALC_TAB is not set")

func TestMain(m *testing.M) {
	if path := os.Getenv("ALC_TAB"); path != "" {
		alcTabErr = alcoholometry.Load(path)
	}
	os.Exit(m.Run())
}

func requireTables(t *testing.T) {
	t.Helper()
	if alcTabErr != nil {
		t.Skipf("alcoholometric tables unavailable: %v", alcTabErr)
	}
}
