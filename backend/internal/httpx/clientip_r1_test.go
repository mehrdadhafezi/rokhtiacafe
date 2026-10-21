package httpx

// R1 client-IP trust boundary tests (docs/SECURITY.md §5.1). Deterministic: no clocks, no timing.
// Synthetic addresses only:
//   192.0.2.0/24, 2001:db8:e::/48    CDN edges (trusted in r1WithEdges)
//   198.51.100.0/24, 2001:db8:1::/48 visitors (198.51.100.77 = a forged value)
//   203.0.113.0/24                   attackers connecting straight to the origin
// rokh's direct peer is LiteSpeed on 127.0.0.1. Chains assume LiteSpeed adds its own peer at the
// END of the header (vendor-documented, not yet observed on the server: R1 runtime acceptance).

import (
	"fmt"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/ratelimit"
)

var (
	r1Loopback  = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	r1WithEdges = []netip.Prefix{
		netip.MustParsePrefix("127.0.0.1/32"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("2001:db8:e::/48"),
	}
)

const r1Peer = "127.0.0.1:40000"

func r1Resolve(trusted []netip.Prefix, peer string, lines []string, extra map[string]string) Resolution {
	r := httptest.NewRequest("GET", "/api/v1/public/menu", nil)
	r.RemoteAddr = peer
	for _, l := range lines {
		r.Header.Add("X-Forwarded-For", l)
	}
	for k, v := range extra {
		r.Header.Set(k, v)
	}
	return ResolveClientIP(r, trusted)
}

// edges returns n distinct edge addresses, joined as one header value.
func edges(n int) string {
	e := make([]string, n)
	for i := range e {
		e[i] = fmt.Sprintf("192.0.2.%d", i+1)
	}
	return strings.Join(e, ", ")
}

type r1Case struct {
	id, name string
	trusted  []netip.Prefix
	peer     string
	lines    []string
	extra    map[string]string
	want     string
	source   IPSource
}

func runR1(t *testing.T, cases []r1Case) {
	t.Helper()
	for _, c := range cases {
		trusted, peer := c.trusted, c.peer
		if trusted == nil {
			trusted = r1WithEdges
		}
		if peer == "" {
			peer = r1Peer
		}
		got := r1Resolve(trusted, peer, c.lines, c.extra)
		if got.Addr.String() != c.want || got.Source != c.source {
			t.Errorf("%s %s: got %s/%s, want %s/%s", c.id, c.name, got.Addr, got.Source, c.want, c.source)
		}
	}
}

// Threat scenarios R1-T01…T13.
func TestR1Scenarios(t *testing.T) {
	runR1(t, []r1Case{
		{id: "T01", name: "CDN visitor", lines: []string{"198.51.100.10, 192.0.2.10"}, want: "198.51.100.10", source: SourceForwarded},
		{id: "T02", name: "CDN failed to strip a forged entry", lines: []string{"198.51.100.77, 198.51.100.10, 192.0.2.10"}, want: "198.51.100.10", source: SourceForwarded},
		{id: "T03", name: "direct origin, forged XFF, attacker appended", lines: []string{"198.51.100.77, 203.0.113.66"}, want: "203.0.113.66", source: SourceForwarded},
		{id: "T04", name: "direct origin claims a trusted edge", lines: []string{"198.51.100.77, 192.0.2.10, 203.0.113.66"}, want: "203.0.113.66", source: SourceForwarded},
		{id: "T06", name: "missing XFF", want: "127.0.0.1", source: SourceFallbackMissing},
		{id: "T06b", name: "blank XFF lines only", lines: []string{"", " \t"}, want: "127.0.0.1", source: SourceFallbackMissing},
		{id: "T07a", name: "malformed only entry", lines: []string{"invalid"}, want: "127.0.0.1", source: SourceFallbackMalformed},
		{id: "T07b", name: "trailing comma", lines: []string{"198.51.100.77,"}, want: "127.0.0.1", source: SourceFallbackMalformed},
		{id: "T07c", name: "garbage right-most, forged left", lines: []string{"198.51.100.77, garbage"}, want: "127.0.0.1", source: SourceFallbackMalformed},
		{id: "T07d", name: "garbage behind a verified edge falls back to that edge", lines: []string{"198.51.100.77, garbage, 192.0.2.10"}, want: "192.0.2.10", source: SourceFallbackMalformed},
		{id: "T07e", name: "zoned IPv6 entry", lines: []string{"fe80::1%eth0"}, want: "127.0.0.1", source: SourceFallbackMalformed},
		{id: "T08", name: "MC_REAL_IP / X-Real-IP ignored", lines: []string{"203.0.113.66"},
			extra: map[string]string{"MC_REAL_IP": "198.51.100.77", "X-Real-IP": "198.51.100.77"}, want: "203.0.113.66", source: SourceForwarded},
		{id: "T10", name: "edge missing from the list (rotation) is the client, never a forged value",
			trusted: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("192.0.2.0/25")},
			lines:   []string{"198.51.100.10, 192.0.2.200"}, want: "192.0.2.200", source: SourceForwarded},
		{id: "T11a", name: "IPv6 visitor, IPv6 edge", lines: []string{"2001:db8:1::10, 2001:db8:e::1"}, want: "2001:db8:1::10", source: SourceForwarded},
		{id: "T11b", name: "IPv4-mapped visitor normalised", lines: []string{"::ffff:198.51.100.10, 192.0.2.10"}, want: "198.51.100.10", source: SourceForwarded},
		{id: "T11c", name: "bracketed IPv6 entry", lines: []string{"[2001:db8:1::10], 192.0.2.10"}, want: "2001:db8:1::10", source: SourceForwarded},
		{id: "T11d", name: "::1 peer is not trusted unless listed", peer: "[::1]:40000", lines: []string{"198.51.100.77"}, want: "::1", source: SourcePeer},
		{id: "T12", name: "untrusted direct peer: headers ignored", peer: "203.0.113.9:5555",
			lines: []string{"198.51.100.77, 192.0.2.10"}, extra: map[string]string{"MC_REAL_IP": "198.51.100.77"}, want: "203.0.113.9", source: SourcePeer},
		{id: "T13", name: "proxy replaced XFF with the edge only: the edge, not loopback", lines: []string{"192.0.2.10"}, want: "192.0.2.10", source: SourceFallbackProxyOnly},
		{id: "T13b", name: "CDN sends only edges: the right-most edge (the anchor)", lines: []string{"192.0.2.20, 192.0.2.10"}, want: "192.0.2.10", source: SourceFallbackProxyOnly},
		{id: "T13c", name: "loopback-only entries collapse to loopback (reported)", lines: []string{"127.0.0.1"}, want: "127.0.0.1", source: SourceFallbackProxyOnly},
	})
}

