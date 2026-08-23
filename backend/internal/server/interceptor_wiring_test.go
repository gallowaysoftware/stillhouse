package server

import (
	"strings"
	"testing"
)

// The three interceptors are the whole of Stillhouse's per-request
// policy: who you are, what your role may call, and what a figure looks
// like on the way out. A handler mounted without one of them is not
// obviously broken — it authenticates, it answers, it just quietly
// stops applying a rule — so the wiring is asserted rather than
// remembered.
func TestEveryInterceptorIsWired(t *testing.T) {
	src := readSource(t, "server.go")
	block := between(src, "interceptors := connect.WithInterceptors(", ")\n")
	if block == "" {
		t.Fatal("could not find the interceptor list in server.go")
	}
	for _, want := range []string{
		"NewAuthInterceptor",
		"NewRoleGateInterceptor",
		"NewFloatRoundingInterceptor",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("%s is not in the interceptor list, so it applies to nothing", want)
		}
	}

	// Every ConnectRPC handler must be mounted with that list. One
	// mounted bare would authenticate through the session middleware and
	// then skip the role gate entirely.
	for _, line := range strings.Split(src, "\n") {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "mux.Handle(stillhousev1connect.New") {
			continue
		}
		if !strings.Contains(l, "interceptors") {
			t.Errorf("handler mounted without interceptors: %s", l)
		}
	}
}
