package httpx

import "net/netip"

// RateKey is the identity per-IP limits and caps are keyed by (docs/SECURITY.md §5).
//
// IPv4: the address itself. IPv6: its /64 network. One subscriber (home, phone, server) normally
// controls a whole /64 and can rotate through its 2^64 addresses at will, so keying by the full
// IPv6 address would let a single client bypass every per-IP limit. An IPv4-mapped IPv6 address
// counts as its IPv4 address. Invalid addresses share one key (fail closed, never unlimited).
func RateKey(a netip.Addr) string {
	a = a.Unmap()
	switch {
	case !a.IsValid():
		return "invalid"
	case a.Is4():
		return a.String()
	default:
		p, err := a.WithZone("").Prefix(64)
		if err != nil {
			return "invalid"
		}
		return p.String()
	}
}
