// Command xffprobe is the diagnostic upstream for the R1-B runtime gate
// (docs/R1-R2-RUNTIME-EVIDENCE.md). Put it where rokh would listen, behind the real LiteSpeed (and
// CDN), send the request matrix of deploy/r1/probe-matrix.sh, and it reports how the proxy rewrote
// X-Forwarded-For — without recording any real address.
//
//	xffprobe -listen 127.0.0.1:18081 -trusted 127.0.0.1/32[,<edge ranges>]
//
// Every request carries X-R1-Case (the case name) and forged marker addresses from the
// documentation ranges 198.51.100.0/24 / 2001:db8:f::/48. Each response and log line is one JSON
// object: the case, the number of header lines, each entry classified as "marker:<addr>",
// "doc:<addr>" (other documentation addresses), "loopback", "trusted" (inside -trusted), "peer:<h>"
// (any other address, as an 8-hex keyed hash so equal addresses can be correlated within a run),
// "malformed"; the verdict about the last proxy's behaviour; what rokh would resolve with
// -trusted; and whether X-Real-IP, MC_REAL_IP or Forwarded reached the upstream. It is a test tool:
// never part of a release, never left running.
package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"os"
	"strings"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
)

var markerRanges = []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("2001:db8:f::/48")}
var docRanges = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("2001:db8::/32")}

// Report is what one request revealed.
type Report struct {
	Case         string     `json:"case"`
	Lines        int        `json:"xff_lines"`
	Entries      [][]string `json:"xff_entries"` // per line, classified
	Verdict      string     `json:"verdict"`
	RokhResolves string     `json:"rokh_resolves"` // classification of what rokh would pick
	RokhSource   string     `json:"rokh_source"`
	PeerLoopback bool       `json:"peer_loopback"`
	Leaked       []string   `json:"other_ip_headers_present,omitempty"`
}

type probe struct {
	trusted []netip.Prefix
	key     []byte
}

func (p *probe) class(entry string) string {
	s := strings.TrimSpace(entry)
	a, err := netip.ParseAddr(strings.Trim(s, "[]"))
	if err != nil {
		if ap, err2 := netip.ParseAddrPort(s); err2 == nil {
			a, err = ap.Addr(), nil
		}
	}
	if err != nil {
		return "malformed"
	}
	a = a.Unmap()
	switch {
	case in(a, markerRanges):
		return "marker:" + a.String()
	case a.IsLoopback():
		return "loopback"
	case in(a, p.trusted):
		return "trusted"
	case in(a, docRanges):
		return "doc:" + a.String()
	}
	m := hmac.New(sha256.New, p.key)
	m.Write([]byte(a.String()))
	return "peer:" + hex.EncodeToString(m.Sum(nil))[:8]
}

// verdict describes what the proxies in front of the probe did with the client's entries. The
// request matrix always sends forged markers when it sends a header (listed in X-R1-Sent).
func verdict(flat []string, sentMarkers int) string {
	firstMarker, lastMarker := -1, -1
	for i, c := range flat {
		if strings.HasPrefix(c, "marker:") {
			if firstMarker < 0 {
				firstMarker = i
			}
			lastMarker = i
		}
	}
	last := len(flat) - 1
	switch {
	case len(flat) == 0:
		return "STRIPPED_OR_ABSENT"
	case sentMarkers == 0:
		return "ADDED"
	case firstMarker < 0:
		return "REPLACED"
	case lastMarker == last && firstMarker > 0:
		return "PREPENDED (UNSAFE: a client entry is last)"
	case lastMarker == last:
		return "NOT_APPENDED (UNSAFE: a client entry is last)"
	default:
		return "APPENDED_AFTER_CLIENT_ENTRIES"
	}
}

func in(a netip.Addr, ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func (p *probe) report(r *http.Request) Report {
	rep := Report{Case: r.Header.Get("X-R1-Case")}
	var flat []string
	for _, line := range r.Header.Values("X-Forwarded-For") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rep.Lines++
		var cls []string
		for _, e := range strings.Split(line, ",") {
			c := p.class(e)
			cls = append(cls, c)
			flat = append(flat, c)
		}
		rep.Entries = append(rep.Entries, cls)
	}
	sent := 0
	for _, f := range strings.Split(r.Header.Get("X-R1-Sent"), ",") {
		if strings.TrimSpace(f) != "" {
			sent++
		}
	}
	rep.Verdict = verdict(flat, sent)
	res := httpx.ResolveClientIP(r, p.trusted)
	rep.RokhResolves, rep.RokhSource = p.class(res.Addr.String()), res.Source.String()
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		rep.PeerLoopback = ap.Addr().Unmap().IsLoopback()
	}
	for _, h := range []string{"X-Real-IP", "MC_REAL_IP", "Mc-Real-Ip", "Forwarded", "True-Client-IP", "CF-Connecting-IP"} {
		if r.Header.Get(h) != "" {
			rep.Leaked = append(rep.Leaked, h)
		}
	}
	return rep
}

func (p *probe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rep := p.report(r)
	b, _ := json.Marshal(rep)
	log.Println(string(b))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(append(b, '\n'))
}

func parsePrefixes(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			a, err := netip.ParseAddr(part)
			if err != nil {
				return nil, err
			}
			part = netip.PrefixFrom(a, a.BitLen()).String()
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

func main() {
	listen := flag.String("listen", "127.0.0.1:18081", "address to listen on (loopback)")
	trusted := flag.String("trusted", "127.0.0.1/32", "TRUSTED_PROXIES candidate to evaluate")
	flag.Parse()
	ps, err := parsePrefixes(*trusted)
	if err != nil {
		fmt.Fprintln(os.Stderr, "xffprobe: -trusted:", err)
		os.Exit(2)
	}
	if ap, err := netip.ParseAddrPort(*listen); err != nil || !ap.Addr().IsLoopback() {
		fmt.Fprintln(os.Stderr, "xffprobe: -listen must be a loopback address:port")
		os.Exit(2)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	log.SetFlags(0)
	log.Printf(`{"msg":"xffprobe listening","listen":%q,"trusted":%q}`, *listen, *trusted)
	if err := http.ListenAndServe(*listen, &probe{trusted: ps, key: key}); err != nil {
		fmt.Fprintln(os.Stderr, "xffprobe:", err)
		os.Exit(1)
	}
}
