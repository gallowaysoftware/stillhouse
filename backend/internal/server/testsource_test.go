package server

import (
	"os"
	"strings"
	"testing"
)

// Small helpers for tests that assert on the shape of source code rather
// than on runtime behaviour. Used where the failure mode is "somebody
// edited a list" and a live check would need a database to prove nothing.

func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	rest := s[i+len(start):]
	if j := strings.Index(rest, end); j >= 0 {
		return rest[:j]
	}
	return rest
}
