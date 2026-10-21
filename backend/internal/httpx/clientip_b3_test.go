package httpx

// R1-B3 tests: the fallback anchor (adversarial cases A–H) and consistent telemetry snapshots.
// Deterministic: no sleeps, no clocks; concurrency is checked through invariants that must hold
// for every interleaving.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// Addresses: 192.0.2.10 = the edge LiteSpeed actually accepted (written last by LiteSpeed);
// 192.0.2.99 = an attacker-written address that happens to be in the trusted edge range;
// 198.51.100.77 = an attacker-written visitor; 203.0.113.66 = the attacker's real address.
const (
	b3RealEdge   = "192.0.2.10"
	b3ForgedEdge = "192.0.2.99"
	b3Forged     = "198.51.100.77"
	b3Attacker   = "203.0.113.66"
)

func TestB3FallbackAnchorAdversarial(t *testing.T) {
	cases := []struct {
		id, name string
		lines    []string // as received by rokh from LiteSpeed (peer 127.0.0.1, trusted)
		attacker string   // which entries the attacker wrote
		want     string
		source   IPSource
		spoofOK  bool // true only where Go cannot prevent it (LiteSpeed precondition broken)
	}{
		{"A", "direct origin: malformed, forged edge, real peer",
			[]string{"garbage, " + b3ForgedEdge + ", " + b3Attacker}, "garbage, forged edge", b3Attacker, SourceForwarded, false},
		{"B", "direct origin: forged visitor, forged edge, real peer",
			[]string{b3Forged + ", " + b3ForgedEdge + ", " + b3Attacker}, "visitor, forged edge", b3Attacker, SourceForwarded, false},
		{"C", "CDN: malformed visitor entry, real edge",
			[]string{"garbage, " + b3RealEdge}, "none (CDN wrote garbage)", b3RealEdge, SourceFallbackMalformed, false},
		{"D", "direct origin: repeated trusted edges, real peer",
			[]string{b3RealEdge + ", " + b3RealEdge + ", " + b3Attacker}, "both edge entries", b3Attacker, SourceForwarded, false},
		{"D2", "CDN: repeated edges only",
			[]string{b3ForgedEdge + ", " + b3ForgedEdge + ", " + b3RealEdge}, "first two (if CDN did not strip)", b3RealEdge, SourceFallbackProxyOnly, false},
		{"E", "only trusted addresses",
			[]string{"192.0.2.20, " + b3RealEdge}, "first (if CDN did not strip)", b3RealEdge, SourceFallbackProxyOnly, false},
		// The B3 correction: with a CDN that fails to strip client entries, B2 picked the
		// attacker-written in-range address as the identity; the anchor ignores header claims.
		{"E2", "CDN failed to strip: forged in-range edge, real edge",
			[]string{b3ForgedEdge + ", " + b3RealEdge}, "forged edge", b3RealEdge, SourceFallbackProxyOnly, false},
		{"E3", "CDN failed to strip: forged visitor, garbage, forged edge, real edge",
			[]string{b3Forged + ", garbage, " + b3ForgedEdge + ", " + b3RealEdge}, "all but the last", b3RealEdge, SourceFallbackMalformed, false},
		{"F", "direct origin: > 16 entries incl. forged edges, real peer right-most",
			[]string{"garbage, " + b3Forged + ", " + edges(20) + ", " + b3Attacker}, "all but the last", b3Attacker, SourceForwarded, false},
		{"F2", "CDN failed to strip: > 16 forged in-range entries, real edge",
			[]string{b3Forged + ", " + edges(20) + ", " + b3RealEdge}, "all but the last", b3RealEdge, SourceFallbackTooLong, false},
		// H: LiteSpeed did NOT append its peer. Every entry is attacker-written and Go cannot tell:
		// these pin the limitation; production stays NO-GO until the runtime check rules it out.
		{"H1", "no append: forged visitor only", []string{b3Forged}, "all", b3Forged, SourceForwarded, true},
		{"H2", "no append: forged in-range edge only", []string{b3ForgedEdge}, "all", b3ForgedEdge, SourceFallbackProxyOnly, true},
	}
	for _, c := range cases {
		got := r1Resolve(r1WithEdges, r1Peer, c.lines, nil)
		if got.Addr.String() != c.want || got.Source != c.source {
			t.Errorf("%s %s: got %s/%s, want %s/%s (attacker wrote: %s)", c.id, c.name, got.Addr, got.Source, c.want, c.source, c.attacker)
		}
		if !c.spoofOK && (got.Addr.String() == b3Forged || got.Addr.String() == b3ForgedEdge) {
			t.Errorf("%s: an attacker-written address was selected", c.id)
		}
		// G: the same chain with one entry per header line resolves identically.
		split := strings.Split(strings.Join(c.lines, ","), ",")
		if alt := r1Resolve(r1WithEdges, r1Peer, split, nil); alt.Addr != got.Addr || alt.Source != got.Source || alt.Crossed != got.Crossed {
			t.Errorf("%s: split lines give %+v, combined %+v", c.id, alt, got)
		}
	}
}

