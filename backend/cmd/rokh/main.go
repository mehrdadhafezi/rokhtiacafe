// Command rokh is the Rokh Tia Café server and its operational subcommands.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // Asia/Tehran must resolve on Windows and minimal servers.

	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/term"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/api"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/audit"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/auth"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/catalog"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/check"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/config"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/database"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/migrate"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/ratelimit"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/users"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: rokh <command>

commands:
  serve            run the HTTP server
  migrate up       apply pending migrations (uses MIGRATION_DATABASE_URL)
  migrate status   list migrations and whether they are applied
  check            verify app-enforced data invariants (read-only, uses DATABASE_URL)
  user create      create a staff account; password is read from stdin
                   flags: -username NAME -display-name NAME -role ADMIN|OPERATOR
  version          print the build version
`

func main() {
	// Local convenience: read .env from the working directory or the repo root.
	// Variables already set in the environment always take precedence.
	if _, err := config.LoadDotEnv(".env", "../.env"); err != nil {
		fmt.Fprintln(os.Stderr, "load .env:", err)
		os.Exit(1)
	}
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return errors.New("missing command")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "serve":
		return serve(ctx)
	case "migrate":
		if len(args) != 2 {
			return errors.New("usage: rokh migrate up|status")
		}
		return runMigrate(ctx, args[1], stdout)
	case "check":
		return runCheck(ctx, stdout)
	case "user":
		if len(args) < 2 || args[1] != "create" {
			return errors.New("usage: rokh user create -username NAME -display-name NAME -role ADMIN|OPERATOR")
		}
		return runUserCreate(ctx, args[2:], os.Stdin, stdout)
	case "version":
		fmt.Fprintln(stdout, version)
		return nil
	default:
		fmt.Fprint(stdout, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

func serve(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	logger := newLogger(cfg)
	slog.SetDefault(logger)

	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	images, err := catalog.NewImageStore(cfg.UploadDir)
	if err != nil {
		return err
	}
	queries := store.New(pool)
	sessions := auth.NewSessions(auth.SessionConfig{IdleTimeout: cfg.SessionIdle, MaxAge: cfg.SessionMaxAge})
	apiHandler := api.New(api.Deps{
		Pool:   pool,
		Logger: logger,
		Auth: &auth.Middleware{
			Sessions: sessions, Queries: queries, AllowedOrigin: cfg.AppOrigin, Logger: logger,
			Cookie: auth.CookieConfig{Secure: cfg.CookieSecure, MaxAge: cfg.SessionMaxAge},
		},
		TrustedProxies: cfg.TrustedProxies,
		LoginPerIP:     ratelimit.New(20, 15*time.Minute),
		LoginPerUser:   ratelimit.New(5, 15*time.Minute),
		PublicPerIP:    ratelimit.New(600, time.Minute),
		Images:         images,
		Ordering:       cfg.Ordering,
		Location:       cfg.Timezone,
	})
	handler := httpx.NewRouter(httpx.Deps{
		Logger:     logger,
		DB:         readiness{q: queries},
		Production: cfg.IsProduction(),
		API:        apiHandler.Handler(),
		Media:      images.MediaHandler(),
	})
	go cleanupSessions(ctx, queries, cfg.SessionIdle, logger)
	srv := httpx.NewServer(cfg.HTTPAddr, handler, logger)

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server starting", "addr", cfg.HTTPAddr, "env", cfg.AppEnv, "version", version)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	logger.Info("shutting down", "timeout", cfg.ShutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("server stopped")
	return nil
}

// readiness checks the database with a real query through the runtime role.
type readiness struct{ q *store.Queries }

func (r readiness) Ping(ctx context.Context) error {
	ok, err := r.q.Ping(ctx)
	if err != nil {
		return err
	}
	if ok != 1 {
		return fmt.Errorf("unexpected ping result %d", ok)
	}
	return nil
}

func runMigrate(ctx context.Context, cmd string, stdout io.Writer) error {
	url, err := config.MigrationDatabaseURL()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m, err := migrate.New(url, logger)
	if err != nil {
		return err
	}
	defer m.Close()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	switch cmd {
	case "up":
		applied, err := m.Up(ctx)
		for _, v := range applied {
			fmt.Fprintf(stdout, "applied %05d\n", v)
		}
		if err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
		if len(applied) == 0 {
			fmt.Fprintln(stdout, "no pending migrations")
		}
		return nil
	case "status":
		st, err := m.Status(ctx)
		if err != nil {
			return fmt.Errorf("migrate status: %w", err)
		}
		pending := 0
		for _, s := range st {
			state := "applied"
			if !s.Applied {
				state = "pending"
				pending++
			}
			fmt.Fprintf(stdout, "%05d  %-8s %s\n", s.Version, state, s.Path)
		}
		fmt.Fprintf(stdout, "%d migrations, %d pending\n", len(st), pending)
		return nil
	default:
		return fmt.Errorf("unknown migrate command %q (want up or status)", cmd)
	}
}

// errCheckFailed is returned when invariants are violated, so the exit code is non-zero.
var errCheckFailed = errors.New("invariant check failed")

func runCheck(ctx context.Context, stdout io.Writer) error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required")
	}
	pool, err := database.Open(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()

	violations, err := check.Run(ctx, store.New(pool))
	if err != nil {
		return err
	}
	if len(violations) == 0 {
		fmt.Fprintln(stdout, "ok: all invariants hold")
		return nil
	}
	for _, v := range violations {
		fmt.Fprintln(stdout, v)
	}
	fmt.Fprintf(stdout, "%d violation(s)\n", len(violations))
	return errCheckFailed
}

// cleanupSessions deletes expired and idle sessions hourly (docs/ARCHITECTURE.md §2.5).
func cleanupSessions(ctx context.Context, q *store.Queries, idle time.Duration, logger *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		n, err := q.DeleteExpiredSessions(ctx, pgtype.Timestamptz{Time: time.Now().Add(-idle), Valid: true})
		if err != nil && ctx.Err() == nil {
			logger.Warn("session cleanup failed", "err", err)
		} else if n > 0 {
			logger.Info("expired sessions removed", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// runUserCreate creates a staff account. The password comes from stdin (never args or env),
// so it does not end up in shell history or process listings.
func runUserCreate(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("user create", flag.ContinueOnError)
	fs.SetOutput(stdout)
	username := fs.String("username", "", "login name (a-z, 0-9, _ and ., 3-32 chars)")
	displayName := fs.String("display-name", "", "name shown in the app")
	role := fs.String("role", "ADMIN", "ADMIN or OPERATOR")
	if err := fs.Parse(args); err != nil {
		return err
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required")
	}

	fmt.Fprint(stdout, "Password (min 10 characters): ")
	password, err := readPassword(stdin)
	fmt.Fprintln(stdout)
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}

	pool, err := database.Open(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()

	svc := &users.Service{Pool: pool}
	u, err := svc.Create(ctx, audit.System, users.CreateInput{
		Username: *username, DisplayName: *displayName, Role: strings.ToUpper(*role), Password: password,
	})
	var bad users.ValidationError
	if errors.As(err, &bad) {
		return fmt.Errorf("invalid input: %v", map[string]string(bad))
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "created %s user %q (id %d)\n", u.Role, u.Username, u.ID)
	return nil
}

// readPassword reads one line without echo when stdin is an interactive terminal, and as a plain
// line otherwise (piped input in scripts and tests).
func readPassword(stdin io.Reader) (string, error) {
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		b, err := term.ReadPassword(int(f.Fd()))
		return string(b), err
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
