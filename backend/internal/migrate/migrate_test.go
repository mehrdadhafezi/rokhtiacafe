package migrate_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/database"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/migrate"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/testdb"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func migrateUp(t *testing.T, ownerURL string) []int64 {
	t.Helper()
	m, err := migrate.New(ownerURL, quiet)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer m.Close()
	applied, err := m.Up(context.Background())
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	return applied
}

func TestUpIsIdempotentAndStatusIsClean(t *testing.T) {
	db := testdb.New(t)

	if got := migrateUp(t, db.OwnerURL); len(got) == 0 || got[0] != 1 {
		t.Fatalf("first Up applied %v, want [1 ...]", got)
	}
	if got := migrateUp(t, db.OwnerURL); len(got) != 0 {
		t.Fatalf("second Up applied %v, want none", got)
	}

	m, err := migrate.New(db.OwnerURL, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	st, err := m.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st {
		if !s.Applied {
			t.Errorf("migration %d (%s) still pending", s.Version, s.Path)
		}
	}
}

func TestConcurrentMigrationsApplyOnce(t *testing.T) {
	db := testdb.New(t)

	var wg sync.WaitGroup
	results := make([][]int64, 4)
	errs := make([]error, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := migrate.New(db.OwnerURL, quiet)
			if err != nil {
				errs[i] = err
				return
			}
			defer m.Close()
			results[i], errs[i] = m.Up(context.Background())
		}()
	}
	wg.Wait()

	total := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("runner %d: %v", i, errs[i])
		}
		total += len(results[i])
	}
	if total != len(migrateUpVersions(t, db.OwnerURL)) {
		t.Fatalf("migrations applied %d times in total across runners, want each exactly once", total)
	}
}

func migrateUpVersions(t *testing.T, ownerURL string) []int64 {
	t.Helper()
	m, err := migrate.New(ownerURL, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	st, err := m.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var v []int64
	for _, s := range st {
		v = append(v, s.Version)
	}
	return v
}

// The runtime role must be able to query and write, but never run DDL or DELETE by default
// (docs/DATABASE.md §6.1).
func TestRuntimeRolePrivileges(t *testing.T) {
	db := testdb.New(t)
	migrateUp(t, db.OwnerURL)
	ctx := context.Background()

	owner, err := database.Open(ctx, db.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	// A table created by the owner after the grants migration inherits default privileges.
	if _, err := owner.Exec(ctx, "CREATE TABLE privilege_probe (id int PRIMARY KEY, v text)"); err != nil {
		t.Fatalf("owner create table: %v", err)
	}

	app, err := database.Open(ctx, db.AppURL)
	if err != nil {
		t.Fatalf("app role connect: %v", err)
	}
	defer app.Close()

	if ok, err := store.New(app).Ping(ctx); err != nil || ok != 1 {
		t.Fatalf("sqlc Ping as rokh_app = %d, %v", ok, err)
	}
	for _, stmt := range []string{
		"INSERT INTO privilege_probe VALUES (1, 'a')",
		"UPDATE privilege_probe SET v = 'b' WHERE id = 1",
		"SELECT count(*) FROM privilege_probe",
	} {
		if _, err := app.Exec(ctx, stmt); err != nil {
			t.Errorf("rokh_app should be allowed %q: %v", stmt, err)
		}
	}
	for _, stmt := range []string{
		"DELETE FROM privilege_probe",
		"TRUNCATE privilege_probe",
		"CREATE TABLE app_made (id int)",
		"DROP TABLE privilege_probe",
		"ALTER TABLE privilege_probe DISABLE TRIGGER ALL",
	} {
		_, err := app.Exec(ctx, stmt)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || (pgErr.Code != "42501" && pgErr.Code != "42P01") {
			t.Errorf("rokh_app must be denied %q, got %v", stmt, err)
		}
	}
}
