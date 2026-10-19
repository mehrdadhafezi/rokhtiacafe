package migrate_test

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/database"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/testdb"
)

// SQLSTATEs asserted below.
const (
	codeCheck      = "23514"
	codeUnique     = "23505"
	codeNotNull    = "23502"
	codePrivilege  = "42501"
	codeNoDelete   = "RK001"
	codeImmutable  = "RK002"
	codeTransition = "RK003"
)

type fixture struct {
	t     *testing.T
	ctx   context.Context
	owner *pgxpool.Pool // rokh_owner: schema owner
	app   *pgxpool.Pool // rokh_app: what the server uses

	userID, categoryID, trackedID, untrackedID int64
}

// newFixture migrates a fresh database and seeds one user, category, tracked and untracked product.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := testdb.New(t)
	migrateUp(t, db.OwnerURL)
	ctx := context.Background()

	owner, err := database.Open(ctx, db.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	app, err := database.Open(ctx, db.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)

	f := &fixture{t: t, ctx: ctx, owner: owner, app: app}
	f.userID = f.id(`INSERT INTO users (username, display_name, password_hash, role) VALUES ('admin', 'Admin', 'x', 'ADMIN') RETURNING id`)
	f.categoryID = f.id(`INSERT INTO categories (name) VALUES ('Drinks') RETURNING id`)
	f.trackedID = f.id(`INSERT INTO products (category_id, name, price_rial, inventory_mode, low_stock_threshold)
		VALUES ($1, 'Soda', 50000, 'TRACKED', 3) RETURNING id`, f.categoryID)
	f.untrackedID = f.id(`INSERT INTO products (category_id, name, price_rial, inventory_mode, estimated_unit_cost_rial)
		VALUES ($1, 'Espresso', 90000, 'UNTRACKED', 30000) RETURNING id`, f.categoryID)
	f.exec(`INSERT INTO inventory_balances (product_id) VALUES ($1)`, f.trackedID)
	return f
}

func (f *fixture) id(sql string, args ...any) int64 {
	f.t.Helper()
	var id int64
	if err := f.app.QueryRow(f.ctx, sql, args...).Scan(&id); err != nil {
		f.t.Fatalf("%s: %v", oneLine(sql), err)
	}
	return id
}

func (f *fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.app.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatalf("%s: %v", oneLine(sql), err)
	}
}

// fails asserts that sql fails with the given SQLSTATE and, if name is set, that the
// constraint name (or message) mentions it.
func (f *fixture) fails(pool *pgxpool.Pool, code, name, sql string, args ...any) {
	f.t.Helper()
	_, err := pool.Exec(f.ctx, sql, args...)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		f.t.Errorf("expected %s for %q, got %v", code, oneLine(sql), err)
		return
	}
	if pgErr.Code != code {
		f.t.Errorf("expected %s for %q, got %s (%s)", code, oneLine(sql), pgErr.Code, pgErr.Message)
		return
	}
	if name != "" && pgErr.ConstraintName != name && !strings.Contains(pgErr.Message, name) {
		f.t.Errorf("expected %q in error for %q, got constraint=%q msg=%q", name, oneLine(sql), pgErr.ConstraintName, pgErr.Message)
	}
}

func random32() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b
}

var displayNumber int

// newOrder inserts a PENDING order with one tracked line of qty 2 at 50,000 (total 100,000).
func (f *fixture) newOrder() int64 {
	f.t.Helper()
	displayNumber++
	id := f.id(`INSERT INTO orders (business_date, display_number, tracking_token_hash, idempotency_key,
			request_hash, client_key, total_rial, expires_at)
		VALUES (current_date, $1, $2, gen_random_uuid(), $3, $4, 100000, now() + interval '20 minutes')
		RETURNING id`, displayNumber, random32(), random32(), random32())
	f.exec(`INSERT INTO order_items (order_id, product_id, product_name, inventory_mode, unit_price_rial, quantity, line_total_rial)
		VALUES ($1, $2, 'Soda', 'TRACKED', 50000, 2, 100000)`, id, f.trackedID)
	return id
}

