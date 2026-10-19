package httpx

import (
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("10.0.0.0/8")}
	cases := []struct {
		name, remote, xff, want string
	}{
		{"direct client, header ignored", "203.0.113.7:5000", "1.2.3.4", "203.0.113.7"},
		{"trusted proxy, single hop", "127.0.0.1:1234", "198.51.100.9", "198.51.100.9"},
		{"spoofed left-most entry ignored", "127.0.0.1:1234", "6.6.6.6, 198.51.100.9", "198.51.100.9"},
		{"skips trusted inner hops", "127.0.0.1:1234", "198.51.100.9, 10.1.2.3", "198.51.100.9"},
		{"trusted proxy without header", "127.0.0.1:1234", "", "127.0.0.1"},
		{"garbage header", "127.0.0.1:1234", "not-an-ip", "127.0.0.1"},
		{"ipv4-mapped peer", "[::ffff:203.0.113.7]:80", "", "203.0.113.7"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := ClientIP(r, trusted).String(); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestDecodeJSON(t *testing.T) {
	type in struct {
		Name string `json:"name"`
	}
	cases := []struct {
		body string
		ct   string
		code int
	}{
		{`{"name":"a"}`, "application/json", 0},
		{`{"name":"a","price":1}`, "application/json", 422},
		{`{"name":`, "application/json", 400},
		{`{"name":"a"}{}`, "application/json", 400},
		{`{"name":"a"}`, "text/plain", 415},
		{`{"name":"` + strings.Repeat("x", MaxJSONBody) + `"}`, "application/json", 413},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/", strings.NewReader(c.body))
		r.Header.Set("Content-Type", c.ct)
		w := httptest.NewRecorder()
		var v in
		ok := DecodeJSON(w, r, &v)
		if c.code == 0 {
			if !ok || v.Name != "a" {
				t.Errorf("body %.30q: ok=%v v=%+v", c.body, ok, v)
			}
			continue
		}
		if ok || w.Code != c.code {
			t.Errorf("body %.30q: ok=%v code=%d, want %d", c.body, ok, w.Code, c.code)
		}
	}
}
