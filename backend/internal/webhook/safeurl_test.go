package webhook

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An operator-supplied URL that the server then fetches is a
// server-side request forgery primitive. These tests are the reason this
// package exists.

func TestValidateURL(t *testing.T) {
	for _, tc := range []struct {
		url string
		ok  bool
		why string
	}{
		{"https://example.com/hook", true, "an ordinary endpoint"},
		{"https://example.com:8443/hook", true, "a non-default port is fine"},

		{"http://example.com/hook", false, "http would send the signature in clear"},
		{"ftp://example.com/hook", false, "not http at all"},
		{"https://user:pw@example.com/h", false, "credentials in the URL"},
		{"", false, "empty"},
		{"https://", false, "no host"},

		// Literal addresses that must never be reachable.
		{"https://127.0.0.1/hook", false, "loopback"},
		{"https://[::1]/hook", false, "IPv6 loopback"},
		{"https://10.0.0.5/hook", false, "RFC1918"},
		{"https://192.168.1.1/hook", false, "RFC1918"},
		{"https://172.16.0.1/hook", false, "RFC1918"},
		{"https://169.254.169.254/latest/meta-data/", false, "the cloud metadata service"},
		{"https://[fe80::1]/hook", false, "IPv6 link-local"},
		{"https://[fd00::1]/hook", false, "IPv6 unique-local"},
		{"https://0.0.0.0/hook", false, "unspecified"},
		{"https://100.64.0.1/hook", false, "carrier-grade NAT"},
		{"https://[::ffff:127.0.0.1]/hook", false, "IPv4-mapped loopback"},
	} {
		err := ValidateURL(tc.url)
		if tc.ok && err != nil {
			t.Errorf("ValidateURL(%q) = %v, want ok (%s)", tc.url, err, tc.why)
		}
		if !tc.ok && err == nil {
			t.Errorf("ValidateURL(%q) = nil, want refused (%s)", tc.url, tc.why)
		}
	}
}

// The check that actually protects the server. ValidateURL runs once, at
// registration, against whatever DNS said then; this runs on every
// connection against the address the socket is about to use. A hostname
// that resolves to a public address at registration and to 127.0.0.1 at
// delivery — DNS rebinding — is stopped here and nowhere else.
func TestSafeTransportRefusesAfterResolution(t *testing.T) {
	// A real listener on loopback, reached by a name that passes
	// ValidateURL. This is the rebinding case, made concrete.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// srv.URL is http://127.0.0.1:PORT. Dial it through the safe
	// transport: resolution yields loopback and Control must refuse.
	c := &http.Client{Transport: SafeTransport()}
	_, err := c.Get(srv.URL)
	if err == nil {
		t.Fatal("the safe transport connected to loopback")
	}
	if !strings.Contains(err.Error(), ErrBlockedAddress.Error()) {
		t.Errorf("refused for the wrong reason: %v", err)
	}
}

// Redirects are a second URL the operator never registered. Following one
// would reintroduce the whole problem by another route.
func TestClientRefusesRedirects(t *testing.T) {
	c := Client()
	if c.CheckRedirect == nil {
		t.Fatal("no redirect policy")
	}
	if err := c.CheckRedirect(nil, nil); err == nil {
		t.Error("redirects are followed")
	}
}

func TestPublicIP(t *testing.T) {
	for _, tc := range []struct {
		ip   string
		want bool
	}{
		{"1.1.1.1", true},
		{"8.8.8.8", true},
		{"2606:4700:4700::1111", true},
		{"127.0.0.1", false},
		{"10.1.2.3", false},
		{"172.31.255.255", false},
		{"169.254.169.254", false},
		{"224.0.0.1", false},
		{"240.0.0.1", false},
		{"::1", false},
		{"fe80::1", false},
		{"fc00::1", false},
	} {
		if got := publicIP(net.ParseIP(tc.ip)); got != tc.want {
			t.Errorf("publicIP(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

var _ = errors.Is
