package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/api"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/ratelimit"
)

// R1 proxy-chain simulation (docs/R1-R2-PRODUCTION-READINESS.md §1, docs/SECURITY.md §5.1).
//
// Real HTTP over real TCP: visitor → CDN edge proxy → "LiteSpeed" proxy → rokh. Every hop is a
// separate server on its own loopback address and dials the next hop FROM its own address, so each
// hop sees a genuine TCP peer (127.0.0.0/8 is all loopback on Linux and Windows). Each proxy maps
// the loopback source it sees to a documentation address, as if it were on the public internet:
//
//	127.0.2.N → 192.0.2.N      CDN edges in the published list (trusted when edges are configured)
//	127.0.6.N → 192.0.3.N      a CDN edge NOT yet in the list (rotation)
//	127.0.3.N → 198.51.100.N   IPv4 visitors
//	127.0.5.N → 2001:db8:1:5::N   IPv6 visitors sharing one /64
//	127.0.7.N → 2001:db8:1:7::N   IPv6 visitor in another /64
//	127.0.4.N → 203.0.113.N    attackers connecting straight to the origin, bypassing the CDN
//
// The edge appends its peer to X-Forwarded-For (the vendor behaviour assumed). "LiteSpeed" runs
// in every forwarding mode a proxy might implement. Only the runtime gate R1-B can tell which one
// the real LiteSpeed uses; these tests show what rokh does in each.

type xffMode string

const (
	modeAppend     xffMode = "append"      // peer appended at the end of one combined line (safe)
	modeAppendLine xffMode = "append-line" // peer appended as a new last header line (safe)
	modePreserve   xffMode = "preserve"    // header passed through unchanged (UNSAFE)
	modePrepend    xffMode = "prepend"     // peer written first (UNSAFE)
	modeReplace    xffMode = "replace"     // header replaced by the peer (safe, coarse)
	modeStrip      xffMode = "strip"       // header removed (safe, one global bucket)
)

var simMap = []struct {
	from netip.Prefix
	to   func(b byte) netip.Addr
}{
	{netip.MustParsePrefix("127.0.2.0/24"), func(b byte) netip.Addr { return netip.AddrFrom4([4]byte{192, 0, 2, b}) }},
	{netip.MustParsePrefix("127.0.6.0/24"), func(b byte) netip.Addr { return netip.AddrFrom4([4]byte{192, 0, 3, b}) }},
	{netip.MustParsePrefix("127.0.3.0/24"), func(b byte) netip.Addr { return netip.AddrFrom4([4]byte{198, 51, 100, b}) }},
	{netip.MustParsePrefix("127.0.4.0/24"), func(b byte) netip.Addr { return netip.AddrFrom4([4]byte{203, 0, 113, b}) }},
	{netip.MustParsePrefix("127.0.5.0/24"), func(b byte) netip.Addr { return netip.MustParseAddr(fmt.Sprintf("2001:db8:1:5::%x", b)) }},
	{netip.MustParsePrefix("127.0.7.0/24"), func(b byte) netip.Addr { return netip.MustParseAddr(fmt.Sprintf("2001:db8:1:7::%x", b)) }},
}

// public is the address a hop "really" has, as seen by the next hop.
func public(loopback netip.Addr) netip.Addr {
	for _, m := range simMap {
		if m.from.Contains(loopback) {
			return m.to(loopback.As4()[3])
		}
	}
	return loopback
}

type simProxy struct {
	mode     xffMode
	upstream string
	client   *http.Client
}

func (p *simProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ap, _ := netip.ParseAddrPort(r.RemoteAddr)
	peer := public(ap.Addr().Unmap()).String()
	out, _ := http.NewRequestWithContext(r.Context(), r.Method, p.upstream+r.URL.RequestURI(), r.Body)
	out.Header = r.Header.Clone()
	lines := r.Header.Values("X-Forwarded-For")
	switch p.mode {
	case modeAppend:
		out.Header.Set("X-Forwarded-For", strings.Join(append(lines, peer), ", "))
	case modeAppendLine:
		out.Header.Add("X-Forwarded-For", peer)
	case modePreserve:
	case modePrepend:
		out.Header.Set("X-Forwarded-For", strings.Join(append([]string{peer}, lines...), ", "))
	case modeReplace:
		out.Header.Set("X-Forwarded-For", peer)
	case modeStrip:
		out.Header.Del("X-Forwarded-For")
	}
	resp, err := p.client.Do(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// from returns an HTTP client whose connections originate from the given loopback address.
func from(addr string) *http.Client {
	d := &net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(addr)}, Timeout: 5 * time.Second}
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, n, a string) (net.Conn, error) { return d.DialContext(ctx, n, a) },
		// one connection per request: a new source address is a new TCP peer
		DisableKeepAlives: true,
	}}
}

