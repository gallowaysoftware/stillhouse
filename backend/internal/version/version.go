// Package version carries what this build is, so an operator can answer
// "what is running?" without guessing from a container digest.
//
// The values are set at link time (see the Makefile's LDFLAGS). A build
// made without them — go run, go test, a plain go build — reports
// "dev", which is the honest answer for one.
package version

import (
	"runtime/debug"
	"strings"
)

var (
	// Version is the release tag this was built from, e.g. "v0.156.0".
	// "dev" for anything not built by `make release`.
	Version = "dev"
	// Commit is the short git SHA. Filled from the build info embedded by
	// the Go toolchain when the linker didn't set it.
	Commit = ""
	// BuildDate is RFC3339, set at link time. Empty for a local build.
	BuildDate = ""
)

func init() {
	if Commit != "" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			Commit = s.Value
			if len(Commit) > 12 {
				Commit = Commit[:12]
			}
		}
	}
}

// String is the one-line form: what a log line and the settings screen
// both show.
func String() string {
	var b strings.Builder
	b.WriteString(Version)
	if Commit != "" {
		b.WriteString(" (")
		b.WriteString(Commit)
		b.WriteString(")")
	}
	if BuildDate != "" {
		b.WriteString(" built ")
		b.WriteString(BuildDate)
	}
	return b.String()
}

// IsRelease reports whether this build came from a tagged release rather
// than somebody's working tree. Hosted installs should only ever run a
// release; the runbook says so and /version makes it checkable.
func IsRelease() bool { return Version != "dev" && Version != "" }
