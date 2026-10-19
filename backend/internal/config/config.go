// Package config loads and validates runtime configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	EnvDevelopment = "development"
	EnvTest        = "test"
	EnvProduction  = "production"
)

// Config holds settings needed by `rokh serve`.
type Config struct {
	AppEnv          string
	HTTPAddr        string
	DatabaseURL     string
	LogLevel        slog.Level
	LogFormat       string // "text" or "json"
	Timezone        *time.Location
	ShutdownTimeout time.Duration

	// AppOrigin is scheme://host[:port] of APP_BASE_URL; unsafe requests must come from it.
	AppOrigin      string
	CookieSecure   bool
	SessionIdle    time.Duration
	SessionMaxAge  time.Duration
	TrustedProxies []netip.Prefix

	UploadDir string
	Ordering  OrderingHours
}

// IsProduction reports whether the app runs in production mode.
func (c Config) IsProduction() bool { return c.AppEnv == EnvProduction }

// Load reads the server configuration. Missing or invalid values are reported together.
func Load() (Config, error) {
	var errs []error
	c := Config{
		AppEnv:    getenv("APP_ENV", EnvDevelopment),
		HTTPAddr:  getenv("HTTP_ADDR", "127.0.0.1:8080"),
		LogFormat: getenv("LOG_FORMAT", ""),
	}

	switch c.AppEnv {
	case EnvDevelopment, EnvTest, EnvProduction:
	default:
		errs = append(errs, fmt.Errorf("APP_ENV must be development, test or production, got %q", c.AppEnv))
	}

	if _, _, err := net.SplitHostPort(c.HTTPAddr); err != nil {
		errs = append(errs, fmt.Errorf("HTTP_ADDR: %w", err))
	}

	c.DatabaseURL = os.Getenv("DATABASE_URL")
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}

	if err := c.LogLevel.UnmarshalText([]byte(getenv("LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %w", err))
	}
	if c.LogFormat == "" {
		c.LogFormat = "text"
		if c.IsProduction() {
			c.LogFormat = "json"
		}
	}
	if c.LogFormat != "text" && c.LogFormat != "json" {
		errs = append(errs, fmt.Errorf("LOG_FORMAT must be text or json, got %q", c.LogFormat))
	}

	tz, err := time.LoadLocation(getenv("APP_TIMEZONE", "Asia/Tehran"))
	if err != nil {
		errs = append(errs, fmt.Errorf("APP_TIMEZONE: %w", err))
	}
	c.Timezone = tz

	c.ShutdownTimeout, err = time.ParseDuration(getenv("SHUTDOWN_TIMEOUT", "15s"))
	if err != nil || c.ShutdownTimeout <= 0 {
		errs = append(errs, fmt.Errorf("SHUTDOWN_TIMEOUT must be a positive duration"))
	}

	base, err := url.Parse(getenv("APP_BASE_URL", "http://127.0.0.1:5173"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		errs = append(errs, errors.New("APP_BASE_URL must be an absolute http(s) URL"))
	} else {
		c.AppOrigin = base.Scheme + "://" + base.Host
	}

	c.CookieSecure, err = strconv.ParseBool(getenv("COOKIE_SECURE", strconv.FormatBool(c.IsProduction())))
	if err != nil {
		errs = append(errs, errors.New("COOKIE_SECURE must be true or false"))
	}
	if c.IsProduction() && (!c.CookieSecure || !strings.HasPrefix(c.AppOrigin, "https://")) {
		errs = append(errs, errors.New("production requires COOKIE_SECURE=true and an https APP_BASE_URL"))
	}

	c.SessionIdle, err = time.ParseDuration(getenv("SESSION_IDLE_TIMEOUT", "12h"))
	if err != nil || c.SessionIdle <= 0 {
		errs = append(errs, errors.New("SESSION_IDLE_TIMEOUT must be a positive duration"))
	}
	c.SessionMaxAge, err = time.ParseDuration(getenv("SESSION_MAX_AGE", "168h"))
	if err != nil || c.SessionMaxAge < c.SessionIdle {
		errs = append(errs, errors.New("SESSION_MAX_AGE must be a duration >= SESSION_IDLE_TIMEOUT"))
	}

	for _, part := range strings.Split(os.Getenv("TRUSTED_PROXIES"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			if a, err := netip.ParseAddr(part); err == nil {
				part = netip.PrefixFrom(a, a.BitLen()).String()
			}
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			errs = append(errs, fmt.Errorf("TRUSTED_PROXIES: invalid entry %q", part))
			continue
		}
		c.TrustedProxies = append(c.TrustedProxies, p.Masked())
	}

	c.UploadDir = getenv("UPLOAD_DIR", "./data/uploads")
	c.Ordering, err = ParseOrderingHours(getenv("ORDERING_HOURS", "08:00-23:00"))
	if err != nil {
		errs = append(errs, err)
	}

	return c, errors.Join(errs...)
}

// MigrationDatabaseURL returns the schema-owner connection string used only by `rokh migrate`.
func MigrationDatabaseURL() (string, error) {
	u := os.Getenv("MIGRATION_DATABASE_URL")
	if u == "" {
		return "", errors.New("MIGRATION_DATABASE_URL is required for migrations")
	}
	return u, nil
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
