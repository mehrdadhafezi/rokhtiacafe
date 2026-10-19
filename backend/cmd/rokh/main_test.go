package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/database"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/testdb"
)

func TestRunUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"bogus"}, &out); err == nil || !strings.Contains(out.String(), "usage") {
		t.Fatalf("err=%v out=%q", err, out.String())
	}
	if err := run(nil, &out); err == nil {
		t.Fatal("expected error with no command")
	}
}

func TestMigrateCommand(t *testing.T) {
	db := testdb.New(t)
	t.Setenv("MIGRATION_DATABASE_URL", db.OwnerURL)

	var out bytes.Buffer
	if err := run([]string{"migrate", "up"}, &out); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if !strings.Contains(out.String(), "applied 00001") {
		t.Errorf("up output = %q", out.String())
	}

	out.Reset()
	if err := run([]string{"migrate", "status"}, &out); err != nil {
		t.Fatalf("migrate status: %v", err)
	}
	if !strings.Contains(out.String(), ", 0 pending") {
		t.Errorf("status output = %q", out.String())
	}

	if err := run([]string{"migrate", "down"}, &out); err == nil {
		t.Error("migrate down must not be supported (forward-only)")
	}
}

func TestReadinessAgainstRealDatabase(t *testing.T) {
	db := testdb.New(t)
	pool, err := database.Open(context.Background(), db.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpx.NewRouter(httpx.Deps{Logger: logger, DB: readiness{q: store.New(pool)}}))
	defer srv.Close()

	if code := get(t, srv.URL+"/readyz"); code != http.StatusOK {
		t.Fatalf("readyz with DB up = %d", code)
	}
	pool.Close()
	if code := get(t, srv.URL+"/readyz"); code != http.StatusServiceUnavailable {
		t.Fatalf("readyz with pool closed = %d", code)
	}
}

// Starts the real serve() on a free port, checks both probes, then cancels the context
// (what SIGINT/SIGTERM does) and expects a clean shutdown.
func TestServeGracefulShutdown(t *testing.T) {
	db := testdb.New(t)
	t.Setenv("DATABASE_URL", db.AppURL)
	t.Setenv("APP_ENV", "test")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("SHUTDOWN_TIMEOUT", "5s")

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	t.Setenv("HTTP_ADDR", addr)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx) }()

	base := "http://" + addr
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("server did not start: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if code := get(t, base+"/readyz"); code != http.StatusOK {
		t.Errorf("readyz = %d", code)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not shut down")
	}
	if _, err := http.Get(base + "/healthz"); err == nil {
		t.Error("server still accepting connections after shutdown")
	}
}

func get(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}
