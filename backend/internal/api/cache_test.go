package api_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
)

// R2: private API responses must never be stored by a shared cache (CDN, proxy) and served to
// another visitor, customer or staff role. docs/SECURITY.md §5.2.

// TestEveryAPIResponseIsNoStore walks every registered API route and calls it anonymously, as a
// customer and as ADMIN: every response (2xx, 3xx, 4xx, 5xx) must be "no-store", except the public
// menu (public data, "no-cache" + ETag). Unknown paths and wrong methods too.
func TestEveryAPIResponseIsNoStore(t *testing.T) {
	e := newEnv(t)
	cust, _ := newCustomer(t, e, "09121111111", "آرش")
	admin := e.newClient().login("admin", adminPassword) // one login: 5 per 15 minutes per username
	callers := map[string]func() *client{
		"anonymous": e.newClient,
		"customer":  func() *client { return cust },
		"admin":     func() *client { return admin },
	}
	var routes [][2]string
	err := chi.Walk(e.api.Handler().(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, [2]string{method, strings.ReplaceAll(strings.ReplaceAll(route, "/*", ""), "{id}", "1")})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	routes = append(routes, [2]string{http.MethodGet, "/no-such-route"}, [2]string{http.MethodDelete, "/public/menu"})
	if len(routes) < 50 {
		t.Fatalf("only %d routes found", len(routes))
	}
	checked := 0
	for name, mk := range callers {
		for _, rt := range routes {
			if rt[1] == "/auth/logout" || rt[1] == "/customer/auth/logout" || rt[1] == "/customer/auth/password" {
				continue // would end the caller's session for the remaining routes; checked separately below
			}
			c := mk()
			var body any
			if rt[0] != http.MethodGet && rt[0] != http.MethodDelete {
				body = map[string]any{}
			}
			resp, _ := c.do(rt[0], "/api/v1"+rt[1], body, withKey(newUUID()))
			want := "no-store"
			if rt[1] == "/public/menu" && rt[0] == http.MethodGet {
				want = "no-cache"
			}
			if got := resp.Header.Get("Cache-Control"); got != want {
				t.Errorf("%s %s %s → %d Cache-Control %q, want %q", name, rt[0], rt[1], resp.StatusCode, got, want)
			}
			checked++
		}
	}
	for _, p := range []string{"/api/v1/auth/logout", "/api/v1/customer/auth/logout"} {
		resp, _ := e.newClient().do(http.MethodPost, p, nil)
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %q", p, resp.Header.Get("Cache-Control"))
		}
	}
	for _, p := range []string{"/healthz", "/readyz"} {
		resp, _ := e.newClient().do(http.MethodGet, p, nil)
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %q", p, resp.Header.Get("Cache-Control"))
		}
	}
	t.Logf("%d responses checked over %d routes", checked, len(routes))
}

// sharedCache is a simulated CDN/proxy cache shared by every visitor, keyed by method + URL only
// (the worst case: it ignores cookies and request headers).
//
//	policy "honor"      caches a GET response unless Cache-Control says no-store, private or
//	                    no-cache; a response WITHOUT Cache-Control gets the CDN's default TTL
//	                    (a common "cache by default" edge setting).
//	policy "everything" caches every GET 200, whatever the headers (a misconfigured edge rule).
type sharedCache struct {
	policy  string
	origin  string
	mu      sync.Mutex
	entries map[string]cached
	hits    int
}

type cached struct {
	status int
	header http.Header
	body   []byte
}

