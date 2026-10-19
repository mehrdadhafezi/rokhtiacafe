package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://x@localhost/db")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AppEnv != EnvDevelopment || c.HTTPAddr != "127.0.0.1:8080" || c.LogFormat != "text" {
		t.Errorf("unexpected defaults: %+v", c)
	}
	if c.LogLevel != slog.LevelInfo || c.ShutdownTimeout != 15*time.Second {
		t.Errorf("unexpected defaults: level=%v shutdown=%v", c.LogLevel, c.ShutdownTimeout)
	}
	if c.Timezone.String() != "Asia/Tehran" {
		t.Errorf("timezone = %s", c.Timezone)
	}
	if c.AppOrigin != "http://127.0.0.1:5173" || c.CookieSecure || c.SessionIdle != 12*time.Hour || c.SessionMaxAge != 168*time.Hour {
		t.Errorf("unexpected auth defaults: origin=%s secure=%v idle=%v max=%v", c.AppOrigin, c.CookieSecure, c.SessionIdle, c.SessionMaxAge)
	}
}

func TestProductionRequiresSecureCookies(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://x@localhost/db")
	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_BASE_URL", "http://rokh.tiacafe.ir")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "COOKIE_SECURE") {
		t.Fatalf("expected https/secure error, got %v", err)
	}
	t.Setenv("APP_BASE_URL", "https://rokh.tiacafe.ir/menu")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.CookieSecure || c.AppOrigin != "https://rokh.tiacafe.ir" {
		t.Errorf("secure=%v origin=%s", c.CookieSecure, c.AppOrigin)
	}
}

func TestTrustedProxies(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://x@localhost/db")
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1, 10.0.0.0/8 ,::1")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.TrustedProxies) != 3 || c.TrustedProxies[0].String() != "127.0.0.1/32" || c.TrustedProxies[1].String() != "10.0.0.0/8" {
		t.Errorf("proxies = %v", c.TrustedProxies)
	}
	t.Setenv("TRUSTED_PROXIES", "not-an-ip")
	if _, err := Load(); err == nil {
		t.Error("expected error for invalid proxy")
	}
}

func TestLoadProductionUsesJSONLogs(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://x@localhost/db")
	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_BASE_URL", "https://rokh.tiacafe.ir")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.IsProduction() || c.LogFormat != "json" {
		t.Errorf("got env=%s format=%s", c.AppEnv, c.LogFormat)
	}
}

func TestLoadReportsAllErrors(t *testing.T) {
	clearEnv(t)
	t.Setenv("APP_ENV", "staging")
	t.Setenv("HTTP_ADDR", "no-port")
	t.Setenv("LOG_LEVEL", "loud")
	t.Setenv("APP_TIMEZONE", "Mars/Olympus")
	t.Setenv("SHUTDOWN_TIMEOUT", "-1s")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"APP_ENV", "HTTP_ADDR", "DATABASE_URL", "LOG_LEVEL", "APP_TIMEZONE", "SHUTDOWN_TIMEOUT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

func TestLoadDotEnvDoesNotOverrideEnvironment(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	content := "# comment\nexport HTTP_ADDR=127.0.0.1:9999\nDATABASE_URL=\"postgres://file\"\nLOG_LEVEL=debug\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOG_LEVEL", "warn")

	got, err := LoadDotEnv(filepath.Join(dir, "missing.env"), p)
	if err != nil || got != p {
		t.Fatalf("LoadDotEnv = %q, %v", got, err)
	}
	if v := os.Getenv("HTTP_ADDR"); v != "127.0.0.1:9999" {
		t.Errorf("HTTP_ADDR = %q", v)
	}
	if v := os.Getenv("DATABASE_URL"); v != "postgres://file" {
		t.Errorf("DATABASE_URL = %q (quotes not stripped?)", v)
	}
	if v := os.Getenv("LOG_LEVEL"); v != "warn" {
		t.Errorf("LOG_LEVEL = %q, existing env must win", v)
	}
}

func TestLoadDotEnvRejectsMalformedLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte("JUSTAKEY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDotEnv(p); err == nil {
		t.Fatal("expected error for malformed line")
	}
}

// clearEnv unsets every variable Load reads; t.Setenv restores the originals afterwards.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"APP_ENV", "HTTP_ADDR", "DATABASE_URL", "LOG_LEVEL", "LOG_FORMAT", "APP_TIMEZONE", "SHUTDOWN_TIMEOUT",
		"APP_BASE_URL", "COOKIE_SECURE", "SESSION_IDLE_TIMEOUT", "SESSION_MAX_AGE", "TRUSTED_PROXIES"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}