// F5: header-line order is significant; a split chain resolves like the combined one.
func TestR1MultiLineOrdering(t *testing.T) {
	runR1(t, []r1Case{
		{id: "M01", name: "one combined line", lines: []string{"198.51.100.10, 192.0.2.10"}, want: "198.51.100.10", source: SourceForwarded},
		{id: "M02", name: "same chain over two lines", lines: []string{"198.51.100.10", "192.0.2.10"}, want: "198.51.100.10", source: SourceForwarded},
		{id: "M03", name: "same chain over three lines", lines: []string{"198.51.100.77", "198.51.100.10", "192.0.2.10"}, want: "198.51.100.10", source: SourceForwarded},
		{id: "M04", name: "forged first line, real peer last line", lines: []string{"198.51.100.77", "203.0.113.66"}, want: "203.0.113.66", source: SourceForwarded},
		{id: "M05", name: "forged edge claim in first line, real peer last", lines: []string{"198.51.100.77, 192.0.2.10", "203.0.113.66"}, want: "203.0.113.66", source: SourceForwarded},
		// Order matters: if a proxy PREPENDED its peer, the forged value would be read as the
		// client. Go cannot tell which line the proxy wrote; the runtime acceptance must show
		// LiteSpeed writes last (R1-B2-RT-03). This case pins the documented limitation.
		{id: "M06", name: "UNSAFE if a proxy prepends: real peer first, forged last", lines: []string{"203.0.113.66", "198.51.100.77"}, want: "198.51.100.77", source: SourceForwarded},
		{id: "M07", name: "repeated identical lines", lines: []string{"198.51.100.10, 192.0.2.10", "198.51.100.10, 192.0.2.10"}, want: "198.51.100.10", source: SourceForwarded},
		{id: "M08", name: "empty lines are skipped", lines: []string{"", "198.51.100.10, 192.0.2.10", "  "}, want: "198.51.100.10", source: SourceForwarded},
		{id: "M09", name: "whitespace variations", lines: []string{"  198.51.100.10 ,\t192.0.2.10  "}, want: "198.51.100.10", source: SourceForwarded},
		{id: "M10", name: "whitespace-only entry inside a chain is malformed", lines: []string{"198.51.100.10, , 192.0.2.10"}, want: "192.0.2.10", source: SourceFallbackMalformed},
		{id: "M11", name: "invalid entry in the right-most line", lines: []string{"198.51.100.10", "nonsense"}, want: "127.0.0.1", source: SourceFallbackMalformed},
		{id: "M12", name: "invalid entry in an earlier line, after the boundary", lines: []string{"nonsense", "198.51.100.10, 192.0.2.10"}, want: "198.51.100.10", source: SourceForwarded},
		{id: "M13", name: "mixed: IPv6 visitor line, IPv4 edge line", lines: []string{"2001:db8:1::10", "192.0.2.10"}, want: "2001:db8:1::10", source: SourceForwarded},
		{id: "M14", name: "mixed: IPv4 visitor, IPv6 edge", lines: []string{"198.51.100.10, 2001:db8:e::1"}, want: "198.51.100.10", source: SourceForwarded},
		{id: "M15", name: "same visitor IP in two positions", lines: []string{"198.51.100.10, 192.0.2.10, 198.51.100.10"}, want: "198.51.100.10", source: SourceForwarded},
		{id: "M16", name: "same edge IP in two positions", lines: []string{"192.0.2.10, 198.51.100.10, 192.0.2.10"}, want: "198.51.100.10", source: SourceForwarded},
		{id: "M17", name: "MC_REAL_IP alongside a multi-line chain is ignored", lines: []string{"198.51.100.10", "192.0.2.10"},
			extra: map[string]string{"MC_REAL_IP": "198.51.100.77"}, want: "198.51.100.10", source: SourceForwarded},
	})

	// Equivalence: every split of a chain into consecutive lines resolves like the combined line.
	chain := []string{"198.51.100.77", "198.51.100.10", "192.0.2.20", "192.0.2.10"}
	combined := r1Resolve(r1WithEdges, r1Peer, []string{strings.Join(chain, ", ")}, nil)
	for mask := 0; mask < 1<<(len(chain)-1); mask++ {
		var lines []string
		cur := chain[0]
		for i := 1; i < len(chain); i++ {
			if mask&(1<<(i-1)) != 0 {
				lines = append(lines, cur)
				cur = chain[i]
			} else {
				cur += ", " + chain[i]
			}
		}
		lines = append(lines, cur)
		got := r1Resolve(r1WithEdges, r1Peer, lines, nil)
		if got.Addr != combined.Addr || got.Source != combined.Source || got.Crossed != combined.Crossed {
			t.Errorf("split %q: got %s/%s, combined gives %s/%s", lines, got.Addr, got.Source, combined.Addr, combined.Source)
		}
	}

	// Regression guard: first-line-only parsing (Header.Get) would return the forged value here.
	if got := r1Resolve(r1Loopback, r1Peer, []string{"198.51.100.77", "203.0.113.66"}, nil); got.Addr.String() == "198.51.100.77" {
		t.Error("only the first X-Forwarded-For line was read")
	}
}

