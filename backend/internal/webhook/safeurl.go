// Package webhook delivers outbound HTTP notifications to operator-
// supplied URLs.
//
// An operator-supplied URL that the server then fetches is a
// server-side request forgery primitive, and this package exists mostly
// to not be one.
package webhook

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrBlockedAddress is returned when a URL resolves somewhere the server
// must not be made to talk to.
var ErrBlockedAddress = errors.New("address is not routable on the public internet")

// ValidateURL checks what can be checked before a DNS lookup: the scheme,
// the shape, and a literal address if one was typed directly.
//
// This is the cheap half and it is NOT the protection. DNS resolves at
// request time, and an attacker who controls a name can point it at a
// public address for the validation and at 169.254.169.254 for the
// delivery — the DNS rebinding attack. What actually protects the server
// is the Control hook in SafeTransport, which checks every address the
// dialler is about to connect to, after resolution, on every attempt.
// This function exists so an operator who fat-fingers an internal
// hostname is told at registration rather than by silence.
func ValidateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("not a URL: %w", err)
	}
	if u.Scheme != "https" {
		// http would send the signature and the payload in clear. The
		// signature is what makes the delivery trustworthy, so sending it
		// where it can be read defeats the point of having one.
		return errors.New("must be an https URL")
	}
	if u.Host == "" {
		return errors.New("must have a host")
	}
	if u.User != nil {
		// Credentials in the URL would be stored in a column the operator
		// can read back, and they are not how this authenticates.
		return errors.New("must not contain credentials")
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if !publicIP(ip) {
			return ErrBlockedAddress
		}
	}
	return nil
}

// publicIP reports whether an address is one the server may be directed
// at. Everything private, local, or special-purpose is refused.
//
// The list is deliberately a denylist of address *properties* rather than
// of specific addresses: 169.254.169.254 is the famous one, but the
// property that makes it dangerous — link-local — is shared by everything
// in 169.254.0.0/16 and fe80::/10, and enumerating addresses would miss
// them.
func publicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() ||
		ip.IsUnspecified() {
		return false
	}
	// IPv4-mapped IPv6 (::ffff:127.0.0.1) passes the checks above as an
	// IPv6 address while connecting to an IPv4 one, so it is unwrapped
	// and judged as what it actually reaches.
	if v4 := ip.To4(); v4 != nil && !ip.Equal(v4) {
		return publicIP(v4)
	}
	// 100.64.0.0/10, carrier-grade NAT — not private by Go's definition
	// but not somewhere a webhook should be going either.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return false
	}
	// 0.0.0.0/8 and 240.0.0.0/4.
	if v4 := ip.To4(); v4 != nil && (v4[0] == 0 || v4[0] >= 240) {
		return false
	}
	// IPv6 unique-local (fc00::/7).
	if len(ip) == net.IPv6len && ip.To4() == nil && (ip[0]&0xfe) == 0xfc {
		return false
	}
	return true
}

// SafeTransport is an http.Transport whose dialler refuses to connect to
// any address that is not publicly routable.
//
// The check is in Control rather than before the dial because Control
// runs after DNS resolution, once per address actually attempted, with
// the address the socket is about to use. That closes the rebinding
// window: a name that resolved to something public a moment ago and
// resolves to 127.0.0.1 now is refused at the point it matters, not at
// the point it was registered.
//
// Redirects are refused entirely by the client below rather than
// re-validated, because a redirect is a second URL the operator never
// registered, and following one to an internal address would be the same
// bug arriving by a different route.
func SafeTransport() *http.Transport {
	d := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("%w: %q is not an IP", ErrBlockedAddress, host)
			}
			if !publicIP(ip) {
				return fmt.Errorf("%w: %s", ErrBlockedAddress, ip)
			}
			return nil
		},
	}
	return &http.Transport{
		DialContext:           d.DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     false,
		MaxIdleConnsPerHost:   2,
	}
}

// Client is the only http.Client this package delivers through.
func Client() *http.Client {
	return &http.Client{
		Transport: SafeTransport(),
		Timeout:   15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("webhook endpoints must not redirect")
		},
	}
}
