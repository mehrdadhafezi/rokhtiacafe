package httpx

import (
	"net/netip"
	"testing"
)

// R1: per-IP limits must not be bypassable by rotating addresses inside one IPv6 /64.
func TestRateKey(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"198.51.100.7", "198.51.100.7"}, // IPv4 unchanged (same keys as before)
		{"::ffff:198.51.100.7", "198.51.100.7"},
		{"2001:db8:1:2::1", "2001:db8:1:2::/64"},
		{"2001:db8:1:2:ffff:ffff:ffff:ffff", "2001:db8:1:2::/64"},
		{"2001:db8:1:3::1", "2001:db8:1:3::/64"},
		{"::1", "::/64"},
		{"fe80::1%eth0", "fe80::/64"},
	} {
		if got := RateKey(netip.MustParseAddr(c.in)); got != c.want {
			t.Errorf("RateKey(%s) = %s, want %s", c.in, got, c.want)
		}
	}
	if RateKey(netip.Addr{}) != "invalid" {
		t.Error("invalid address must map to one shared key")
	}
	// 1000 addresses of one /64 are one key; neighbouring /64s are distinct.
	seen := map[string]bool{}
	base := netip.MustParseAddr("2001:db8:1:2::").As16()
	for i := 0; i < 1000; i++ {
		b := base
		b[15], b[14], b[9] = byte(i), byte(i>>8), byte(i*7)
		seen[RateKey(netip.AddrFrom16(b))] = true
	}
	if len(seen) != 1 {
		t.Errorf("rotation inside one /64 produced %d keys", len(seen))
	}
}