// serveOn starts h on a fixed loopback address.
func serveOn(t *testing.T, addr string, h http.Handler) string {
	t.Helper()
	l, err := net.Listen("tcp", addr+":0")
	if err != nil {
		t.Skipf("cannot listen on %s (loopback range unavailable): %v", addr, err)
	}
	s := httptest.NewUnstartedServer(h)
	s.Listener = l
	s.Start()
	t.Cleanup(s.Close)
	return s.URL
}

// chain is one simulated production path in front of an origin URL (rokh).
type chain struct {
	liteSpeed string            // URL attackers can reach directly (the origin)
	edges     map[string]string // edge loopback address → URL
}

func newChain(t *testing.T, origin string, mode xffMode) *chain {
	t.Helper()
	c := &chain{edges: map[string]string{}}
	// LiteSpeed on the origin host dials rokh from 127.0.0.1 (rokh's only trusted peer).
	c.liteSpeed = serveOn(t, "127.0.0.1", &simProxy{mode: mode, upstream: origin, client: from("127.0.0.1")})
	for _, e := range []string{"127.0.2.10", "127.0.2.11", "127.0.6.12"} {
		c.edges[e] = serveOn(t, e, &simProxy{mode: modeAppend, upstream: c.liteSpeed, client: from(e)})
	}
	return c
}

// via sends GET path from a visitor address through an edge (edge "" = straight to the origin).
func (c *chain) via(t *testing.T, visitor, edge, path string, xff ...string) *http.Response {
	t.Helper()
	target := c.liteSpeed
	if edge != "" {
		target = c.edges[edge]
	}
	req, _ := http.NewRequest(http.MethodGet, target+path, nil)
	for _, v := range xff {
		req.Header.Add("X-Forwarded-For", v)
	}
	resp, err := from(visitor).Do(req)
	if err != nil {
		t.Fatalf("%s via %q: %v", visitor, edge, err)
	}
	return resp
}

var (
	loopbackOnly = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}                                        // production today
	withEdges    = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("192.0.2.0/24")} // after R1-B
)

const forged = "198.51.100.77"

