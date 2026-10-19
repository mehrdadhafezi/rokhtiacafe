package check_test

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/check"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/database"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/migrate"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/testdb"
)

func setup(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	db := testdb.New(t)
	m, err := migrate.New(db.OwnerURL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.Close()
	ctx := context.Background()
	app, err := database.Open(ctx, db.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return app, ctx
}

func mustExec(t *testing.T, p *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := p.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func run(t *testing.T, p *pgxpool.Pool) []string {
	t.Helper()
	vs, err := check.Run(context.Background(), store.New(p))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.String()
	}
	return out
}

func r32() []byte { b := make([]byte, 32); _, _ = rand.Read(b); return b }

func TestCheckPassesOnEmptyAndConsistentData(t *testing.T) {
	app, _ := setup(t)
	if v := run(t, app); len(v) != 0 {
		t.Fatalf("empty database: %v", v)
	}

	mustExec(t, app, `INSERT INTO users (username, display_name, password_hash, role) VALUES ('admin', 'A', 'x', 'ADMIN')`)
	mustExec(t, app, `INSERT INTO categories (name) VALUES ('c')`)
	mustExec(t, app, `INSERT INTO products (category_id, name, price_rial, inventory_mode) VALUES (1, 'Soda', 50000, 'TRACKED')`)
	mustExec(t, app, `INSERT INTO inventory_balances (product_id, on_hand, reserved, inventory_value_rial) VALUES (1, 5, 2, 500)`)
	mustExec(t, app, `INSERT INTO inventory_movements (product_id, movement_type, reason_code, quantity_delta, value_delta_rial,
		on_hand_after, value_after_rial, note) VALUES (1, 'ADJUSTMENT_IN', 'OPENING_BALANCE', 5, 500, 5, 500, 'open')`)
	mustExec(t, app, `INSERT INTO orders (business_date, display_number, tracking_token_hash, idempotency_key, request_hash,
		client_key, total_rial, expires_at) VALUES (current_date, 1, $1, gen_random_uuid(), $2, $3, 100000, now() + interval '20 minutes')`,
		r32(), r32(), r32())
	mustExec(t, app, `INSERT INTO order_items (order_id, product_id, product_name, inventory_mode, unit_price_rial, quantity, line_total_rial)
		VALUES (1, 1, 'Soda', 'TRACKED', 50000, 2, 100000)`)

	if v := run(t, app); len(v) != 0 {
		t.Fatalf("consistent data reported violations: %v", v)
	}
}

// R-F6-3: deliberately corrupted values are detected.
func TestCheckDetectsCorruption(t *testing.T) {
	app, _ := setup(t)
	mustExec(t, app, `INSERT INTO users (username, display_name, password_hash, role, is_active) VALUES ('operator', 'O', 'x', 'OPERATOR', true)`)
	mustExec(t, app, `INSERT INTO categories (name) VALUES ('c')`)
	mustExec(t, app, `INSERT INTO products (category_id, name, price_rial, inventory_mode) VALUES (1, 'Soda', 50000, 'TRACKED')`)
	mustExec(t, app, `INSERT INTO products (category_id, name, price_rial, inventory_mode) VALUES (1, 'Chips', 50000, 'TRACKED')`)
	// reserved without any open order, and on_hand without ledger rows
	mustExec(t, app, `INSERT INTO inventory_balances (product_id, on_hand, reserved, inventory_value_rial) VALUES (1, 4, 3, 400)`)
	mustExec(t, app, `INSERT INTO orders (business_date, display_number, tracking_token_hash, idempotency_key, request_hash,
		client_key, total_rial, expires_at, status, accepted_at, paid_rial)
		VALUES (current_date, 1, $1, gen_random_uuid(), $2, $3, 100000, now() + interval '20 minutes', 'PREPARING', now(), 5000)`,
		r32(), r32(), r32())
	mustExec(t, app, `INSERT INTO order_items (order_id, product_id, product_name, inventory_mode, unit_price_rial, quantity, line_total_rial)
		VALUES (1, 1, 'Soda', 'TRACKED', 50000, 1, 50000)`)

	got := strings.Join(run(t, app), "\n")
	for _, want := range []string{
		"INV-3: product 1 reserved=3 but open tracked order quantity=1",
		"INV-4: product 1 on_hand=4 value=400 but ledger qty=0 value=0",
		"INV-4: tracked product 2 has no inventory balance row",
		"INV-7b: order 1 total=100000 but lines sum to 50000",
		"INV-11: order 1 paid=5000 refunded=0 but payment rows paid=0 refunded=0",
		"INV-16: 1 users exist but none is an active ADMIN",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing violation %q in:\n%s", want, got)
		}
	}
}
