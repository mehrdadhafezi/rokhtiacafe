package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"time"
)

// Client-IP telemetry (docs/SECURITY.md §5.1, deploy/README.md §11). Aggregate counters only: no
// addresses, no header values, a fixed set of keys. One summary log line per interval, never one
// per request.

// ClientIPSummaryInterval is how often the summary is logged when there was API traffic.
const ClientIPSummaryInterval = 5 * time.Minute

// A summary is logged at WARN when, within one interval, at least clientIPWarnMin requests (and at
// least clientIPWarnPercent of all) fell back or were attributed to loopback. Telemetry only:
// nothing here changes how requests are handled.
const (
	clientIPWarnMin     = 20
	clientIPWarnPercent = 1
)

// ClientIPStats counts how API requests were attributed. The zero value is ready to use; it is
// safe for concurrent use. One mutex guards the whole counter bank, so Record and Take never
// interleave: every recorded request lands in exactly one snapshot, and in each snapshot Total
// equals the sum of the per-source counts (R1-B3). The critical sections are a few integer
// increments or one struct copy, negligible at café request rates.
type ClientIPStats struct {
	mu   sync.Mutex
	bank clientIPBank
}

type clientIPBank struct {
	bySource  [len(ipSourceNames)]uint64
	multiLine uint64
	direct    uint64 // forwarded with no trusted entry crossed
	loopback  uint64 // attributed to a loopback address
	total     uint64
}

// ClientIPCounts is one interval's counts.
type ClientIPCounts struct {
	Total     uint64
	BySource  map[string]uint64
	MultiLine uint64 // more than one X-Forwarded-For field line
	Direct    uint64 // forwarded, but the entry the trusted proxy reported is itself the client
	Loopback  uint64 // attributed to a loopback address (shared bucket)
}

// Record counts one resolution, exactly once, under exactly one source.
func (s *ClientIPStats) Record(r Resolution) {
	multi := r.Lines > 1
	direct := r.Source == SourceForwarded && r.Crossed == 0
	loop := r.Addr.IsLoopback()
	s.mu.Lock()
	defer s.mu.Unlock()
	b := &s.bank
	b.total++
	b.bySource[r.Source]++
	if multi {
		b.multiLine++
	}
	if direct {
		b.direct++
	}
	if loop {
		b.loopback++
	}
}

// Take returns the counts since the previous Take and resets them, as one atomic snapshot.
func (s *ClientIPStats) Take() ClientIPCounts {
	s.mu.Lock()
	b := s.bank
	s.bank = clientIPBank{}
	s.mu.Unlock()
	c := ClientIPCounts{
		Total:     b.total,
		BySource:  make(map[string]uint64, len(ipSourceNames)),
		MultiLine: b.multiLine,
		Direct:    b.direct,
		Loopback:  b.loopback,
	}
	for i, n := range b.bySource {
		c.BySource[ipSourceNames[i]] = n
	}
	return c
}

// Fallbacks is the number of requests whose visitor could not be established.
func (c ClientIPCounts) Fallbacks() uint64 {
	var n uint64
	for i := SourceFallbackMissing; int(i) < len(ipSourceNames); i++ {
		n += c.BySource[i.String()]
	}
	return n
}

// Degraded reports whether fallbacks or loopback attribution reached the warning threshold.
func (c ClientIPCounts) Degraded() bool {
	over := func(n uint64) bool { return n >= clientIPWarnMin && n*100 >= c.Total*clientIPWarnPercent }
	return over(c.Fallbacks()) || over(c.Loopback)
}

// Log writes one summary line for the counts, at WARN when degraded; nothing when idle.
func (c ClientIPCounts) Log(ctx context.Context, logger *slog.Logger) {
	if c.Total == 0 {
		return
	}
	level := slog.LevelInfo
	if c.Degraded() {
		level = slog.LevelWarn
	}
	attrs := []slog.Attr{
		slog.Uint64("total", c.Total),
		slog.Uint64("fallbacks", c.Fallbacks()),
		slog.Uint64("loopback", c.Loopback),
		slog.Uint64("forwarded_direct", c.Direct),
		slog.Uint64("multi_line", c.MultiLine),
	}
	for _, name := range ipSourceNames {
		attrs = append(attrs, slog.Uint64("source_"+name, c.BySource[name]))
	}
	logger.LogAttrs(ctx, level, "client ip summary", attrs...)
}

// Run logs a summary every interval until ctx ends, then flushes what was counted since the last
// summary. Requests that finish after that (during graceful shutdown) are flushed by the caller
// with Flush. A logging failure never affects requests.
func (s *ClientIPStats) Run(ctx context.Context, logger *slog.Logger, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Flush(logger)
			return
		case <-t.C:
			s.Take().Log(ctx, logger)
		}
	}
}

// Flush logs and resets whatever has been counted since the last summary (nothing when idle).
func (s *ClientIPStats) Flush(logger *slog.Logger) {
	s.Take().Log(context.Background(), logger)
}

// ClientIPTelemetry records how each request's client IP is resolved. It only observes: the
// request continues unchanged, and handlers resolve the address themselves.
func ClientIPTelemetry(trusted []netip.Prefix, stats *ClientIPStats) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stats.Record(ResolveClientIP(r, trusted))
			next.ServeHTTP(w, r)
		})
	}
}
