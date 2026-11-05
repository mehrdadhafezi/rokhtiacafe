package main

import (
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

// The probe must name every proxy behaviour the R1-B matrix can meet and never echo a real address.
func TestProbeVerdicts(t *testing.T) {
	p := &probe{trusted: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, key: []byte("k")}
	sent := "198.51.100.77,198.51.100.78"
	for _, c := range []struct {
		name    string
		lines   []string
		verdict string
		resolve string
	}{
		{"append", []string{"198.51.100.77, 198.51.100.78, 203.0.113.66"}, "APPENDED_AFTER_CLIENT_ENTRIES", "doc:203.0.113.66"},
		{"append-line", []string{"198.51.100.77, 198.51.100.78", "203.0.113.66"}, "APPENDED_AFTER_CLIENT_ENTRIES", "doc:203.0.113.66"},
		{"preserve", []string{"198.51.100.77, 198.51.100.78"}, "NOT_APPENDED (UNSAFE: a client entry is last)", "marker:198.51.100.78"},
		{"prepend", []string{"203.0.113.66, 198.51.100.77, 198.51.100.78"}, "PREPENDED (UNSAFE: a client entry is last)", "marker:198.51.100.78"},
		{"replace", []string{"203.0.113.66"}, "REPLACED", "doc:203.0.113.66"},
		{"strip", nil, "STRIPPED_OR_ABSENT", "loopback"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "127.0.0.1:40000"
		r.Header.Set("X-R1-Case", c.name)
		r.Header.Set("X-R1-Sent", sent)
		for _, l := range c.lines {
			r.Header.Add("X-Forwarded-For", l)
		}
		rep := p.report(r)
		if rep.Verdict != c.verdict || rep.RokhResolves != c.resolve || !rep.PeerLoopback || rep.Lines != len(c.lines) {
			t.Errorf("%s: %+v", c.name, rep)
		}
	}
	// A real address is never echoed: it becomes a keyed hash; other client-IP headers are flagged.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("X-R1-Sent", "198.51.100.77")
	r.Header.Set("X-Forwarded-For", "198.51.100.77, 8.8.8.8, [2001:4860::8888]:443, nonsense")
	r.Header.Set("X-Real-IP", "8.8.8.8")
	rep := p.report(r)
	flat := strings.Join(rep.Entries[0], " ")
	if strings.Contains(flat, "8.8.8.8") || strings.Contains(flat, "2001:4860") || !strings.Contains(flat, "peer:") ||
		!strings.Contains(flat, "malformed") || strings.Contains(rep.RokhResolves, "8.8") || len(rep.Leaked) != 1 {
		t.Errorf("leak or misclassification: %+v", rep)
	}
}
