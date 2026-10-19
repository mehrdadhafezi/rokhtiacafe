package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func newTestRouter(db Pinger, prod bool) (http.Handler, *bytes.Buffer) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	return NewRouter(Deps{Logger: logger, DB: db, Production: prod}), &logs
}

func do(t *testing.T, h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("invalid JSON %q: %v", rec.Body.String(), err)
	}
	return m
}

func TestHealthz(t *testing.T) {
	h, _ := newTestRouter(fakePinger{}, false)
	rec := do(t, h, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK || decode(t, rec)["status"] != "ok" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestReadyz(t *testing.T) {
	h, _ := newTestRouter(fakePinger{}, false)
	if rec := do(t, h, http.MethodGet, "/readyz", nil); rec.Code != http.StatusOK {
		t.Fatalf("ready: got %d", rec.Code)
	}

	h, logs := newTestRouter(fakePinger{err: errors.New("connection refused")}, false)
	rec := do(t, h, http.MethodGet, "/readyz", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("not ready: got %d", rec.Code)
	}
	body := decode(t, rec)["error"].(map[string]any)
	if body["code"] != "NOT_READY" || strings.Contains(rec.Body.String(), "refused") {
		t.Errorf("body leaks detail or wrong code: %s", rec.Body)
	}
	if !strings.Contains(logs.String(), "connection refused") {
		t.Errorf("cause not logged: %s", logs)
	}
}

func TestRequestIDGeneratedAndPropagated(t *testing.T) {
	h, _ := newTestRouter(fakePinger{}, false)

	rec := do(t, h, http.MethodGet, "/healthz", nil)
	if id := rec.Header().Get(RequestIDHeader); len(id) != 24 {
		t.Errorf("generated id = %q", id)
	}

	rec = do(t, h, http.MethodGet, "/healthz", map[string]string{RequestIDHeader: "proxy-id-12345"})
	if id := rec.Header().Get(RequestIDHeader); id != "proxy-id-12345" {
		t.Errorf("incoming id not kept: %q", id)
	}

	rec = do(t, h, http.MethodGet, "/healthz", map[string]string{RequestIDHeader: "bad id\r\nx"})
	if id := rec.Header().Get(RequestIDHeader); id == "bad id\r\nx" || len(id) != 24 {
		t.Errorf("malformed id accepted: %q", id)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h, _ := newTestRouter(fakePinger{}, false)
	rec := do(t, h, http.MethodGet, "/healthz", nil)
	for _, k := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Permissions-Policy"} {
		if rec.Header().Get(k) == "" {
			t.Errorf("missing %s", k)
		}
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS must not be sent outside production")
	}

	h, _ = newTestRouter(fakePinger{}, true)
	if do(t, h, http.MethodGet, "/healthz", nil).Header().Get("Strict-Transport-Security") == "" {
		t.Error("HSTS missing in production")
	}
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	h, _ := newTestRouter(fakePinger{}, false)
	if rec := do(t, h, http.MethodGet, "/nope", nil); rec.Code != http.StatusNotFound || decode(t, rec)["error"] == nil {
		t.Errorf("404: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, http.MethodPost, "/healthz", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("405: %d", rec.Code)
	}
}

func TestRecoverReturnsEnvelopeWithoutDetails(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	r := chi.NewRouter()
	r.Use(RequestID, Recover(logger))
	r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("secret internal detail") })

	rec := do(t, r, http.MethodGet, "/boom", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("panic detail leaked to client: %s", rec.Body)
	}
	if !strings.Contains(logs.String(), "secret internal detail") {
		t.Errorf("panic not logged")
	}
}

func TestAccessLogOmitsQueryString(t *testing.T) {
	h, logs := newTestRouter(fakePinger{}, false)
	do(t, h, http.MethodGet, "/healthz?token=abc123", nil)
	line, _ := io.ReadAll(logs)
	if !bytes.Contains(line, []byte(`"route":"/healthz"`)) || bytes.Contains(line, []byte("abc123")) {
		t.Errorf("access log = %s", line)
	}
}