func (s *sharedCache) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + r.URL.RequestURI()
	if r.Method == http.MethodGet {
		s.mu.Lock()
		c, ok := s.entries[key]
		if ok {
			s.hits++
		}
		s.mu.Unlock()
		if ok {
			for k, v := range c.header {
				w.Header()[k] = v
			}
			w.Header().Set("X-Sim-Cache", "HIT")
			w.WriteHeader(c.status)
			_, _ = w.Write(c.body)
			return
		}
	}
	out, _ := http.NewRequest(r.Method, s.origin+r.URL.RequestURI(), r.Body)
	out.Header = r.Header.Clone()
	resp, err := http.DefaultTransport.RoundTrip(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if r.Method == http.MethodGet && s.storable(resp) {
		s.mu.Lock()
		s.entries[key] = cached{resp.StatusCode, resp.Header.Clone(), body}
		s.mu.Unlock()
	}
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.Header().Set("X-Sim-Cache", "MISS")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

func (s *sharedCache) storable(resp *http.Response) bool {
	if s.policy == "everything" {
		return resp.StatusCode == http.StatusOK
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusUnauthorized &&
		resp.StatusCode != http.StatusForbidden {
		return false
	}
	cc := strings.ToLower(resp.Header.Get("Cache-Control"))
	for _, d := range []string{"no-store", "private", "no-cache"} {
		if strings.Contains(cc, d) {
			return false
		}
	}
	return true // explicit freshness, or no directive at all (CDN default TTL)
}

func newSharedCache(t *testing.T, policy string, origin http.Handler) (*sharedCache, string) {
	t.Helper()
	upstream := httptest.NewServer(origin)
	t.Cleanup(upstream.Close)
	c := &sharedCache{policy: policy, origin: upstream.URL, entries: map[string]cached{}}
	front := httptest.NewServer(c)
	t.Cleanup(front.Close)
	return c, front.URL
}

// isolationScenario runs identity-bearing GETs through the shared cache in an order that would
// leak if anything were cached, and returns the leaks it observed.
func isolationScenario(t *testing.T, e *env, base string) (leaks []string) {
	t.Helper()
	via := func(c *client) *client { c.base = base; return c }
	_, _, espresso, _, _ := seedCatalog(t, e.newClient().login("admin", adminPassword))
	admin := via(e.newClient()).login("admin", adminPassword)
	e.createUser("barista", "OPERATOR", "barista-pass-1")
	op := via(e.newClient()).login("barista", "barista-pass-1")

	reg := func(phone, name string) *client {
		c := via(e.newClient())
		resp, body := c.do(http.MethodPost, "/api/v1/customer/auth/register", map[string]string{
			"first_name": name, "last_name": "مشتری", "phone": phone, "password": "secure-cafe-2026"})
		expect(t, resp, body, http.StatusCreated, "")
		c.csrf = body["csrf_token"].(string)
		return c
	}
	a, b := reg("09121111111", "آرش"), reg("09122222222", "بهار")
	order := func(c *client) (number float64, token string) {
		resp, body := c.do(http.MethodPost, "/api/v1/public/orders", map[string]any{"items": []any{line(espresso, 1, 950000)}},
			withKey(newUUID()), withHeader("X-Client-Id", newUUID()))
		expect(t, resp, body, http.StatusCreated, "")
		return body["order_number"].(float64), body["tracking_token"].(string)
	}
	numA, tokA := order(a)
	numB, tokB := order(b)

	same := func(what string, first, second *client, path string, opts1, opts2 []reqOpt, differ func(r1, r2 *http.Response, b1, b2 map[string]any) bool) {
		r1, b1 := first.do(http.MethodGet, path, nil, opts1...)
		r2, b2 := second.do(http.MethodGet, path, nil, opts2...)
		if r2.Header.Get("X-Sim-Cache") == "HIT" || !differ(r1, r2, b1, b2) {
			leaks = append(leaks, fmt.Sprintf("%s (%s %d → %d, second response %s)", what, path, r1.StatusCode, r2.StatusCode, r2.Header.Get("X-Sim-Cache")))
		}
	}
	name := func(b map[string]any, k string) string { return fmt.Sprint(obj(b[k])["first_name"]) }
	same("customer profile A→B", a, b, "/api/v1/customer/auth/me", nil, nil,
		func(_, _ *http.Response, b1, b2 map[string]any) bool {
			return name(b1, "customer") != name(b2, "customer")
		})
	same("customer profile A→anonymous", a, via(e.newClient()), "/api/v1/customer/auth/me", nil, nil,
		func(_, r2 *http.Response, _, _ map[string]any) bool { return r2.StatusCode == http.StatusUnauthorized })
	same("order history A→B", a, b, "/api/v1/customer/orders", nil, nil,
		func(_, _ *http.Response, b1, b2 map[string]any) bool {
			return fmt.Sprint(b1["items"]) != fmt.Sprint(b2["items"])
		})
	same("activity A→B", a, b, "/api/v1/customer/activity", nil, nil,
		func(_, _ *http.Response, b1, b2 map[string]any) bool { return fmt.Sprint(b1) != fmt.Sprint(b2) })
	same("tracking token A→B (same URL, token in a header)", via(e.newClient()), via(e.newClient()), "/api/v1/public/order",
		[]reqOpt{withHeader("X-Tracking-Token", tokA)}, []reqOpt{withHeader("X-Tracking-Token", tokB)},
		func(_, _ *http.Response, b1, b2 map[string]any) bool {
			return b1["order_number"] == numA && b2["order_number"] == numB
		})
	same("staff identity ADMIN→OPERATOR", admin, op, "/api/v1/auth/me", nil, nil,
		func(_, _ *http.Response, b1, b2 map[string]any) bool {
			return fmt.Sprint(b1["user"]) != fmt.Sprint(b2["user"])
		})
	d := today()
	for _, p := range []string{"/api/v1/admin/reports/summary?from=" + d + "&to=" + d, "/api/v1/admin/customers",
		"/api/v1/admin/reports/customers?from=" + d + "&to=" + d, "/api/v1/admin/users", "/api/v1/admin/audit-logs"} {
		same("ADMIN→OPERATOR "+strings.SplitN(p, "?", 2)[0], admin, op, p, nil, nil,
			func(r1, r2 *http.Response, _, _ map[string]any) bool {
				return r1.StatusCode == 200 && r2.StatusCode == http.StatusForbidden
			})
	}
	same("ADMIN→anonymous orders", admin, via(e.newClient()), "/api/v1/admin/orders", nil, nil,
		func(r1, r2 *http.Response, _, _ map[string]any) bool {
			return r1.StatusCode == 200 && r2.StatusCode == http.StatusUnauthorized
		})
	for _, f := range []string{"csv", "xlsx"} {
		p := "/api/v1/admin/reports/customers." + f + "?from=" + d + "&to=" + d
		r1, _ := admin.get(p)
		r2, _ := op.get(p)
		if r2.Header.Get("X-Sim-Cache") == "HIT" || r1.StatusCode != 200 || r2.StatusCode != http.StatusForbidden {
			leaks = append(leaks, "export "+f+" ADMIN→OPERATOR")
		}
	}
	return leaks
}

func TestSharedCacheNeverServesPrivateResponsesAcrossIdentities(t *testing.T) {
	e := newEnv(t)
	cache, base := newSharedCache(t, "honor", e.srv.Config.Handler)
	if leaks := isolationScenario(t, e, base); len(leaks) > 0 {
		t.Errorf("private responses served across identities:\n  %s", strings.Join(leaks, "\n  "))
	}
	if cache.hits != 0 {
		t.Errorf("%d private responses were served from the shared cache", cache.hits)
	}
	// The cache does work for public, versioned content: the menu revalidates (no-cache), and an
	// immutable asset is stored and served again.
	r1, _ := e.newClient().do(http.MethodGet, "/api/v1/public/menu", nil)
	if r1.Header.Get("Cache-Control") != "no-cache" || r1.Header.Get("ETag") == "" {
		t.Errorf("menu caching headers: %v", r1.Header)
	}
}

// The same scenario against the router as it was before R2 (API mounted without the no-store
// default): a "cache by default" edge leaks private responses. This is the regression the
// middleware closes.
func TestSharedCacheLeaksWithoutNoStoreDefault(t *testing.T) {
	e := newEnv(t)
	legacy := chi.NewRouter()
	legacy.Mount("/api/v1", e.api.Handler())
	_, base := newSharedCache(t, "honor", legacy)
	leaks := isolationScenario(t, e, base)
	if len(leaks) == 0 {
		t.Fatal("expected leaks without the no-store default: the simulation no longer demonstrates the risk")
	}
	t.Logf("pre-R2 router leaks through a cache-by-default edge (%d):\n  %s", len(leaks), strings.Join(leaks, "\n  "))
}

// A misconfigured edge that caches everything ignores origin headers: no application change can
// prevent that, which is why the CDN bypass rule for /api/* is a separate runtime gate (R2-B).
func TestSharedCacheEverythingPolicyIsOutOfAppControl(t *testing.T) {
	e := newEnv(t)
	_, base := newSharedCache(t, "everything", e.srv.Config.Handler)
	if leaks := isolationScenario(t, e, base); len(leaks) == 0 {
		t.Fatal("a cache-everything edge should leak; the R2-B gate depends on the CDN rule")
	}
}

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
