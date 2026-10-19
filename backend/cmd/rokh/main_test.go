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
	t.Setenv("SHUTDOWN_TIMEOUT", "10s")

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

	// No keep-alives: a pooled connection that never sends a request stays "new" on the server,
	// and Shutdown waits up to 5s for those, which made this test flaky.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 5 * time.Second}
	base := "http://" + addr
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := client.Get(base + "/healthz")
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
	resp, err := client.Get(base + "/readyz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("readyz = %v %v", resp, err)
	} else {
		resp.Body.Close()
	}
	client.CloseIdleConnections()

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("shutdown took %v; something kept a connection open", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not shut down")
	}
	if _, err := client.Get(base + "/healthz"); err == nil {
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

func TestUserCreateCommand(t *testing.T) {
	db := testdb.New(t)
	t.Setenv("MIGRATION_DATABASE_URL", db.OwnerURL)
	if err := run([]string{"migrate", "up"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", db.AppURL)
	ctx := context.Background()
	args := []string{"-username", "Owner", "-display-name", "Café Owner", "-role", "admin"}

	var out bytes.Buffer
	if err := runUserCreate(ctx, args, strings.NewReader("owner-pass-2026\r\n"), &out); err != nil {
		t.Fatalf("create: %v (%s)", err, out.String())
	}
	if !strings.Contains(out.String(), `created ADMIN user "owner"`) {
		t.Errorf("output = %q", out.String())
	}
	if strings.Contains(out.String(), "owner-pass-2026") {
		t.Error("password echoed to output")
	}
	if err := runUserCreate(ctx, args, strings.NewReader("owner-pass-2026\n"), io.Discard); err == nil {
		t.Error("duplicate username must fail")
	}
	if err := runUserCreate(ctx, []string{"-username", "weak", "-display-name", "W"}, strings.NewReader("123\n"), io.Discard); err == nil ||
		!strings.Contains(err.Error(), "password") {
		t.Errorf("weak password: %v", err)
	}

	pool, err := database.Open(ctx, db.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var kind string
	if err := pool.QueryRow(ctx, `SELECT actor_kind FROM audit_logs WHERE action = 'user.create'`).Scan(&kind); err != nil || kind != "SYSTEM" {
		t.Errorf("audit actor = %q, %v", kind, err)
	}
}
