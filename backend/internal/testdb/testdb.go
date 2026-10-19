// Package testdb gives integration tests an isolated, freshly created PostgreSQL database
// with the same role layout as production (owner for DDL, app role for runtime).
//
// Required environment (see .env.example): TEST_DATABASE_ADMIN_URL (superuser),
// MIGRATION_DATABASE_URL (rokh_owner) and DATABASE_URL (rokh_app). Tests are skipped when
// TEST_DATABASE_ADMIN_URL is unset, unless REQUIRE_DB_TESTS=1 (set in CI), which fails them.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/config"
)

// DB is one throwaway database.
type DB struct {
	Name     string
	OwnerURL string // rokh_owner — run migrations with this
	AppURL   string // rokh_app — what the server uses
}

// New creates a database and drops it when the test finishes.
func New(t testing.TB) *DB {
	t.Helper()
	loadRepoDotEnv()

	admin := os.Getenv("TEST_DATABASE_ADMIN_URL")
	if admin == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "1" {
			t.Fatal("REQUIRE_DB_TESTS=1 but TEST_DATABASE_ADMIN_URL is not set")
		}
		t.Skip("TEST_DATABASE_ADMIN_URL not set; skipping PostgreSQL integration test")
	}
	ownerBase, appBase := os.Getenv("MIGRATION_DATABASE_URL"), os.Getenv("DATABASE_URL")
	if ownerBase == "" || appBase == "" {
		t.Fatal("MIGRATION_DATABASE_URL and DATABASE_URL must be set for integration tests")
	}

	var b [6]byte
	_, _ = rand.Read(b[:])
	name := "rokh_test_" + hex.EncodeToString(b[:])

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer conn.Close(ctx)
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+ident+" OWNER rokh_owner"); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, err := pgx.Connect(ctx, admin)
		if err != nil {
			t.Logf("cleanup connect: %v", err)
			return
		}
		defer c.Close(ctx)
		if _, err := c.Exec(ctx, "DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)"); err != nil {
			t.Logf("drop %s: %v", name, err)
		}
	})

	// Same per-database setup as deploy/local/postgres/roles.sql.
	dbConn, err := pgx.Connect(ctx, withDatabase(t, admin, name))
	if err != nil {
		t.Fatalf("connect new database: %v", err)
	}
	defer dbConn.Close(ctx)
	for _, stmt := range []string{
		"CREATE SCHEMA rokh AUTHORIZATION rokh_owner",
		"REVOKE CREATE ON SCHEMA public FROM PUBLIC",
		"REVOKE ALL ON DATABASE " + ident + " FROM PUBLIC",
		"GRANT CONNECT ON DATABASE " + ident + " TO rokh_app",
	} {
		if _, err := dbConn.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	return &DB{Name: name, OwnerURL: withDatabase(t, ownerBase, name), AppURL: withDatabase(t, appBase, name)}
}

func withDatabase(t testing.TB, raw, name string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// loadRepoDotEnv lets `go test` pick up the developer's .env from the repository root.
func loadRepoDotEnv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for range 6 {
		if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
			_, _ = config.LoadDotEnv(filepath.Join(dir, ".env"))
			return
		}
		dir = filepath.Dir(dir)
	}
}
