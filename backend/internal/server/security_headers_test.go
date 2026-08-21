package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The app shipped with no security response headers at all. These pin the
// ones that matter, and the dev/prod split on HSTS.
func TestSecurityHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, tc := range []struct {
		name string
		dev  bool
		want map[string]string
		// absent headers, checked by name
		absent []string
	}{{
		name: "production",
		dev:  false,
		want: map[string]string{
			"X-Content-Type-Options":    "nosniff",
			"X-Frame-Options":           "DENY",
			"Referrer-Policy":           "same-origin",
			"Strict-Transport-Security": "max-age=31536000",
		},
	}, {
		name: "dev omits HSTS",
		dev:  true,
		want: map[string]string{
			"X-Frame-Options": "DENY",
		},
		// Asserting HSTS over plain http on a LAN would pin the browser
		// to a scheme the deployment may not serve.
		absent: []string{"Strict-Transport-Security"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			securityHeaders(inner, tc.dev).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
			for k, v := range tc.want {
				if got := rec.Header().Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
			for _, k := range tc.absent {
				if got := rec.Header().Get(k); got != "" {
					t.Errorf("%s = %q, want it unset in dev", k, got)
				}
			}
		})
	}
}

// The clickjacking case is the concrete one: /settings carries "delete
// distillery" (a cascading hard delete) and the B266 submit button.
func TestCSPForbidsFraming(t *testing.T) {
	rec := httptest.NewRecorder()
	securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), false).
		ServeHTTP(rec, httptest.NewRequest("GET", "/settings", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP does not forbid framing: %q", csp)
	}
	for _, directive := range []string{"default-src 'self'", "object-src 'none'", "base-uri 'none'"} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP missing %q: %q", directive, csp)
		}
	}
	// The app loads nothing from anywhere else; a script-src that allowed
	// inline would give up most of the value.
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Error("script-src allows unsafe-inline")
	}
}