// F3: the hop budget, at, below and beyond the limit. Pure input/output; no timing.
func TestR1HopBudget(t *testing.T) {
	const visitor = "198.51.100.10"
	cases := []struct {
		name    string
		lines   []string
		want    string
		source  IPSource
		crossed int
	}{
		{"one trusted hop", []string{visitor + ", " + edges(1)}, visitor, SourceForwarded, 1},
		{"visitor is exactly the last examined entry", []string{visitor + ", " + edges(MaxForwardedHops-1)}, visitor, SourceForwarded, MaxForwardedHops - 1},
		{"one hop beyond the budget", []string{visitor + ", " + edges(MaxForwardedHops)}, "192.0.2.16", SourceFallbackTooLong, MaxForwardedHops},
		{"far beyond the budget, forged value at the left", []string{"198.51.100.77, " + edges(250)}, "192.0.2.250", SourceFallbackTooLong, MaxForwardedHops},
		{"exactly the budget of trusted hops, nothing else: all examined", []string{edges(MaxForwardedHops)}, "192.0.2.16", SourceFallbackProxyOnly, MaxForwardedHops},
		{"budget minus one trusted hops, nothing else", []string{edges(MaxForwardedHops - 1)}, "192.0.2.15", SourceFallbackProxyOnly, MaxForwardedHops - 1},
		// Long but valid: entries left of the decision are never parsed, so their number and
		// content cannot change the answer.
		{"5000 forged entries left of the attacker the proxy appended", []string{strings.Repeat("198.51.100.77, ", 5000) + "203.0.113.66"}, "203.0.113.66", SourceForwarded, 0},
		{"5000 garbage entries left of a valid chain", []string{strings.Repeat("x, ", 5000) + visitor + ", 192.0.2.10"}, visitor, SourceForwarded, 1},
	}
	for _, c := range cases {
		got := r1Resolve(r1WithEdges, r1Peer, c.lines, nil)
		if got.Addr.String() != c.want || got.Source != c.source || got.Crossed != c.crossed {
			t.Errorf("%s: got %s/%s crossed %d, want %s/%s crossed %d", c.name, got.Addr, got.Source, got.Crossed, c.want, c.source, c.crossed)
		}
		if got.Addr.String() == "198.51.100.77" {
			t.Errorf("%s: forged value selected", c.name)
		}
	}
	// The same over-long chain split over one line per entry behaves identically.
	split := strings.Split(visitor+", "+edges(MaxForwardedHops), ", ")
	if got := r1Resolve(r1WithEdges, r1Peer, split, nil); got.Source != SourceFallbackTooLong || got.Addr.String() != "192.0.2.16" {
		t.Errorf("split over-long chain: got %s/%s", got.Addr, got.Source)
	}
}