// TestR1ProxyChainIdentity: what rokh resolves for every forwarding mode, trust setting and threat.
func TestR1ProxyChainIdentity(t *testing.T) {
	type tc struct {
		name           string
		visitor, edge  string
		xff            []string
		honestEdges    string // correct identity with edge ranges trusted
		honestLoopback string // correct identity with only 127.0.0.1 trusted (production today)
		attack         bool   // the request tries to impersonate 198.51.100.77
	}
	cases := []tc{
		{"visitor via edge", "127.0.3.1", "127.0.2.10", nil, "198.51.100.1", "192.0.2.10", false},
		{"visitor forges a leftmost address", "127.0.3.1", "127.0.2.10", []string{forged}, "198.51.100.1", "192.0.2.10", true},
		{"visitor forges victim + trusted edge", "127.0.3.1", "127.0.2.10", []string{forged + ", 192.0.2.99"}, "198.51.100.1", "192.0.2.10", true},
		{"visitor sends duplicate header lines", "127.0.3.1", "127.0.2.10", []string{forged, "192.0.2.99"}, "198.51.100.1", "192.0.2.10", true},
		{"malformed entries", "127.0.3.1", "127.0.2.10", []string{"not-an-ip, [fe80::1%eth0], 999.1.1.1"}, "198.51.100.1", "192.0.2.10", false},
		{"IPv6 visitor", "127.0.5.1", "127.0.2.11", nil, "2001:db8:1:5::1", "192.0.2.11", false},
		{"edge missing from the published list", "127.0.3.1", "127.0.6.12", nil, "192.0.3.12", "192.0.3.12", false},
		{"direct-origin attacker forges victim", "127.0.4.66", "", []string{forged}, "203.0.113.66", "203.0.113.66", true},
		{"direct-origin attacker forges victim + edge", "127.0.4.66", "", []string{forged + ", 192.0.2.10"}, "203.0.113.66", "203.0.113.66", true},
		{"direct-origin attacker, two lines", "127.0.4.66", "", []string{forged, "192.0.2.10"}, "203.0.113.66", "203.0.113.66", true},
		{"direct-origin request, no header", "127.0.4.66", "", nil, "203.0.113.66", "203.0.113.66", false},
	}
	var report []string
	for _, mode := range []xffMode{modeAppend, modeAppendLine, modeReplace, modeStrip, modePreserve, modePrepend} {
		for trustName, trust := range map[string][]netip.Prefix{"loopback-only": loopbackOnly, "with-edges": withEdges} {
			echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				res := httpx.ResolveClientIP(r, trust)
				_ = json.NewEncoder(w).Encode(map[string]string{"addr": res.Addr.String(), "source": res.Source.String()})
			}))
			ch := newChain(t, echo.URL, mode)
			forgedWins := 0
			for _, c := range cases {
				resp := ch.via(t, c.visitor, c.edge, "/", c.xff...)
				var got map[string]string
				_ = json.NewDecoder(resp.Body).Decode(&got)
				resp.Body.Close()
				honest := c.honestEdges
				if trustName == "loopback-only" {
					honest = c.honestLoopback
				}
				if c.attack && got["addr"] == forged {
					forgedWins++
				}
				switch mode {
				case modeAppend, modeAppendLine:
					if got["addr"] != honest {
						t.Errorf("%s/%s %s: rokh resolved %s (%s), want %s", mode, trustName, c.name, got["addr"], got["source"], honest)
					}
				case modeReplace, modeStrip:
					if got["addr"] == forged {
						t.Errorf("%s/%s %s: a forged address was accepted", mode, trustName, c.name)
					}
				}
				report = append(report, fmt.Sprintf("%-11s %-13s %-44s → %-16s %s", mode, trustName, c.name, got["addr"], got["source"]))
			}
			switch mode {
			case modePreserve, modePrepend:
				// The demonstration this suite exists for: with these LiteSpeed behaviours the
				// attacker chooses the identity, and nothing in rokh can detect it.
				if forgedWins == 0 {
					t.Errorf("%s/%s: expected the forged address to win (UNSAFE mode no longer demonstrated)", mode, trustName)
				}
			default:
				if forgedWins != 0 {
					t.Errorf("%s/%s: forged address accepted %d times", mode, trustName, forgedWins)
				}
			}
			echo.Close()
		}
	}
	t.Logf("resolution matrix (mode, TRUSTED_PROXIES, case → identity, source):\n%s", strings.Join(report, "\n"))
}

