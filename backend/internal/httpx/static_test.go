package httpx

import (
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func testUI() fstest.MapFS {
	big := strings.Repeat("console.log('رخ تیا');\n", 200)
	return fstest.MapFS{
		"index.html":                 {Data: []byte("<!doctype html><title>menu</title>")},
		"admin/index.html":           {Data: []byte("<!doctype html><title>admin</title>")},
		"admin/sw.js":                {Data: []byte("importScripts('/admin/push-sw.js')")},
		"admin/push-sw.js":           {Data: []byte("self.addEventListener('push', () => {})")},
		"admin/manifest.webmanifest": {Data: []byte(`{"scope":"/admin/"}`)},
		"admin/icons/icon-192.png":   {Data: []byte("\x89PNG")},
		"assets/customer-abc123.js":  {Data: []byte(big)},
		"assets/vazirmatn-xyz.woff2": {Data: []byte("wOF2")},
		"favicon.svg":                {Data: []byte("<svg/>")},
		".vite/manifest.json":        {Data: []byte("{}")},
	}
}

func get(t *testing.T, h http.Handler, path string, hdr ...string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func body(t *testing.T, r *http.Response) string {
	t.Helper()
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

func TestStaticRoutingAndFallbacks(t *testing.T) {
	s := NewStatic(testUI(), time.Now())
	if !s.Ready() {
		t.Fatal("both entry points present: Ready must be true")
	}
	cases := []struct {
		path, wantBody string
		status         int
	}{
		{"/menu", "<title>menu</title>", 200},
		{"/menu/", "<title>menu</title>", 200},
		{"/menu/order", "<title>menu</title>", 200},
		{"/menu/cart", "<title>menu</title>", 200},
		{"/admin/", "<title>admin</title>", 200},
		{"/admin/orders/12", "<title>admin</title>", 200},
		{"/admin/inventory", "<title>admin</title>", 200},
		{"/admin/sw.js", "importScripts", 200},
		{"/favicon.svg", "<svg/>", 200},
		{"/assets/missing-000.js", "NOT_FOUND", 404}, // a missing asset is never HTML
		{"/admin/missing.png", "NOT_FOUND", 404},
		{"/.vite/manifest.json", "NOT_FOUND", 404}, // no dotfiles
		{"/assets/", "NOT_FOUND", 404},             // no directory listing
		{"/elsewhere", "NOT_FOUND", 404},
		{"/menu/../.vite/manifest.json", "NOT_FOUND", 404},
	}
	for _, c := range cases {
		r := get(t, s, c.path)
		if b := body(t, r); r.StatusCode != c.status || !strings.Contains(b, c.wantBody) {
			t.Errorf("%s = %d %.60q, want %d containing %q", c.path, r.StatusCode, b, c.status, c.wantBody)
		}
	}
	if r := get(t, s, "/"); r.StatusCode != http.StatusFound || r.Header.Get("Location") != "/menu" {
		t.Errorf("/ = %d → %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r := get(t, s, "/admin"); r.StatusCode != http.StatusMovedPermanently || r.Header.Get("Location") != "/admin/" {
		t.Errorf("/admin = %d → %s", r.StatusCode, r.Header.Get("Location"))
	}
	req := httptest.NewRequest(http.MethodPost, "/menu", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /menu = %d", rec.Code)
	}
}

func TestStaticHeaders(t *testing.T) {
	s := NewStatic(testUI(), time.Now())
	check := func(path, ctype, cache string) *http.Response {
		t.Helper()
		r := get(t, s, path)
		if !strings.HasPrefix(r.Header.Get("Content-Type"), ctype) || r.Header.Get("Cache-Control") != cache {
			t.Errorf("%s: type=%q cache=%q, want %q / %q", path, r.Header.Get("Content-Type"), r.Header.Get("Cache-Control"), ctype, cache)
		}
		return r
	}
	check("/menu/order", "text/html", "no-cache")
	check("/admin/manifest.webmanifest", "application/manifest+json", "no-cache")
	check("/assets/customer-abc123.js", "text/javascript", "public, max-age=31536000, immutable")
	check("/assets/vazirmatn-xyz.woff2", "font/woff2", "public, max-age=31536000, immutable")
	check("/admin/push-sw.js", "text/javascript", "no-cache") // imported by the worker: must update with it
	check("/admin/icons/icon-192.png", "image/png", "public, max-age=86400")
	if r := check("/admin/sw.js", "text/javascript", "no-cache"); r.Header.Get("Service-Worker-Allowed") != "/admin/" {
		t.Error("staff worker must be limited to /admin/")
	}
}

func TestStaticGzip(t *testing.T) {
	s := NewStatic(testUI(), time.Now())
	r := get(t, s, "/assets/customer-abc123.js", "Accept-Encoding", "gzip, br")
	if r.Header.Get("Content-Encoding") != "gzip" || !strings.Contains(r.Header.Get("Vary"), "Accept-Encoding") {
		t.Fatalf("headers: %v", r.Header)
	}
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)
	if !strings.Contains(string(plain), "رخ تیا") {
		t.Error("gzip body does not round-trip")
	}
	if r := get(t, s, "/assets/customer-abc123.js"); r.Header.Get("Content-Encoding") != "" {
		t.Error("compressed without Accept-Encoding")
	}
	if r := get(t, s, "/admin/icons/icon-192.png", "Accept-Encoding", "gzip"); r.Header.Get("Content-Encoding") != "" {
		t.Error("binary images must not be gzipped")
	}
}

func TestStaticNotReadyWithoutBuild(t *testing.T) {
	if NewStatic(fstest.MapFS{".gitkeep": {}}, time.Now()).Ready() {
		t.Error("an empty embed must not count as a frontend build")
	}
}

// API, health and media routes keep priority over the frontend catch-all, and API errors stay
// JSON and uncompressed (CSRF tokens never travel in compressed responses).
func TestRouterPriorityWithStatic(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		JSON(w, http.StatusOK, map[string]string{"api": r.URL.Path, "csrf_token": strings.Repeat("x", 2000)})
	})
	media := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := NewRouter(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), API: http.StripPrefix("/api/v1", api), Media: media,
		Static: NewStatic(testUI(), time.Now())})
	if r := get(t, h, "/api/v1/public/menu", "Accept-Encoding", "gzip"); r.StatusCode != 200 || r.Header.Get("Content-Encoding") != "" ||
		!strings.Contains(body(t, r), `"api":"/public/menu"`) {
		t.Errorf("API route: %d %v", r.StatusCode, r.Header)
	}
	if r := get(t, h, "/healthz"); r.StatusCode != 200 || !strings.Contains(body(t, r), "ok") {
		t.Errorf("healthz: %d", r.StatusCode)
	}
	if r := get(t, h, "/media/products/x.jpg"); r.StatusCode != http.StatusTeapot {
		t.Errorf("media: %d", r.StatusCode)
	}
	r := get(t, h, "/admin/orders")
	if r.StatusCode != 200 || !strings.Contains(body(t, r), "<title>admin</title>") || r.Header.Get("Content-Security-Policy") == "" {
		t.Errorf("staff deep link through the router: %d %v", r.StatusCode, r.Header)
	}
}
