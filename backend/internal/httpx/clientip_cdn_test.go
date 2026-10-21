package httpx

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

// TestClientIPBehindCDN documents the production chain (deploy/README.md §5, docs/SECURITY.md §5.1):
// visitor → MizbanCloud edge → LiteSpeed on the origin → rokh on 127.0.0.1. rokh's direct peer is
// always LiteSpeed (127.0.0.1); 192.0.2.0/24 stands in for the CDN edge ranges.
func TestClientIPBehindCDN(t *testing.T) {
	loopbackOnly := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	withEdges := append([]netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}, loopbackOnly...)
	const visitor, edge, attacker = "198.51.100.20", "192.0.2.10", "203.0.113.66"

	cases := []struct {
		name    string
		trusted []netip.Prefix
		xff     string
		extra   map[string]string
		want    string
	}{
		// LiteSpeed appends the edge address to the edge's header.
		{"loopback only: every visitor looks like the edge (shared rate-limit bucket)", loopbackOnly,
			visitor + ", " + edge, nil, edge},
		{"edge ranges trusted: the visitor", withEdges, visitor + ", " + edge, nil, visitor},
		{"visitor-supplied entries left of the edge's are ignored", withEdges,
			"6.6.6.6, " + visitor + ", " + edge, nil, visitor},
		// Someone bypasses the CDN and connects to the origin directly: LiteSpeed appends their address.
		{"direct to origin, forged edge entry", withEdges, visitor + ", " + edge + ", " + attacker, nil, attacker},
		{"direct to origin, forged header only", loopbackOnly, visitor + ", " + attacker, nil, attacker},
		// rokh never reads the CDN's alternative headers, whatever they say.
		{"MC_REAL_IP and X-Real-IP are ignored", loopbackOnly, attacker,
			map[string]string{"MC_REAL_IP": visitor, "X-Real-IP": visitor}, attacker},
		// If LiteSpeed replaced the header with its own peer instead of appending, only the edge is
		// left: with the edges trusted, visitors fall back to that verified edge (R1-B2), never to
		// a shared 127.0.0.1.
		{"proxy replaces the header", loopbackOnly, edge, nil, edge},
		{"proxy replaces the header, edges trusted", withEdges, edge, nil, edge},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "127.0.0.1:40000"
		r.Header.Set("X-Forwarded-For", c.xff)
		for k, v := range c.extra {
			r.Header.Set(k, v)
		}
		if got := ClientIP(r, c.trusted).String(); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// TestClientIPUpstreamFailureModes pins down what rokh does when the proxy in front of it does
// NOT sanitize or append correctly. These cases pass because they assert today's behaviour, and
// the two marked UNSAFE are spoofable: they are why production client-IP handling stays blocked until
// LiteSpeed's forwarding is measured (docs/HOSTING-ASSESSMENT.md R1). rokh can only be as
// trustworthy as the header it is handed by a trusted peer.
func TestClientIPUpstreamFailureModes(t *testing.T) {
	loopbackOnly := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	withEdges := append([]netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}, loopbackOnly...)
	const forged, edge = "198.51.100.77", "192.0.2.10"

	cases := []struct {
		name    string
		trusted []netip.Prefix
		lines   []string // X-Forwarded-For header lines as received by rokh, in order
		want    string
	}{
		// SPOOFABLE: the proxy forwards the visitor's header verbatim and appends nothing.
		{"UNSAFE: proxy passes a forged header through unchanged", loopbackOnly, []string{forged}, forged},
		// SPOOFABLE: a direct-to-origin request forges a trusted edge entry; the proxy appends nothing.
		{"UNSAFE: forged edge entry, nothing appended", withEdges, []string{forged + ", " + edge}, forged},
		// Fixed (R1-B2): a proxy that adds its peer as a second header line is honoured, because all
		// lines are combined in order (RFC 9110 §5.3); the forged first line no longer wins.
		{"real peer in a second header line wins over a forged first line", loopbackOnly, []string{forged, "203.0.113.5"}, "203.0.113.5"},
		// Not spoofable, but collapses visitors: a malformed right-most entry stops the walk.
		{"malformed right-most entry falls back to the peer", loopbackOnly, []string{forged + ", garbage"}, "127.0.0.1"},
		{"empty entry falls back to the peer", loopbackOnly, []string{forged + ", "}, "127.0.0.1"},
		{"entry with a port is read as its address", loopbackOnly, []string{"198.51.100.20:4444"}, "198.51.100.20"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "127.0.0.1:40000"
		for _, l := range c.lines {
			r.Header.Add("X-Forwarded-For", l)
		}
		if got := ClientIP(r, c.trusted).String(); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}

	// A peer that is not trusted is never overridden, whatever it sends: the only unconditional
	// guarantee rokh gives on its own.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:5000"
	r.Header.Set("X-Forwarded-For", forged)
	r.Header.Set("X-Real-IP", forged)
	if got := ClientIP(r, loopbackOnly).String(); got != "203.0.113.9" {
		t.Errorf("untrusted peer: got %s, want 203.0.113.9", got)
	}
}
