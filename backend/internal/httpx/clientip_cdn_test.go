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
		// left: with the edges trusted, every visitor falls back to the peer, 127.0.0.1.
		{"proxy replaces the header", loopbackOnly, edge, nil, edge},
		{"proxy replaces the header, edges trusted", withEdges, edge, nil, "127.0.0.1"},
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