// TestR1ProxyChainRateLimits drives the real rokh API (GET /public/order, 3 per hour per IP here)
// through the simulated chain and counts how many requests each actor gets through.
func TestR1ProxyChainRateLimits(t *testing.T) {
	const limit = 3
	setup := func(t *testing.T, trust []netip.Prefix, mode xffMode) *chain {
		e := newEnv(t, func(d *api.Deps) {
			d.TrustedProxies = trust
			d.TrackPerIP = ratelimit.New(limit, time.Hour)
		})
		return newChain(t, e.srv.URL, mode)
	}
	// allowed sends n tracking requests and counts those not refused with 429.
	allowed := func(t *testing.T, n int, send func(i int) *http.Response) int {
		ok := 0
		for i := 0; i < n; i++ {
			resp := send(i)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusTooManyRequests {
				ok++
			}
		}
		return ok
	}
	track := "/api/v1/public/order"

	t.Run("append+edges: rotating forged headers cannot bypass the limit", func(t *testing.T) {
		ch := setup(t, withEdges, modeAppend)
		got := allowed(t, 10, func(i int) *http.Response {
			return ch.via(t, "127.0.4.66", "", track, fmt.Sprintf("198.51.100.%d, 192.0.2.10", 100+i))
		})
		if got != limit {
			t.Errorf("direct-origin attacker got %d requests through, want %d", got, limit)
		}
		got = allowed(t, 10, func(i int) *http.Response {
			return ch.via(t, "127.0.3.1", "127.0.2.10", track, fmt.Sprintf("198.51.100.%d", 100+i))
		})
		if got != limit {
			t.Errorf("visitor rotating forged headers got %d through, want %d", got, limit)
		}
	})
	t.Run("append+edges: visitors behind one edge have separate buckets; edge rotation keeps identity", func(t *testing.T) {
		ch := setup(t, withEdges, modeAppend)
		v1 := allowed(t, 6, func(i int) *http.Response {
			return ch.via(t, "127.0.3.1", []string{"127.0.2.10", "127.0.2.11"}[i%2], track) // same visitor, two edges
		})
		v2 := allowed(t, 2, func(int) *http.Response { return ch.via(t, "127.0.3.2", "127.0.2.10", track) })
		if v1 != limit || v2 != 2 {
			t.Errorf("visitor 1 (rotating edges) %d (want %d), visitor 2 on the same edge %d (want 2)", v1, limit, v2)
		}
	})
	t.Run("append+edges: IPv6 rotation inside one /64 shares one bucket", func(t *testing.T) {
		ch := setup(t, withEdges, modeAppend)
		got := allowed(t, 10, func(i int) *http.Response {
			return ch.via(t, fmt.Sprintf("127.0.5.%d", i+1), "127.0.2.10", track)
		})
		other := allowed(t, 2, func(int) *http.Response { return ch.via(t, "127.0.7.1", "127.0.2.10", track) })
		if got != limit || other != 2 {
			t.Errorf("rotating /64 got %d (want %d), another /64 got %d (want 2)", got, limit, other)
		}
	})
	t.Run("append+loopback-only (production today): visitors behind one edge share a bucket", func(t *testing.T) {
		ch := setup(t, loopbackOnly, modeAppend)
		v1 := allowed(t, limit, func(int) *http.Response { return ch.via(t, "127.0.3.1", "127.0.2.10", track) })
		v2 := allowed(t, 2, func(int) *http.Response { return ch.via(t, "127.0.3.2", "127.0.2.10", track) })
		v3 := allowed(t, 2, func(int) *http.Response { return ch.via(t, "127.0.3.3", "127.0.2.11", track) })
		if v1 != limit || v2 != 0 || v3 != 2 {
			t.Errorf("v1 %d, v2 same edge %d (want 0: shared edge bucket), v3 other edge %d", v1, v2, v3)
		}
		forgedOK := allowed(t, 10, func(i int) *http.Response {
			return ch.via(t, "127.0.4.66", "", track, fmt.Sprintf("198.51.100.%d", 100+i))
		})
		if forgedOK != limit {
			t.Errorf("direct-origin attacker got %d through, want %d", forgedOK, limit)
		}
	})
	t.Run("strip: one global bucket (safe from spoofing, collapses everyone)", func(t *testing.T) {
		ch := setup(t, withEdges, modeStrip)
		v1 := allowed(t, limit, func(int) *http.Response { return ch.via(t, "127.0.3.1", "127.0.2.10", track) })
		v2 := allowed(t, 2, func(int) *http.Response { return ch.via(t, "127.0.3.9", "127.0.2.11", track) })
		if v1 != limit || v2 != 0 {
			t.Errorf("v1 %d, unrelated visitor %d (want 0: global bucket)", v1, v2)
		}
	})
	for _, mode := range []xffMode{modePreserve, modePrepend} {
		t.Run(string(mode)+"+edges: UNSAFE — rotating forged headers bypass the limit", func(t *testing.T) {
			ch := setup(t, withEdges, mode)
			got := allowed(t, 10, func(i int) *http.Response {
				return ch.via(t, "127.0.4.66", "", track, fmt.Sprintf("198.51.100.%d", 100+i))
			})
			if got <= limit {
				t.Errorf("expected the bypass to be demonstrated (got %d through)", got)
			}
			t.Logf("direct-origin attacker got %d of 10 requests through a %d/hour limit", got, limit)
		})
	}
}