// T09/T16: identity and rate-limit isolation.
func TestR1RateLimitIsolation(t *testing.T) {
	a := r1Resolve(r1WithEdges, r1Peer, []string{"198.51.100.10, 192.0.2.10"}, nil).Addr
	b := r1Resolve(r1WithEdges, r1Peer, []string{"198.51.100.11, 192.0.2.10"}, nil).Addr
	if a == b {
		t.Errorf("T09: two visitors behind one edge share identity %s", a)
	}
	a = r1Resolve(r1Loopback, r1Peer, []string{"198.51.100.10, 192.0.2.10"}, nil).Addr
	b = r1Resolve(r1Loopback, r1Peer, []string{"198.51.100.11, 192.0.2.10"}, nil).Addr
	if a.String() != "192.0.2.10" || a != b {
		t.Errorf("T09 loopback-only trust: expected both to be the edge, got %s and %s", a, b)
	}

	lim := ratelimit.New(2, time.Minute) // a fixed window: the test does not depend on elapsed time
	allow := func(lines ...string) bool {
		ok, _ := lim.Allow(r1Resolve(r1WithEdges, r1Peer, lines, nil).Addr.String())
		return ok
	}
	for i := 0; i < 2; i++ {
		if !allow("198.51.100.10, 192.0.2.10") || !allow("198.51.100.11, 192.0.2.10") {
			t.Fatal("T16: a visitor was limited by another visitor's traffic")
		}
	}
	allowed := 0
	for i := 0; i < 10; i++ {
		if allow(fmt.Sprintf("198.51.100.%d", 100+i), "203.0.113.66") { // forged line + real peer line
			allowed++
		}
	}
	if allowed != 2 {
		t.Errorf("T16: rotating forged XFF from one attacker got %d requests through, want 2", allowed)
	}
}

// T14: if LiteSpeed does NOT add its peer, Go cannot tell a forged header from a real one. This
// pins the limitation; only the runtime acceptance can exclude it.
func TestR1KnownLimitationNoAppend(t *testing.T) {
	if got := r1Resolve(r1WithEdges, r1Peer, []string{"198.51.100.77"}, nil); got.Addr.String() != "198.51.100.77" || got.Source != SourceForwarded {
		t.Errorf("T14 limitation changed: got %s/%s", got.Addr, got.Source)
	}
}