// Every fallback identity is the anchor: the right-most entry when trusted, otherwise the peer.
func TestB3FallbackIsAlwaysTheAnchor(t *testing.T) {
	chains := [][]string{
		{"garbage"}, {"garbage, " + b3RealEdge}, {b3ForgedEdge + ", garbage, " + b3RealEdge},
		{edges(30)}, {b3Forged + ", " + edges(MaxForwardedHops)}, {"192.0.2.1, 192.0.2.2, 192.0.2.3"},
		{"2001:db8:e::5, garbage, 2001:db8:e::1"},
	}
	for _, lines := range chains {
		got := r1Resolve(r1WithEdges, r1Peer, lines, nil)
		if !got.Source.Fallback() {
			t.Fatalf("%q: expected a fallback, got %s", lines, got.Source)
		}
		joined := strings.Join(lines, ",")
		last := strings.TrimSpace(joined[strings.LastIndexByte(joined, ',')+1:])
		want := "127.0.0.1"
		if a := parseHop(last); a.IsValid() && inPrefixes(a, r1WithEdges) {
			want = a.String()
		}
		if got.Addr.String() != want {
			t.Errorf("%q: fallback %s, want anchor %s", lines, got.Addr, want)
		}
	}
}

func TestB3StatsSerial(t *testing.T) {
	var s ClientIPStats
	for i := 0; i < 3; i++ {
		s.Record(Resolution{Addr: netip.MustParseAddr("198.51.100.10"), Source: SourceForwarded, Crossed: 1, Lines: 2})
	}
	s.Record(Resolution{Addr: netip.MustParseAddr("127.0.0.1"), Source: SourceFallbackMissing})
	c := s.Take()
	if c.Total != 4 || c.BySource["forwarded"] != 3 || c.BySource["fallback_missing"] != 1 || c.MultiLine != 3 || c.Loopback != 1 || c.Direct != 0 {
		t.Errorf("serial counts: %+v", c)
	}
	if e := s.Take(); e.Total != 0 || e.Fallbacks() != 0 {
		t.Errorf("empty interval not empty: %+v", e)
	}
}

// Conservation under concurrency: whatever the interleaving, every snapshot is internally
// consistent and the snapshots together hold every recorded event exactly once.
func TestB3StatsConcurrentConservation(t *testing.T) {
	var s ClientIPStats
	kinds := []Resolution{
		{Addr: netip.MustParseAddr("198.51.100.10"), Source: SourceForwarded, Crossed: 1},
		{Addr: netip.MustParseAddr(b3Attacker), Source: SourceForwarded, Lines: 2},
		{Addr: netip.MustParseAddr("127.0.0.1"), Source: SourceFallbackMalformed},
		{Addr: netip.MustParseAddr(b3RealEdge), Source: SourceFallbackProxyOnly, Crossed: 1},
	}
	const workers, per = 8, 4000
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start // barrier: all recorders begin together
			for i := 0; i < per; i++ {
				s.Record(kinds[(w+i)%len(kinds)])
			}
		}(w)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	close(start)

	var snaps []ClientIPCounts
	for finished := false; !finished; {
		select {
		case <-done:
			finished = true
		default:
		}
		snaps = append(snaps, s.Take())
	}
	snaps = append(snaps, s.Take()) // everything recorded after the last in-loop Take

	var total uint64
	perSource := map[string]uint64{}
	var multi, direct, loop uint64
	for i, c := range snaps {
		var sum uint64
		for k, v := range c.BySource {
			sum += v
			perSource[k] += v
		}
		if sum != c.Total {
			t.Fatalf("snapshot %d: Total %d != sum of sources %d", i, c.Total, sum)
		}
		if c.MultiLine > c.Total || c.Direct > c.Total || c.Loopback > c.Total {
			t.Fatalf("snapshot %d: flag count exceeds total: %+v", i, c)
		}
		total += c.Total
		multi += c.MultiLine
		direct += c.Direct
		loop += c.Loopback
	}
	n := uint64(workers * per)
	if total != n {
		t.Fatalf("conservation: snapshots hold %d events, recorded %d", total, n)
	}
	each := n / uint64(len(kinds)) // workers*per is a multiple of len(kinds) by construction
	want := map[string]uint64{"forwarded": 2 * each, "fallback_malformed": each, "fallback_proxy_only": each}
	for k, v := range want {
		if perSource[k] != v {
			t.Errorf("source %s: %d, want %d", k, perSource[k], v)
		}
	}
	if multi != each || direct != each || loop != each {
		t.Errorf("flags: multi=%d direct=%d loopback=%d, want %d each", multi, direct, loop, each)
	}
	if e := s.Take(); e.Total != 0 {
		t.Errorf("remaining after final Take: %d", e.Total)
	}
}

type b3SyncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *b3SyncBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *b3SyncBuffer) lines(t *testing.T) []map[string]any {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(w.b.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	w.b.Reset()
	return out
}

// Shutdown: Run flushes the last partial interval when ctx ends; a later Flush (main.go, after
// the server drained) logs only what came after, so nothing is lost or counted twice.
func TestB3StatsFinalFlush(t *testing.T) {
	var buf b3SyncBuffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	var s ClientIPStats
	for i := 0; i < 3; i++ {
		s.Record(Resolution{Addr: netip.MustParseAddr("198.51.100.10"), Source: SourceForwarded, Crossed: 1})
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx, logger, time.Hour); close(done) }() // the hour never elapses
	cancel()
	<-done
	got := buf.lines(t)
	if len(got) != 1 || got[0]["total"] != float64(3) {
		t.Fatalf("final flush: %v", got)
	}
	s.Flush(logger)
	if got := buf.lines(t); len(got) != 0 {
		t.Fatalf("idle flush logged: %v", got)
	}
	s.Record(Resolution{Addr: netip.MustParseAddr("127.0.0.1"), Source: SourceFallbackMissing})
	s.Flush(logger)
	got = buf.lines(t)
	if len(got) != 1 || got[0]["total"] != float64(1) || got[0]["source_fallback_missing"] != float64(1) {
		t.Fatalf("drain flush: %v", got)
	}
}
