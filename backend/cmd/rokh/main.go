// Command rokh is the Rokh Tia Café server and its operational subcommands.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // Asia/Tehran must resolve on Windows and minimal servers.

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/config"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/database"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/migrate"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: rokh <command>

commands:
  serve            run the HTTP server
  migrate up       apply pending migrations (uses MIGRATION_DATABASE_URL)
  migrate status   list migrations and whether they are applied
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

	handler := httpx.NewRouter(httpx.Deps{
		Logger:     logger,
		DB:         readiness{q: store.New(pool)},
		Production: cfg.IsProduction(),
	})
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
