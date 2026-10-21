package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
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

// setProduction sets a complete, valid production configuration.
func setProduction(t *testing.T) {
	t.Helper()
	clearEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://rokh_app@localhost/rokhtiacafe")
	t.Setenv("APP_BASE_URL", "https://rokh.tiacafe.ir")
	t.Setenv("APP_SECRET_KEY", NewSecret())
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1")
	t.Setenv("UPLOAD_DIR", "/home/rokh/rokh/uploads")
}

func TestProductionRequiresSecureCookies(t *testing.T) {
	setProduction(t)
	t.Setenv("APP_BASE_URL", "http://rokh.tiacafe.ir")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "COOKIE_SECURE") {
		t.Fatalf("expected https/secure error, got %v", err)
	}
	t.Setenv("APP_BASE_URL", "https://rokh.tiacafe.ir/menu")
	t.Setenv("APP_SECRET_KEY", NewSecret())
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

// The MizbanCloud edge list published at https://mizbancloud.com/ips.txt (retrieved 2026-10-21)
// is accepted next to loopback, as are the narrowest boundaries of the guard (/16, /32).
func TestProductionAcceptsCDNEdgeRanges(t *testing.T) {
	setProduction(t)
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1,185.226.140.0/22,62.204.61.128/28,185.49.231.0/28,"+
		"193.37.37.208/28,62.204.61.40/29,77.72.80.224/28,31.25.90.192/28,152.233.66.88/29")
	c, err := Load()
	if err != nil {
		t.Fatalf("edge ranges rejected: %v", err)
	}
	if len(c.TrustedProxies) != 9 {
		t.Errorf("proxies = %v", c.TrustedProxies)
	}
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1,198.18.0.0/16,2001:db8::/32")
	if _, err := Load(); err != nil {
		t.Errorf("boundary prefixes rejected: %v", err)
	}
}

func TestLoadProductionUsesJSONLogs(t *testing.T) {
	setProduction(t)

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
		"APP_BASE_URL", "COOKIE_SECURE", "SESSION_IDLE_TIMEOUT", "SESSION_MAX_AGE", "TRUSTED_PROXIES", "UPLOAD_DIR", "ORDERING_HOURS",
		"ORDER_PENDING_TTL", "ORDER_MAX_PENDING", "ORDER_MAX_PENDING_PER_CLIENT", "APP_SECRET_KEY", "MIGRATION_DATABASE_URL",
		"VAPID_PUBLIC_KEY", "VAPID_PRIVATE_KEY", "VAPID_SUBJECT", "PUSH_HOST_ALLOWLIST"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func TestOrderingAndSecretConfig(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://x@localhost/db")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.OrderPendingTTL != 20*time.Minute || c.OrderMaxPending != 40 || c.OrderMaxPendingPerClient != 15 {
		t.Errorf("defaults: %v %d %d", c.OrderPendingTTL, c.OrderMaxPending, c.OrderMaxPendingPerClient)
	}
	if !c.SecretGenerated || len(c.SecretKey) != 32 {
		t.Error("development must generate a random secret when none is set")
	}

	t.Setenv("APP_SECRET_KEY", "dG9vLXNob3J0") // "too-short"
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "32 bytes") {
		t.Errorf("short secret: %v", err)
	}
	t.Setenv("APP_SECRET_KEY", NewSecret())
	t.Setenv("ORDER_PENDING_TTL", "10s")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ORDER_PENDING_TTL") {
		t.Errorf("short TTL: %v", err)
	}

	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://x@localhost/db")
	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_BASE_URL", "https://rokh.tiacafe.ir")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "APP_SECRET_KEY") {
		t.Errorf("production without secret: %v", err)
	}
}

func TestPushConfig(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://x@localhost/db")
	c, err := Load()
	if err != nil || c.PushEnabled() || c.PushAllowlist != DefaultPushAllowlist {
		t.Fatalf("push must be off by default with the documented allowlist: %v %+v", err, c.PushAllowlist)
	}

	// A valid pair (as printed by `rokh vapid gen`) needs a contact subject.
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("VAPID_PUBLIC_KEY", pub)
	t.Setenv("VAPID_PRIVATE_KEY", priv)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "VAPID_SUBJECT") {
		t.Errorf("missing subject: %v", err)
	}
	t.Setenv("VAPID_SUBJECT", "mailto:owner@example.com")
	if c, err = Load(); err != nil || !c.PushEnabled() {
		t.Fatalf("valid push config: %v", err)
	}

	t.Setenv("VAPID_PRIVATE_KEY", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "VAPID_PRIVATE_KEY") {
		t.Errorf("half-configured keys must be rejected: %v", err)
	}
	t.Setenv("VAPID_PRIVATE_KEY", "not-a-key")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "VAPID_PRIVATE_KEY") {
		t.Errorf("malformed private key: %v", err)
	}
}

func TestProductionDeploymentRules(t *testing.T) {
	setProduction(t)
	if _, err := Load(); err != nil {
		t.Fatalf("valid production config rejected: %v", err)
	}
	cases := []struct{ key, value, want string }{
		{"HTTP_ADDR", "0.0.0.0:8080", "loopback"},
		{"HTTP_ADDR", "62.204.61.18:8080", "loopback"},
		{"TRUSTED_PROXIES", "", "TRUSTED_PROXIES"},
		{"TRUSTED_PROXIES", "127.0.0.1,0.0.0.0/0", "too broad"},
		{"TRUSTED_PROXIES", "127.0.0.1,::/0", "too broad"},
		{"TRUSTED_PROXIES", "127.0.0.1,10.0.0.0/8", "too broad"},
		{"TRUSTED_PROXIES", "127.0.0.1,185.226.0.0/15", "too broad"},
		{"TRUSTED_PROXIES", "127.0.0.1,2001:db8::/31", "too broad"},
		{"UPLOAD_DIR", "./data/uploads", "UPLOAD_DIR"},
		{"DATABASE_URL", "postgres://postgres@localhost/rokhtiacafe", "app role"},
		{"DATABASE_URL", "postgres://rokh_owner@localhost/rokhtiacafe", "app role"},
	}
	for _, c := range cases {
		setProduction(t)
		t.Setenv(c.key, c.value)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s=%q: want error mentioning %q, got %v", c.key, c.value, c.want, err)
		}
	}
	// The owner role named in MIGRATION_DATABASE_URL is refused at runtime too.
	setProduction(t)
	t.Setenv("MIGRATION_DATABASE_URL", "postgres://cafe_owner@localhost/rokhtiacafe")
	t.Setenv("DATABASE_URL", "postgres://cafe_owner@localhost/rokhtiacafe")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "app role") {
		t.Errorf("owner role as runtime user: %v", err)
	}
	setProduction(t)
	t.Setenv("HTTP_ADDR", "localhost:8080")
	if _, err := Load(); err != nil {
		t.Errorf("localhost must be accepted: %v", err)
	}
	// Development keeps its permissive defaults.
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://postgres@localhost/db")
	t.Setenv("HTTP_ADDR", "0.0.0.0:8080")
	if _, err := Load(); err != nil {
		t.Errorf("development must not apply production rules: %v", err)
	}
}
