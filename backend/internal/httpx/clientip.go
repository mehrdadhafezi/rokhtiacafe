package httpx

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// MaxForwardedHops is how many X-Forwarded-For entries, counted from the right, are examined.
// A real chain here is visitor → CDN edge → LiteSpeed (two or three entries). If no untrusted
// address is found within this budget the chain is not trusted (SourceFallbackTooLong). Entries
// further left are never parsed, so their number and content cannot affect the result.
const MaxForwardedHops = 16

// IPSource says how ClientIP chose its answer (docs/SECURITY.md §5.1).
type IPSource int

const (
	// SourcePeer: the direct peer is not a trusted proxy; forwarding headers were ignored.
	SourcePeer IPSource = iota
	// SourceForwarded: the right-most X-Forwarded-For entry that is not a trusted proxy.
	SourceForwarded
	// SourceFallbackMissing: a trusted peer sent no X-Forwarded-For entries.
	SourceFallbackMissing
	// SourceFallbackMalformed: an unparsable or zoned entry was reached before an untrusted one.
	SourceFallbackMalformed
	// SourceFallbackTooLong: MaxForwardedHops entries were examined without an untrusted one.
	SourceFallbackTooLong
	// SourceFallbackProxyOnly: every entry is a trusted proxy (the visitor is not in the header).
	SourceFallbackProxyOnly
)

var ipSourceNames = [...]string{"peer", "forwarded", "fallback_missing", "fallback_malformed",
	"fallback_too_long", "fallback_proxy_only"}

func (s IPSource) String() string { return ipSourceNames[s] }

// Fallback reports whether the visitor could not be established from the chain.
func (s IPSource) Fallback() bool { return s >= SourceFallbackMissing }

// Resolution is the outcome of client-IP resolution, with what diagnostics need. It carries no
// header content.
type Resolution struct {
	Addr   netip.Addr
	Source IPSource
	// Lines is the number of non-empty X-Forwarded-For field lines (more than one is unusual).
	Lines int
	// Crossed is the number of trusted entries passed on the right before the decision.
	Crossed int
}

// ClientIP returns the caller's address. X-Forwarded-For is honoured only when the direct peer
// is a trusted proxy, and then the right-most address that is not itself trusted is used
// (docs/SECURITY.md §5).
func ClientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	return ResolveClientIP(r, trusted).Addr
}

// ResolveClientIP is ClientIP plus the reason for the answer.
//
// All X-Forwarded-For field lines are combined in their received order, as RFC 9110 §5.3
// specifies, so a chain split over several lines resolves exactly like the same chain on one line,
// and a proxy that records the real peer as a later line is not bypassed by a forged first line.
// The chain is read from the right (the end written last, by the proxy rokh trusts).
//
// When the visitor cannot be established, the answer is the anchor: the right-most entry when it
// is a trusted proxy (the address the trusted peer itself reports as its own peer, e.g. the CDN
// edge LiteSpeed accepted the connection from), otherwise the direct peer. Entries left of the
// right-most one are header claims; they are believed only to find the visitor, never to choose a
// fallback identity, so a degraded chain cannot let anyone pick their bucket by writing addresses
// that happen to lie in a trusted range (docs/SECURITY.md §5.1).
func ResolveClientIP(r *http.Request, trusted []netip.Prefix) Resolution {
	peer := parseAddr(r.RemoteAddr)
	if !peer.IsValid() || !inPrefixes(peer, trusted) {
		return Resolution{Addr: peer, Source: SourcePeer}
	}
	var lines []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		if strings.TrimSpace(v) != "" {
			lines = append(lines, v)
		}
	}
	res := Resolution{Addr: peer, Lines: len(lines)}
	if len(lines) == 0 {
		res.Source = SourceFallbackMissing
		return res
	}
	rest := strings.Join(lines, ",")
	anchor := peer // fallback identity: the right-most entry if trusted, else the peer
	for examined := 0; ; examined++ {
		if examined == MaxForwardedHops {
			res.Addr, res.Source = anchor, SourceFallbackTooLong
			return res
		}
		entry := rest
		more := false
		if i := strings.LastIndexByte(rest, ','); i >= 0 {
			entry, rest, more = rest[i+1:], rest[:i], true
		}
		a := parseHop(entry)
		switch {
		case !a.IsValid():
			res.Addr, res.Source = anchor, SourceFallbackMalformed
			return res
		case !inPrefixes(a, trusted):
			res.Addr, res.Source = a, SourceForwarded
			return res
		}
		if res.Crossed == 0 {
			anchor = a
		}
		res.Crossed++
		if !more {
			res.Addr, res.Source = anchor, SourceFallbackProxyOnly
			return res
		}
	}
}

// parseHop parses one X-Forwarded-For entry: a bare address, optionally bracketed (IPv6) or with
// a port. Zoned addresses are rejected: they never identify a remote visitor.
func parseHop(s string) netip.Addr {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		s = s[1 : len(s)-1]
	}
	a := parseAddr(s)
	if a.Zone() != "" {
		return netip.Addr{}
	}
	return a
}

func parseAddr(s string) netip.Addr {
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}

func inPrefixes(a netip.Addr, ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
