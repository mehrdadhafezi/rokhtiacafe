package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientIPStatsClassification(t *testing.T) {
	var s ClientIPStats
	for _, lines := range [][]string{
		{"198.51.100.10, 192.0.2.10"},   // forwarded via a trusted edge
		{"198.51.100.10", "192.0.2.10"}, // same, two lines
		{"198.51.100.77, 203.0.113.66"}, // forwarded, nothing trusted crossed (direct to origin)
		nil,                             // missing
		{"garbage"},                     // malformed, collapses to loopback
		{"192.0.2.10"},                  // proxy only
		{"198.51.100.10, " + edges(MaxForwardedHops)}, // too long
	} {
		s.Record(r1Resolve(r1WithEdges, r1Peer, lines, nil))
	}
	s.Record(r1Resolve(r1WithEdges, "203.0.113.9:1", []string{"198.51.100.77"}, nil)) // untrusted peer

	c := s.Take()
	want := map[string]uint64{"peer": 1, "forwarded": 3, "fallback_missing": 1, "fallback_malformed": 1,
		"fallback_too_long": 1, "fallback_proxy_only": 1}
	for k, v := range want {
		if c.BySource[k] != v {
			t.Errorf("source %s = %d, want %d", k, c.BySource[k], v)
		}
	}
	if len(c.BySource) != len(want) {
		t.Errorf("unexpected keys: %v", c.BySource)
	}
	if c.Total != 8 || c.Fallbacks() != 4 || c.MultiLine != 1 || c.Direct != 1 || c.Loopback != 2 {
		t.Errorf("total=%d fallbacks=%d multi=%d direct=%d loopback=%d", c.Total, c.Fallbacks(), c.MultiLine, c.Direct, c.Loopback)
	}
	if again := s.Take(); again.Total != 0 || again.Fallbacks() != 0 || again.Loopback != 0 {
		t.Errorf("Take did not reset: %+v", again)
	}
}

func TestClientIPCountsDegraded(t *testing.T) {
	counts := func(total, fallbacks, loopback uint64) ClientIPCounts {
		return ClientIPCounts{Total: total, Loopback: loopback,
			BySource: map[string]uint64{"forwarded": total - fallbacks, "fallback_malformed": fallbacks}}
	}
	cases := []struct {
		name string
		c    ClientIPCounts
		want bool
	}{
		{"healthy", counts(1000, 0, 0), false},
		{"few fallbacks: below the absolute minimum", counts(1000, 19, 0), false},
		{"20 fallbacks, 2%", counts(1000, 20, 0), true},
		{"20 fallbacks but only 0.2%", counts(10000, 20, 0), false},
		{"exactly 1%", counts(2000, 20, 0), true},
		{"loopback attribution 25%", counts(100, 0, 25), true},
	}
	for _, c := range cases {
		if got := c.c.Degraded(); got != c.want {
			t.Errorf("%s: Degraded() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestClientIPSummaryLogIsPrivateAndBounded(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	var s ClientIPStats

	s.Take().Log(context.Background(), logger)
	if buf.Len() != 0 {
		t.Fatalf("idle interval logged: %s", buf.String())
	}

	for i := 0; i < 30; i++ {
		s.Record(r1Resolve(r1WithEdges, r1Peer, []string{"198.51.100.77, garbage"}, nil))
		s.Record(r1Resolve(r1WithEdges, r1Peer, []string{"198.51.100.10, 192.0.2.10"}, map[string]string{"Cookie": "rokh_session=secret"}))
	}
	s.Take().Log(context.Background(), logger)
	out := buf.String()
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("want exactly one summary line, got %q", out)
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(out), &line); err != nil {
		t.Fatal(err)
	}
	if line["level"] != "WARN" || line["msg"] != "client ip summary" || line["total"] != float64(60) || line["fallbacks"] != float64(30) {
		t.Errorf("summary = %v", line)
	}
	if len(line) != 3+5+len(ipSourceNames) { // time, level, msg + 5 totals + one per source
		t.Errorf("unexpected number of keys: %d (%v)", len(line), line)
	}
	for _, leak := range []string{"198.51.100", "192.0.2", "garbage", "secret", "X-Forwarded-For", "Cookie"} {
		if strings.Contains(out, leak) {
			t.Errorf("summary leaks %q: %s", leak, out)
		}
	}

	buf.Reset()
	s.Record(r1Resolve(r1WithEdges, r1Peer, []string{"198.51.100.10, 192.0.2.10"}, nil))
	s.Take().Log(context.Background(), logger)
	if !strings.Contains(buf.String(), `"level":"INFO"`) {
		t.Errorf("healthy interval should log at INFO: %s", buf.String())
	}
}

func TestClientIPTelemetryOnlyObservesAPIRequests(t *testing.T) {
	stats := &ClientIPStats{}
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-XFF", strings.Join(r.Header.Values("X-Forwarded-For"), "|"))
		w.WriteHeader(http.StatusTeapot)
	})
	h := NewRouter(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), API: api,
		TrustedProxies: r1WithEdges, ClientIPStats: stats})

	for _, path := range []string{"/api/v1/public/menu", "/api/v1/admin/x", "/healthz"} {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = r1Peer
		r.Header.Add("X-Forwarded-For", "198.51.100.10")
		r.Header.Add("X-Forwarded-For", "192.0.2.10")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if strings.HasPrefix(path, "/api/v1/") && (w.Code != http.StatusTeapot || w.Header().Get("X-Seen-XFF") != "198.51.100.10|192.0.2.10") {
			t.Errorf("%s: request altered: %d %q", path, w.Code, w.Header().Get("X-Seen-XFF"))
		}
	}
	c := stats.Take()
	if c.Total != 2 || c.BySource["forwarded"] != 2 || c.MultiLine != 2 {
		t.Errorf("counts = %+v (want 2 API requests, /healthz not counted)", c)
	}

	plain := NewRouter(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), API: api})
	r := httptest.NewRequest("GET", "/api/v1/public/menu", nil)
	w := httptest.NewRecorder()
	plain.ServeHTTP(w, r)
	if w.Code != http.StatusTeapot {
		t.Errorf("router without telemetry: %d", w.Code)
	}
}

func TestClientIPStatsRunStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		(&ClientIPStats{}).Run(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Hour)
		close(done)
	}()
	<-done // returns because ctx is already cancelled; the hour-long interval never elapses
}
