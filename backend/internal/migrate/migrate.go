// Package migrate applies the embedded goose migrations as the schema-owner role.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/db"
)

// Status is one migration's state.
type Status struct {
	Version int64
	Path    string
	Applied bool
}

// Migrator wraps a goose provider and the connection it owns.
type Migrator struct {
	db       *sql.DB
	provider *goose.Provider
}

// New connects with the owner URL. Close must be called when done.
func New(ownerURL string, logger *slog.Logger) (*Migrator, error) {
	cfg, err := pgx.ParseConfig(ownerURL)
	if err != nil {
		return nil, fmt.Errorf("parse migration database url: %w", err)
	}
	cfg.RuntimeParams["search_path"] = "rokh"
	cfg.RuntimeParams["application_name"] = "rokh-migrate"
	sqlDB := stdlib.OpenDB(*cfg)

	migrations, err := fs.Sub(db.Migrations, "migrations")
	if err != nil {
		sqlDB.Close()
		return nil, err
	}
	// A session-level advisory lock stops two `rokh migrate` runs from racing.
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		sqlDB.Close()
		return nil, err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations,
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
		goose.WithSlog(logger),
	)
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("create migration provider: %w", err)
	}
	return &Migrator{db: sqlDB, provider: p}, nil
}

// Up applies all pending migrations and returns the versions applied.
func (m *Migrator) Up(ctx context.Context) ([]int64, error) {
	results, err := m.provider.Up(ctx)
	applied := make([]int64, 0, len(results))
	for _, r := range results {
		if r.Error == nil {
			applied = append(applied, r.Source.Version)
		}
	}
	return applied, err
}

// Status lists every known migration and whether it is applied.
func (m *Migrator) Status(ctx context.Context) ([]Status, error) {
	st, err := m.provider.Status(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Status, 0, len(st))
	for _, s := range st {
		out = append(out, Status{Version: s.Source.Version, Path: s.Source.Path, Applied: s.State == goose.StateApplied})
	}
	return out, nil
}

// Close releases the provider and its connection.
func (m *Migrator) Close() error {
	return m.provider.Close()
}
