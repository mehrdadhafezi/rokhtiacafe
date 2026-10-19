package push

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

var testAllow = ParseAllowlist("fcm.googleapis.com, updates.push.services.mozilla.com,web.push.apple.com,*.notify.windows.com")

func TestValidateEndpoint(t *testing.T) {
	ok := []string{
		"https://fcm.googleapis.com/fcm/send/abc:def",
		"https://updates.push.services.mozilla.com/wpush/v2/gAAAA",
		"https://web.push.apple.com/QGq3x",
		"https://wns2-db5p.notify.windows.com/w/?token=x",
		"https://fcm.googleapis.com:443/fcm/send/x",
	}
	for _, e := range ok {
		if err := ValidateEndpoint(e, testAllow); err != nil {
			t.Errorf("%s: %v", e, err)
		}
	}
	// R-F11-1 plus the rest of SECURITY §12.
	bad := []string{
		"https://127.0.0.1/push",
		"https://169.254.169.254/latest/meta-data",
		"https://[::1]/push",
		"http://fcm.googleapis.com/fcm/send/x",
		"https://evil.example.com/fcm/send/x",
		"https://fcm.googleapis.com.evil.com/x",
		"https://notify.windows.com/x", // suffix entry needs a subdomain
		"https://user:pw@fcm.googleapis.com/x",
		"https://fcm.googleapis.com:8443/x",
		"https:///nohost",
		"ftp://fcm.googleapis.com/x",
		"https://fcm.googleapis.com/" + strings.Repeat("a", MaxEndpointLen),
		"",
	}
	for _, e := range bad {
		if err := ValidateEndpoint(e, testAllow); !errors.Is(err, ErrEndpoint) {
			t.Errorf("%.60s accepted (err=%v)", e, err)
		}
	}
}

func TestIsPublic(t *testing.T) {
	for _, s := range []string{"8.8.8.8", "142.250.185.10", "2a00:1450:4001:80b::200a"} {
		if !IsPublic(netip.MustParseAddr(s)) {
			t.Errorf("%s should be public", s)
		}
	}
	for _, s := range []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0",
		"224.0.0.1", "255.255.255.255", "::1", "fe80::1", "fc00::1", "::", "::ffff:10.0.0.1", "64:ff9b::a00:1",
	} {
		if IsPublic(netip.MustParseAddr(s)) {
			t.Errorf("%s must not be public", s)
		}
	}
}

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return f[host], nil
}

func TestSafeDialRefusesPrivateResolution(t *testing.T) {
	// An allowlisted name that (maliciously or by misconfiguration) resolves to an internal IP.
	r := fakeResolver{
		"fcm.googleapis.com": {netip.MustParseAddr("10.0.0.5")},
		"mixed.example":      {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")},
	}
	dial := SafeDialContext(r, &net.Dialer{Timeout: time.Second})
	for _, host := range []string{"fcm.googleapis.com:443", "mixed.example:443"} {
		if _, err := dial(context.Background(), "tcp", host); !errors.Is(err, ErrUnsafeAddress) {
			t.Errorf("%s: expected ErrUnsafeAddress, got %v", host, err)
		}
	}
	// Real resolver: localhost is refused before any connection is attempted.
	realDial := SafeDialContext(net.DefaultResolver, &net.Dialer{Timeout: time.Second})
	if _, err := realDial(context.Background(), "tcp", "localhost:443"); !errors.Is(err, ErrUnsafeAddress) {
		t.Errorf("localhost: %v", err)
	}
}

func TestHTTPClientDoesNotFollowRedirectsOrUseProxy(t *testing.T) {
	c := NewHTTPClient(net.DefaultResolver)
	if c.Timeout != SendTimeout || c.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("client must refuse redirects and bound the request time")
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil || tr.DialContext == nil {
		t.Fatal("transport must use the safe dialer and ignore proxy environment variables")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	if d := parseRetryAfter("120", now); d != 2*time.Minute {
		t.Errorf("seconds: %v", d)
	}
	if d := parseRetryAfter("Tue, 20 Oct 2026 12:10:00 GMT", now); d != 10*time.Minute {
		t.Errorf("date: %v", d)
	}
	if parseRetryAfter("soon", now) != 0 || parseRetryAfter("", now) != 0 || parseRetryAfter("-5", now) != 0 {
		t.Error("invalid values must give no hint")
	}
}