func (f *fixture) accept(orderID int64) {
	f.exec(`UPDATE orders SET status = 'PREPARING', accepted_at = now(), accepted_by = $2 WHERE id = $1`, orderID, f.userID)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestSchemaHasAllApprovedTables(t *testing.T) {
	f := newFixture(t)
	want := []string{"audit_logs", "categories", "expenses", "inventory_balances", "inventory_movements",
		"notification_events", "order_items", "orders", "payments", "products", "purchase_entries",
		"push_subscriptions", "sessions", "users"}
	rows, err := f.owner.Query(f.ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'rokh'
		AND tablename <> 'goose_db_version' ORDER BY tablename`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		got = append(got, n)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tables = %v\nwant     %v", got, want)
	}
	var owner string
	if err := f.owner.QueryRow(f.ctx, `SELECT DISTINCT tableowner FROM pg_tables WHERE schemaname = 'rokh'`).Scan(&owner); err != nil || owner != "rokh_owner" {
		t.Errorf("tables owned by %q (%v), want rokh_owner only", owner, err)
	}
}

// R-F6-1: void reasons are required and NULL-safe.
func TestVoidReasonIsRequired(t *testing.T) {
	f := newFixture(t)
	f.fails(f.app, codeCheck, "expenses_void_reason", `INSERT INTO expenses (category, amount_rial, expense_date, description,
		idempotency_key, created_by, voided_at, voided_by) VALUES ('OTHER', 10, current_date, 'x', gen_random_uuid(), $1, now(), $1)`, f.userID)
	f.fails(f.app, codeCheck, "expenses_void_reason", `INSERT INTO expenses (category, amount_rial, expense_date, description,
		idempotency_key, created_by, voided_at, voided_by, void_reason) VALUES ('OTHER', 10, current_date, 'x', gen_random_uuid(), $1, now(), $1, '   ')`, f.userID)
	f.fails(f.app, codeCheck, "purchase_entries_void_reason", `INSERT INTO purchase_entries (product_id, quantity, unit_cost_rial,
		total_cost_rial, purchased_on, idempotency_key, created_by, voided_at, voided_by)
		VALUES ($1, 1, 10, 10, current_date, gen_random_uuid(), $2, now(), $2)`, f.trackedID, f.userID)

	order := f.newOrder()
	f.accept(order)
	payID := f.id(`INSERT INTO payments (order_id, kind, method, amount_rial, idempotency_key, created_by)
		VALUES ($1, 'PAYMENT', 'CASH', 1000, gen_random_uuid(), $2) RETURNING id`, order, f.userID)
	f.fails(f.app, codeCheck, "payments_void_reason", `UPDATE payments SET voided_at = now(), voided_by = $2 WHERE id = $1`, payID, f.userID)
	f.exec(`UPDATE payments SET voided_at = now(), voided_by = $2, void_reason = 'wrong method' WHERE id = $1`, payID, f.userID)
}

// R-F6-2: adjustment reason codes and notes.
func TestMovementReasonRules(t *testing.T) {
	f := newFixture(t)
	insert := `INSERT INTO inventory_movements (product_id, movement_type, reason_code, quantity_delta, value_delta_rial,
		on_hand_after, value_after_rial, note) VALUES ($1, $2, $3, $4, 0, 1, 0, $5)`
	f.fails(f.app, codeCheck, "inventory_movements_reason_code", insert, f.trackedID, "ADJUSTMENT_IN", nil, 1, "found")
	f.fails(f.app, codeCheck, "inventory_movements_reason_code", insert, f.trackedID, "ADJUSTMENT_IN", "DAMAGED", 1, "wrong code for IN")
	f.fails(f.app, codeCheck, "inventory_movements_adjustment_note", insert, f.trackedID, "ADJUSTMENT_IN", "OPENING_BALANCE", 1, " ")
	f.fails(f.app, codeCheck, "inventory_movements_adjustment_note", insert, f.trackedID, "ADJUSTMENT_IN", "OPENING_BALANCE", 1, nil)
	f.fails(f.app, codeCheck, "inventory_movements_direction", insert, f.trackedID, "ADJUSTMENT_OUT", "DAMAGED", 1, "positive out")
	f.exec(insert, f.trackedID, "ADJUSTMENT_IN", "OPENING_BALANCE", 1, "opening stock")

	order := f.newOrder()
	f.fails(f.app, codeCheck, "inventory_movements_reason_code", `INSERT INTO inventory_movements (product_id, movement_type, reason_code,
		quantity_delta, value_delta_rial, on_hand_after, value_after_rial, order_id)
		VALUES ($1, 'SALE', 'DAMAGED', -1, 0, 0, 0, $2)`, f.trackedID, order)
	f.fails(f.app, codeCheck, "inventory_movements_order_link", `INSERT INTO inventory_movements (product_id, movement_type,
		quantity_delta, value_delta_rial, on_hand_after, value_after_rial) VALUES ($1, 'SALE', -1, 0, 0, 0)`, f.trackedID)
}

// INV-1/INV-2/INV-5.
func TestInventoryConstraints(t *testing.T) {
	f := newFixture(t)
	f.fails(f.app, codeCheck, "inventory_reserved_le_on_hand", `UPDATE inventory_balances SET reserved = 1 WHERE product_id = $1`, f.trackedID)
	f.fails(f.app, codeCheck, "", `UPDATE inventory_balances SET on_hand = -1 WHERE product_id = $1`, f.trackedID)
	f.fails(f.app, codeCheck, "inventory_zero_stock_zero_value", `UPDATE inventory_balances SET inventory_value_rial = 5 WHERE product_id = $1`, f.trackedID)
	f.exec(`UPDATE inventory_balances SET on_hand = 5, reserved = 5, inventory_value_rial = 500 WHERE product_id = $1`, f.trackedID)

	order := f.newOrder()
	sale := `INSERT INTO inventory_movements (product_id, movement_type, quantity_delta, value_delta_rial,
		on_hand_after, value_after_rial, order_id) VALUES ($1, 'SALE', -2, -200, 3, 300, $2)`
	f.exec(sale, f.trackedID, order)
	f.fails(f.app, codeUnique, "inventory_movements_order_once", sale, f.trackedID, order)

	purchase := f.id(`INSERT INTO purchase_entries (product_id, quantity, unit_cost_rial, total_cost_rial, purchased_on,
		idempotency_key, created_by) VALUES ($1, 2, 100, 200, current_date, gen_random_uuid(), $2) RETURNING id`, f.trackedID, f.userID)
	void := `INSERT INTO inventory_movements (product_id, movement_type, quantity_delta, value_delta_rial,
		on_hand_after, value_after_rial, purchase_entry_id) VALUES ($1, 'PURCHASE_VOID', -2, -200, 1, 100, $2)`
	f.exec(void, f.trackedID, purchase)
	f.fails(f.app, codeUnique, "inventory_movements_purchase_once", void, f.trackedID, purchase)
	f.fails(f.app, codeCheck, "", `INSERT INTO purchase_entries (product_id, quantity, unit_cost_rial, total_cost_rial,
		purchased_on, idempotency_key, created_by) VALUES ($1, 2, 100, 999, current_date, gen_random_uuid(), $2)`, f.trackedID, f.userID)
}

func TestProductConstraints(t *testing.T) {
	f := newFixture(t)
	f.fails(f.app, codeCheck, "", `INSERT INTO products (category_id, name, price_rial, inventory_mode) VALUES ($1, 'Odd', 12345, 'TRACKED')`, f.categoryID)
	f.fails(f.app, codeCheck, "products_threshold_tracked_only", `INSERT INTO products (category_id, name, price_rial, inventory_mode, low_stock_threshold)
		VALUES ($1, 'Latte', 10000, 'UNTRACKED', 2)`, f.categoryID)
	f.fails(f.app, codeCheck, "products_estimate_untracked_only", `INSERT INTO products (category_id, name, price_rial, inventory_mode, estimated_unit_cost_rial)
		VALUES ($1, 'Cake', 10000, 'TRACKED', 2)`, f.categoryID)
	f.fails(f.app, codeUnique, "", `INSERT INTO products (category_id, name, price_rial, inventory_mode) VALUES ($1, 'Soda', 10000, 'TRACKED')`, f.categoryID)
}

// R-F1-6 / INV-10 / INV-11b: payment amounts and the generated payment_status.
func TestOrderMoneyConstraints(t *testing.T) {
	f := newFixture(t)
	status := func(id int64) (s, p string) {
		t.Helper()
		if err := f.app.QueryRow(f.ctx, `SELECT status, payment_status FROM orders WHERE id = $1`, id).Scan(&s, &p); err != nil {
			t.Fatal(err)
		}
		return
	}

	order := f.newOrder()
	if s, p := status(order); s != "PENDING" || p != "UNPAID" {
		t.Fatalf("new order = %s/%s", s, p)
	}
	f.fails(f.app, codeCheck, "orders_no_money_when_pending", `UPDATE orders SET paid_rial = 1000 WHERE id = $1`, order)

	f.accept(order)
	f.exec(`UPDATE orders SET paid_rial = 40000 WHERE id = $1`, order)
	if _, p := status(order); p != "UNPAID" {
		t.Errorf("partially paid = %s, want UNPAID", p)
	}
	f.fails(f.app, codeCheck, "orders_paid_range", `UPDATE orders SET paid_rial = 100001 WHERE id = $1`, order)
	f.fails(f.app, codeCheck, "orders_no_money_when_cancelled",
		`UPDATE orders SET status = 'CANCELLED', cancelled_at = now(), cancel_reason = 'STAFF' WHERE id = $1`, order)
	f.fails(f.app, codeCheck, "orders_refunds_full", `UPDATE orders SET refunded_rial = 10000 WHERE id = $1`, order)

	f.exec(`UPDATE orders SET paid_rial = 100000 WHERE id = $1`, order)
	if _, p := status(order); p != "PAID" {
		t.Errorf("fully paid = %s, want PAID", p)
	}
	f.exec(`UPDATE orders SET refunded_rial = 100000, status = 'CANCELLED', cancelled_at = now(), cancel_reason = 'STAFF' WHERE id = $1`, order)
	if s, p := status(order); s != "CANCELLED" || p != "REFUNDED" {
		t.Errorf("refunded+cancelled = %s/%s", s, p)
	}
	f.fails(f.app, "428C9", "", `UPDATE orders SET payment_status = 'PAID' WHERE id = $1`, order) // generated column
}

// R-F2-4: acceptance deadline.
func TestOrderDeadlineConstraints(t *testing.T) {
	f := newFixture(t)
	order := f.newOrder()
	f.fails(f.app, codeCheck, "orders_accepted_before_deadline",
		`UPDATE orders SET status = 'PREPARING', accepted_at = expires_at + interval '1 second' WHERE id = $1`, order)

	f.accept(order)
	f.fails(f.app, codeCheck, "orders_expired_never_accepted",
		`UPDATE orders SET status = 'CANCELLED', cancelled_at = now(), cancel_reason = 'EXPIRED' WHERE id = $1`, order)

	pending := f.newOrder()
	f.exec(`UPDATE orders SET status = 'CANCELLED', cancelled_at = now(), cancel_reason = 'EXPIRED' WHERE id = $1`, pending)
	f.fails(f.app, codeCheck, "orders_expires_after_created", `INSERT INTO orders (business_date, display_number, tracking_token_hash,
		idempotency_key, request_hash, client_key, total_rial, expires_at)
		VALUES (current_date, 999, $1, gen_random_uuid(), $2, $3, 100, now() - interval '1 minute')`, random32(), random32(), random32())
}

// INV-9 + orders_guard.
func TestOrderGuard(t *testing.T) {
	f := newFixture(t)
	order := f.newOrder()
	f.fails(f.app, codeTransition, "illegal status transition", `UPDATE orders SET status = 'READY', accepted_at = now(), ready_at = now() WHERE id = $1`, order)
	f.fails(f.app, codeImmutable, "total_rial", `UPDATE orders SET total_rial = 1 WHERE id = $1`, order)
	f.fails(f.app, codeImmutable, "tracking_token_hash", `UPDATE orders SET tracking_token_hash = $2 WHERE id = $1`, order, random32())
	f.fails(f.app, codeImmutable, "expires_at", `UPDATE orders SET expires_at = expires_at + interval '1 hour' WHERE id = $1`, order)

	f.accept(order)
	f.fails(f.app, codeImmutable, "accepted_at", `UPDATE orders SET accepted_at = now() - interval '1 minute' WHERE id = $1`, order)
	f.exec(`UPDATE orders SET status = 'READY', ready_at = now() WHERE id = $1`, order)
	f.exec(`UPDATE orders SET status = 'COMPLETED', completed_at = now(), completed_by = $2 WHERE id = $1`, order, f.userID)
	f.fails(f.app, codeTransition, "", `UPDATE orders SET status = 'CANCELLED', cancelled_at = now(), cancel_reason = 'STAFF' WHERE id = $1`, order)
	f.fails(f.app, codeImmutable, "only accepts payment changes", `UPDATE orders SET cancel_note = 'late' WHERE id = $1`, order)
	f.exec(`UPDATE orders SET paid_rial = 100000 WHERE id = $1`, order) // paying after completion is allowed

	cancelled := f.newOrder()
	f.exec(`UPDATE orders SET status = 'CANCELLED', cancelled_at = now(), cancel_reason = 'CUSTOMER' WHERE id = $1`, cancelled)
	f.fails(f.app, codeImmutable, "is final", `UPDATE orders SET cancelled_by = $2 WHERE id = $1`, cancelled, f.userID)

	var before, after string
	_ = f.app.QueryRow(f.ctx, `SELECT updated_at::text FROM orders WHERE id = $1`, order).Scan(&before)
	f.exec(`UPDATE orders SET refunded_rial = 100000 WHERE id = $1`, order)
	_ = f.app.QueryRow(f.ctx, `SELECT updated_at::text FROM orders WHERE id = $1`, order).Scan(&after)
	if before == after {
		t.Error("updated_at not maintained by trigger")
	}
}

func TestOrderItemsGuard(t *testing.T) {
	f := newFixture(t)
	order := f.newOrder()
	var item int64
	_ = f.app.QueryRow(f.ctx, `SELECT id FROM order_items WHERE order_id = $1`, order).Scan(&item)

	f.fails(f.app, codeImmutable, "snapshot", `UPDATE order_items SET unit_price_rial = 40000, line_total_rial = 80000 WHERE id = $1`, item)
	f.fails(f.app, codeImmutable, "snapshot", `UPDATE order_items SET product_name = 'Other' WHERE id = $1`, item)
	f.exec(`UPDATE order_items SET cogs_rial = 700 WHERE id = $1`, item)
	f.fails(f.app, codeImmutable, "cogs_rial", `UPDATE order_items SET cogs_rial = 800 WHERE id = $1`, item)
	f.exec(`UPDATE order_items SET wasted_quantity = 1 WHERE id = $1`, item)
	f.fails(f.app, codeImmutable, "wasted_quantity", `UPDATE order_items SET wasted_quantity = 2 WHERE id = $1`, item)
	f.fails(f.app, codeCheck, "order_items_waste_range", `INSERT INTO order_items (order_id, product_id, product_name, inventory_mode,
		unit_price_rial, quantity, line_total_rial, wasted_quantity) VALUES ($1, $2, 'Espresso', 'UNTRACKED', 90000, 1, 90000, 2)`, order, f.untrackedID)
	f.fails(f.app, codeCheck, "order_items_estimate_untracked_only", `INSERT INTO order_items (order_id, product_id, product_name, inventory_mode,
		unit_price_rial, quantity, line_total_rial, estimated_cost_rial) VALUES ($1, $2, 'Espresso', 'TRACKED', 90000, 1, 90000, 5)`, order, f.untrackedID)
	f.fails(f.app, codeCheck, "order_items_line_total", `INSERT INTO order_items (order_id, product_id, product_name, inventory_mode,
		unit_price_rial, quantity, line_total_rial) VALUES ($1, $2, 'Espresso', 'UNTRACKED', 90000, 2, 90000)`, order, f.untrackedID)
}

// F1/F9/F10: refund links, refund rows cannot be voided, voids happen once.
func TestPaymentRowRules(t *testing.T) {
	f := newFixture(t)
	order := f.newOrder()
	f.accept(order)
	pay := f.id(`INSERT INTO payments (order_id, kind, method, amount_rial, idempotency_key, created_by)
		VALUES ($1, 'PAYMENT', 'CASH', 100000, gen_random_uuid(), $2) RETURNING id`, order, f.userID)
	f.fails(f.app, codeCheck, "payments_idempotency_key", `INSERT INTO payments (order_id, kind, method, amount_rial, created_by)
		VALUES ($1, 'PAYMENT', 'CASH', 1, $2)`, order, f.userID)
	f.fails(f.app, codeCheck, "payments_refund_link", `INSERT INTO payments (order_id, kind, method, amount_rial, created_by)
		VALUES ($1, 'REFUND', 'CASH', 1, $2)`, order, f.userID)

	refund := f.id(`INSERT INTO payments (order_id, kind, method, amount_rial, refund_of_payment_id, created_by)
		VALUES ($1, 'REFUND', 'CASH', 100000, $2, $3) RETURNING id`, order, pay, f.userID)
	f.fails(f.app, codeUnique, "", `INSERT INTO payments (order_id, kind, method, amount_rial, refund_of_payment_id, created_by)
		VALUES ($1, 'REFUND', 'CASH', 100000, $2, $3)`, order, pay, f.userID)
	f.fails(f.app, codeCheck, "payments_refund_not_voidable",
		`UPDATE payments SET voided_at = now(), voided_by = $2, void_reason = 'oops' WHERE id = $1`, refund, f.userID)

	f.fails(f.app, codeImmutable, "only voided_at", `UPDATE payments SET amount_rial = 5 WHERE id = $1`, pay)
	f.exec(`UPDATE payments SET voided_at = now(), voided_by = $2, void_reason = 'wrong method' WHERE id = $1`, pay, f.userID)
	f.fails(f.app, codeImmutable, "already voided", `UPDATE payments SET voided_at = now(), voided_by = $2, void_reason = 'again' WHERE id = $1`, pay, f.userID)
}

func TestOtherTableConstraints(t *testing.T) {
	f := newFixture(t)
	f.fails(f.app, codeCheck, "", `INSERT INTO users (username, display_name, password_hash, role) VALUES ('Bad Name', 'x', 'x', 'ADMIN')`)
	f.fails(f.app, codeCheck, "", `INSERT INTO users (username, display_name, password_hash, role) VALUES ('owner2', 'x', 'x', 'OWNER')`)
	f.fails(f.app, codeCheck, "", `INSERT INTO sessions (token_hash, user_id, csrf_token, expires_at) VALUES ('\x00', $1, 'c', now())`, f.userID)
	f.fails(f.app, codeCheck, "", `INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth) VALUES ($1, 'http://fcm.googleapis.com/x', 'k', 'a')`, f.userID)
	f.fails(f.app, codeCheck, "audit_logs_actor", `INSERT INTO audit_logs (actor_kind, action, entity_type, entity_id) VALUES ('USER', 'x', 'order', '1')`)
	f.fails(f.app, codeCheck, "audit_logs_actor", `INSERT INTO audit_logs (actor_user_id, actor_kind, action, entity_type, entity_id) VALUES ($1, 'SYSTEM', 'x', 'order', '1')`, f.userID)
	f.fails(f.app, codeCheck, "notification_events_lease", `INSERT INTO notification_events (event_type, dedup_key, payload, status)
		VALUES ('TEST', 'k', '{}', 'SENDING')`)
	f.fails(f.app, codeCheck, "notification_events_sent_at", `INSERT INTO notification_events (event_type, dedup_key, payload, status)
		VALUES ('TEST', 'k', '{}', 'SENT')`)
	f.fails(f.app, codeNotNull, "", `INSERT INTO categories (name) VALUES (NULL)`)

	// INV-14: one display number per business date; INV-13: one order per idempotency key.
	a := f.newOrder()
	var num int
	var key string
	_ = f.app.QueryRow(f.ctx, `SELECT display_number, idempotency_key::text FROM orders WHERE id = $1`, a).Scan(&num, &key)
	insert := `INSERT INTO orders (business_date, display_number, tracking_token_hash, idempotency_key, request_hash,
		client_key, total_rial, expires_at) VALUES (current_date, $1, $2, $3::uuid, $4, $5, 100, now() + interval '20 minutes')`
	f.fails(f.app, codeUnique, "orders_business_date_display_number_key", insert, num, random32(), "00000000-0000-4000-8000-000000000001", random32(), random32())
	f.fails(f.app, codeUnique, "orders_idempotency_key_key", insert, num+1000, random32(), key, random32(), random32())
}

// AC-21 / INV-12: append-only and delete protection, for the runtime role and the owner.
func TestAppendOnlyProtection(t *testing.T) {
	f := newFixture(t)
	// Every protected table gets at least one row: row-level delete triggers only fire on rows.
	order := f.newOrder()
	f.accept(order)
	f.exec(`INSERT INTO payments (order_id, kind, method, amount_rial, idempotency_key, created_by)
		VALUES ($1, 'PAYMENT', 'CASH', 1000, gen_random_uuid(), $2)`, order, f.userID)
	f.exec(`INSERT INTO expenses (category, amount_rial, expense_date, description, idempotency_key, created_by)
		VALUES ('RENT', 1000, current_date, 'rent', gen_random_uuid(), $1)`, f.userID)
	f.exec(`INSERT INTO purchase_entries (product_id, quantity, unit_cost_rial, total_cost_rial, purchased_on, idempotency_key, created_by)
		VALUES ($1, 1, 100, 100, current_date, gen_random_uuid(), $2)`, f.trackedID, f.userID)
	f.exec(`INSERT INTO audit_logs (actor_kind, action, entity_type, entity_id) VALUES ('SYSTEM', 'test', 'order', '1')`)
	f.exec(`UPDATE inventory_balances SET on_hand = 1, inventory_value_rial = 100 WHERE product_id = $1`, f.trackedID)
	f.exec(`INSERT INTO inventory_movements (product_id, movement_type, reason_code, quantity_delta, value_delta_rial,
		on_hand_after, value_after_rial, note) VALUES ($1, 'ADJUSTMENT_IN', 'OPENING_BALANCE', 1, 100, 1, 100, 'open')`, f.trackedID)

	protected := []string{"orders", "order_items", "payments", "expenses", "purchase_entries",
		"inventory_movements", "audit_logs", "products", "inventory_balances"}
	for _, table := range protected {
		f.fails(f.app, codePrivilege, "", "DELETE FROM "+table)
		f.fails(f.owner, codeNoDelete, "", "DELETE FROM "+table)
		f.fails(f.app, codePrivilege, "", "TRUNCATE "+table)
		f.fails(f.owner, codeNoDelete, "", "TRUNCATE "+table+" CASCADE")
	}
	for _, table := range []string{"inventory_movements", "audit_logs"} {
		f.fails(f.app, codePrivilege, "", "UPDATE "+table+" SET created_at = now()")
		f.fails(f.owner, codeImmutable, "append-only", "UPDATE "+table+" SET created_at = now()")
	}
	f.fails(f.app, codePrivilege, "", "ALTER TABLE orders DISABLE TRIGGER orders_guard")
	f.fails(f.app, codePrivilege, "", "DROP TRIGGER orders_guard ON orders")
	f.fails(f.app, codePrivilege, "", "CREATE TABLE sneaky (id int)")

	for _, table := range protected {
		var n int
		if err := f.owner.QueryRow(f.ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n == 0 {
			t.Errorf("%s lost its rows: n=%d err=%v", table, n, err)
		}
	}
}

// Only sessions, push_subscriptions and notification_events are deletable at runtime.
func TestRuntimeDeleteAllowedWhereDocumented(t *testing.T) {
	f := newFixture(t)
	f.exec(`INSERT INTO sessions (token_hash, user_id, csrf_token, expires_at) VALUES ($1, $2, 'c', now() + interval '1 hour')`, random32(), f.userID)
	sub := f.id(`INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth) VALUES ($1, 'https://fcm.googleapis.com/x', 'k', 'a') RETURNING id`, f.userID)
	f.exec(`INSERT INTO notification_events (event_type, dedup_key, subscription_id, payload) VALUES ('TEST', 'k', $1, '{}')`, sub)

	f.exec(`DELETE FROM sessions`)
	f.exec(`DELETE FROM push_subscriptions WHERE id = $1`, sub)
	var subID *int64
	if err := f.app.QueryRow(f.ctx, `SELECT subscription_id FROM notification_events`).Scan(&subID); err != nil || subID != nil {
		t.Errorf("subscription delete should SET NULL on events: %v %v", subID, err)
	}
	f.exec(`DELETE FROM notification_events`)
	f.fails(f.app, codePrivilege, "", `DELETE FROM users`)
	f.fails(f.app, codePrivilege, "", `DELETE FROM categories`)
}
