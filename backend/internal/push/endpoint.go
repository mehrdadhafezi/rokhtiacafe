// Package push implements Web Push for staff: subscriptions, endpoint safety and the leased
// notification outbox worker (docs/WORKFLOWS.md §4, docs/SECURITY.md §12).
package push

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// MaxEndpointLen is the longest accepted subscription endpoint (also a DB CHECK).
const MaxEndpointLen = 2048

// Allowlist holds push-service hosts: exact names, or ".suffix" for subdomains.
type Allowlist []string

// ParseAllowlist reads a comma-separated list; "*.example.com" is accepted as ".example.com".
func ParseAllowlist(s string) Allowlist {
	var out Allowlist
	for _, part := range strings.Split(s, ",") {
		p := strings.ToLower(strings.TrimSpace(part))
		p = strings.TrimPrefix(p, "*")
		if p != "" && p != "." {
			out = append(out, p)
		}
	}
	return out
}

// Allows reports whether host is a listed push service.
func (a Allowlist) Allows(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, entry := range a {
		if strings.HasPrefix(entry, ".") {
			if strings.HasSuffix(host, entry) && len(host) > len(entry) {
				return true
			}
		} else if host == entry {
			return true
		}
	}
	return false
}

// ErrEndpoint wraps every reason a subscription endpoint is refused.
var ErrEndpoint = errors.New("push endpoint not allowed")

// ValidateEndpoint checks an endpoint supplied by a browser before it is stored and again
// before every send: https on port 443, no userinfo, no IP literal, an allowlisted host.
func ValidateEndpoint(raw string, allow Allowlist) error {
	if raw == "" || len(raw) > MaxEndpointLen {
		return fmt.Errorf("%w: length", ErrEndpoint)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: malformed", ErrEndpoint)
	}
	switch {
	case u.Scheme != "https":
		return fmt.Errorf("%w: https required", ErrEndpoint)
	case u.User != nil:
		return fmt.Errorf("%w: userinfo", ErrEndpoint)
	case u.Port() != "" && u.Port() != "443":
		return fmt.Errorf("%w: port", ErrEndpoint)
	case u.Hostname() == "":
		return fmt.Errorf("%w: host", ErrEndpoint)
	}
	if _, err := netip.ParseAddr(u.Hostname()); err == nil {
		return fmt.Errorf("%w: IP address host", ErrEndpoint)
	}
	if !allow.Allows(u.Hostname()) {
		return fmt.Errorf("%w: host not on the push-service allowlist", ErrEndpoint)
	}
	return nil
}

// Reserved ranges that net/netip has no predicate for.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, broadcast
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64: may map to private IPv4
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001:db8::/32"), // documentation
}

// IsPublic reports whether ip is a globally routable unicast address the server may connect to.
func IsPublic(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// Resolver looks up host addresses (net.DefaultResolver in production; a fake in tests).
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// ErrUnsafeAddress is returned by the dialer for hosts resolving to non-public addresses.
var ErrUnsafeAddress = errors.New("push host resolves to a non-public address")

// SafeDialContext resolves the host itself, refuses it if any address is not public, and
// connects to the vetted address, so DNS rebinding cannot swap in an internal IP afterwards.
func SafeDialContext(r Resolver, d *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := r.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("push host %q has no addresses", host)
		}
		for _, ip := range ips {
			if !IsPublic(ip) {
				return nil, fmt.Errorf("%w (%s)", ErrUnsafeAddress, host)
			}
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

// SendTimeout bounds one delivery attempt (shorter than the outbox lease).
const SendTimeout = 10 * time.Second

// NewHTTPClient returns the client used for push services: SSRF-safe dialer, no proxy from the
// environment, no redirects, bounded timeouts.
func NewHTTPClient(r Resolver) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Client{
		Timeout: SendTimeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           SafeDialContext(r, dialer),
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: SendTimeout,
			MaxIdleConns:          8,
			IdleConnTimeout:       90 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
