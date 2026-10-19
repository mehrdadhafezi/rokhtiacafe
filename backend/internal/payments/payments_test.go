package payments_test

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/audit"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/check"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/config"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/database"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/inventory"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/migrate"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/orders"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/payments"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/testdb"
)

const espressoPrice = 90_000

type fx struct {
	t              *testing.T
	ctx            context.Context
	pool           *pgxpool.Pool
	orders         *orders.Service
	pay            *payments.Service
	staff          audit.Actor
	espresso, soda int64
	n              byte
}

func setup(t *testing.T) *fx {
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
	pool, err := database.Open(ctx, db.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	tehran, _ := time.LoadLocation("Asia/Tehran")
	hours, _ := config.ParseOrderingHours("00:00-24:00")
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	f := &fx{t: t, ctx: ctx, pool: pool, pay: &payments.Service{Pool: pool},
		orders: orders.New(pool, orders.Config{PendingTTL: 20 * time.Minute, MaxPending: 1000, MaxPendingPerClient: 1000,
			Hours: hours, Location: tehran, Secret: secret})}
	var uid int64
	f.must(pool.QueryRow(ctx, `INSERT INTO users (username, display_name, password_hash, role) VALUES ('admin','A','x','ADMIN') RETURNING id`).Scan(&uid))
	f.staff = audit.UserActor(uid, netip.MustParseAddr("127.0.0.1"))
	f.must(pool.QueryRow(ctx, `INSERT INTO categories (name) VALUES ('c') RETURNING id`).Scan(new(int64)))
	f.must(pool.QueryRow(ctx, `INSERT INTO products (category_id, name, price_rial, inventory_mode) VALUES (1, 'Espresso', $1, 'UNTRACKED') RETURNING id`, espressoPrice).Scan(&f.espresso))
	f.must(pool.QueryRow(ctx, `INSERT INTO products (category_id, name, price_rial, inventory_mode) VALUES (1, 'Soda', 50000, 'TRACKED') RETURNING id`).Scan(&f.soda))
	_, err = pool.Exec(ctx, `INSERT INTO inventory_balances (product_id) VALUES ($1)`, f.soda)
	f.must(err)
	inv := &inventory.Service{Pool: pool, Location: tehran}
	_, _, err = inv.Purchase(ctx, f.staff, newKey(), inventory.PurchaseInput{ProductID: f.soda, Quantity: 100, UnitCostRial: 1000,
		PurchasedOn: time.Now().In(tehran).Format(time.DateOnly)})
	f.must(err)
	return f
}

func (f *fx) must(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}

func newKey() pgtype.UUID {
	var k pgtype.UUID
	_, _ = rand.Read(k.Bytes[:])
	k.Bytes[6] = (k.Bytes[6] & 0x0f) | 0x40
	k.Bytes[8] = (k.Bytes[8] & 0x3f) | 0x80
	k.Valid = true
	return k
}

// order creates an order for qty espressos (total qty × 90 000) and moves it to status.
func (f *fx) order(qty int32, status string, extra ...orders.Item) store.Order {
	f.t.Helper()
	f.n++
	items := append([]orders.Item{{ProductID: f.espresso, Quantity: qty, ExpectedUnitPriceRial: espressoPrice}}, extra...)
	d, _, err := f.orders.Create(f.ctx, orders.CreateInput{Key: newKey(), ClientIP: netip.AddrFrom4([4]byte{10, 0, 0, f.n}), Items: items})
	f.must(err)
	id := d.Order.ID
	steps := map[string][]func(context.Context, audit.Actor, int64) (orders.Detail, error){
		"PENDING":   nil,
		"PREPARING": {f.orders.Accept},
		"READY":     {f.orders.Accept, f.orders.Ready},
		"COMPLETED": {f.orders.Accept, f.orders.Ready, f.orders.Complete},
	}
	for _, step := range steps[status] {
		_, err := step(f.ctx, f.staff, id)
		f.must(err)
	}
	return f.get(id)
}

func (f *fx) get(id int64) store.Order {
	f.t.Helper()
	d, err := f.orders.Get(f.ctx, id)
	f.must(err)
	return d.Order
}

func (f *fx) payment(orderID int64, method string, amount int64) (store.Payment, store.Order, error) {
	p, o, _, err := f.pay.Record(f.ctx, f.staff, orderID, newKey(), payments.RecordInput{Method: method, AmountRial: amount})
	return p, o, err
}

func isState(err error) bool {
	var s payments.StateError
	return errors.As(err, &s)
}

func (f *fx) invariantsHold() {
	f.t.Helper()
	v, err := check.Run(f.ctx, store.New(f.pool))
	f.must(err)
	if len(v) != 0 {
		f.t.Fatalf("invariant violations: %v", v)
	}
}

// AC-23
func TestSplitPaymentAndNoOverpayment(t *testing.T) {
	f := setup(t)
	o := f.order(2, "PREPARING") // 180 000
	_, o, err := f.payment(o.ID, payments.MethodCash, 100_000)
	f.must(err)
	if o.PaidRial != 100_000 || o.PaymentStatus.String != "UNPAID" {
		t.Errorf("after cash: %d %s", o.PaidRial, o.PaymentStatus.String)
	}
	if _, _, err := f.payment(o.ID, payments.MethodCardTerminal, 90_000); !isState(err) {
		t.Errorf("overpayment accepted: %v", err)
	}
	_, o, err = f.payment(o.ID, payments.MethodCardTerminal, 80_000)
	f.must(err)
	if o.PaidRial != 180_000 || o.PaymentStatus.String != "PAID" {
		t.Errorf("after card: %d %s", o.PaidRial, o.PaymentStatus.String)
	}
	if _, _, err := f.payment(o.ID, payments.MethodCash, 10); !isState(err) {
		t.Errorf("payment on a PAID order: %v", err)
	}
	f.invariantsHold()
}

// AC-24 and input validation
func TestPaymentPreconditions(t *testing.T) {
	f := setup(t)
	pending := f.order(1, "PENDING")
	if _, _, err := f.payment(pending.ID, payments.MethodCash, 10); !isState(err) {
		t.Errorf("payment on PENDING: %v", err)
	}
	cancelled := f.order(1, "PREPARING")
	_, err := f.orders.Cancel(f.ctx, f.staff, cancelled.ID, orders.CancelInput{})
	f.must(err)
	if _, _, err := f.payment(cancelled.ID, payments.MethodCash, 10); !isState(err) {
		t.Errorf("payment on CANCELLED: %v", err)
	}
	if _, _, err := f.payment(999999, payments.MethodCash, 10); !errors.Is(err, payments.ErrNotFound) {
		t.Errorf("unknown order: %v", err)
	}
	var bad payments.ValidationError
	for _, in := range []payments.RecordInput{{Method: "BITCOIN", AmountRial: 10}, {Method: payments.MethodCash, AmountRial: 0}, {Method: payments.MethodCash, AmountRial: -5}} {
		if _, _, _, err := f.pay.Record(f.ctx, f.staff, cancelled.ID, newKey(), in); !errors.As(err, &bad) {
			t.Errorf("%+v accepted: %v", in, err)
		}
	}
	// Payments after completion are allowed (pay on pickup).
	done := f.order(1, "COMPLETED")
	if _, o, err := f.payment(done.ID, payments.MethodCardTerminal, espressoPrice); err != nil || o.PaymentStatus.String != "PAID" {
		t.Errorf("payment after completion: %v", err)
	}
	f.invariantsHold()
}

// AC-27
func TestPaymentIdempotency(t *testing.T) {
	f := setup(t)
	o := f.order(2, "PREPARING")
	key := newKey()
	in := payments.RecordInput{Method: payments.MethodCash, AmountRial: 50_000, Note: "table 3"}
	p1, _, replay, err := f.pay.Record(f.ctx, f.staff, o.ID, key, in)
	f.must(err)
	p2, o2, replay2, err := f.pay.Record(f.ctx, f.staff, o.ID, key, in)
	f.must(err)
	if replay || !replay2 || p1.ID != p2.ID || o2.PaidRial != 50_000 {
		t.Errorf("replay: %v %v ids %d/%d paid %d", replay, replay2, p1.ID, p2.ID, o2.PaidRial)
	}
	in.AmountRial = 60_000
	if _, _, _, err := f.pay.Record(f.ctx, f.staff, o.ID, key, in); !errors.Is(err, payments.ErrIdempotencyReused) {
		t.Errorf("different amount: %v", err)
	}
	other := f.order(1, "PREPARING")
	if _, _, _, err := f.pay.Record(f.ctx, f.staff, other.ID, key, payments.RecordInput{Method: payments.MethodCash, AmountRial: 50_000, Note: "table 3"}); !errors.Is(err, payments.ErrIdempotencyReused) {
		t.Errorf("key reused on another order: %v", err)
	}

	// Concurrent duplicates: one payment row.
	key = newKey()
	var wg sync.WaitGroup
	errs := make([]error, 10)
	start := make(chan struct{})
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, _, errs[i] = f.pay.Record(f.ctx, f.staff, o.ID, key, payments.RecordInput{Method: payments.MethodCardTerminal, AmountRial: 10_000})
		}()
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		f.must(err)
	}
	if got := f.get(o.ID); got.PaidRial != 60_000 {
		t.Errorf("paid after concurrent duplicates = %d, want 60000", got.PaidRial)
	}
	f.invariantsHold()
}

// R-F1-1: concurrent payments never exceed the total.
func TestConcurrentPaymentsNeverOverpay(t *testing.T) {
	f := setup(t)
	o := f.order(2, "READY") // 180 000
	var wg sync.WaitGroup
	errs := make([]error, 10)
	start := make(chan struct{})
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, errs[i] = f.payment(o.ID, payments.MethodCash, 50_000)
		}()
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case isState(err):
		default:
			t.Errorf("unexpected: %v", err)
		}
	}
	if got := f.get(o.ID); ok != 3 || got.PaidRial != 150_000 {
		t.Errorf("ok=%d paid=%d, want 3 and 150000", ok, got.PaidRial)
	}
	_, o, err := f.payment(o.ID, payments.MethodCardTerminal, 30_000)
	f.must(err)
	if o.PaymentStatus.String != "PAID" {
		t.Errorf("status = %s", o.PaymentStatus.String)
	}
	f.invariantsHold()
}

// AC-25
func TestFullRefundOfCompletedOrder(t *testing.T) {
	f := setup(t)
	partial := f.order(2, "COMPLETED")
	_, _, err := f.payment(partial.ID, payments.MethodCash, 90_000)
	f.must(err)
	if _, _, err := f.pay.Refund(f.ctx, f.staff, partial.ID, ""); !isState(err) {
		t.Errorf("refund of a partially paid order: %v", err)
	}
	open := f.order(1, "READY")
	_, _, err = f.payment(open.ID, payments.MethodCash, espressoPrice)
	f.must(err)
	if _, _, err := f.pay.Refund(f.ctx, f.staff, open.ID, ""); !isState(err) {
		t.Errorf("refund of an open order: %v", err)
	}

	o := f.order(2, "COMPLETED")
	cash, _, err := f.payment(o.ID, payments.MethodCash, 100_000)
	f.must(err)
	card, _, err := f.payment(o.ID, payments.MethodCardTerminal, 80_000)
	f.must(err)
	got, refunds, err := f.pay.Refund(f.ctx, f.staff, o.ID, "cold coffee")
	f.must(err)
	if got.PaymentStatus.String != "REFUNDED" || got.RefundedRial != 180_000 || len(refunds) != 2 {
		t.Fatalf("after refund: %+v (%d refunds)", got, len(refunds))
	}
	mirror := map[int64]store.Payment{cash.ID: cash, card.ID: card}
	for _, r := range refunds {
		p := mirror[r.RefundOfPaymentID.Int64]
		if r.Kind != "REFUND" || r.Method != p.Method || r.AmountRial != p.AmountRial {
			t.Errorf("refund %+v does not mirror %+v", r, p)
		}
	}
	again, more, err := f.pay.Refund(f.ctx, f.staff, o.ID, "")
	f.must(err)
	if len(more) != 0 || again.RefundedRial != 180_000 {
		t.Errorf("repeat refund created rows: %d", len(more))
	}
	if _, _, err := f.payment(o.ID, payments.MethodCash, 10); !isState(err) {
		t.Errorf("payment after refund: %v", err)
	}
	if _, _, err := f.pay.Void(f.ctx, f.staff, cash.ID, "oops"); !isState(err) {
		t.Errorf("void after refund: %v", err)
	}
	if _, _, err := f.pay.Void(f.ctx, f.staff, refunds[0].ID, "oops"); !isState(err) {
		t.Errorf("void of a refund row: %v", err)
	}
	if got := f.get(o.ID); got.Status != "COMPLETED" {
		t.Errorf("refund changed order status to %s", got.Status)
	}
	f.invariantsHold()
}

// AC-26
func TestVoidMistakenPayment(t *testing.T) {
	f := setup(t)
	o := f.order(1, "PREPARING")
	wrong, _, err := f.payment(o.ID, payments.MethodCash, 50_000)
	f.must(err)
	var bad payments.ValidationError
	if _, _, err := f.pay.Void(f.ctx, f.staff, wrong.ID, "  "); !errors.As(err, &bad) {
		t.Errorf("blank reason: %v", err)
	}
	v, got, err := f.pay.Void(f.ctx, f.staff, wrong.ID, "should have been card")
	f.must(err)
	if !v.VoidedAt.Valid || v.VoidReason.String != "should have been card" || v.VoidedBy.Int64 != f.staff.UserID || got.PaidRial != 0 {
		t.Errorf("void: %+v paid %d", v, got.PaidRial)
	}
	again, got2, err := f.pay.Void(f.ctx, f.staff, wrong.ID, "again")
	f.must(err)
	if again.VoidReason.String != "should have been card" || got2.PaidRial != 0 {
		t.Error("repeat void changed something")
	}
	_, got, err = f.payment(o.ID, payments.MethodCardTerminal, espressoPrice)
	f.must(err)
	if got.PaymentStatus.String != "PAID" {
		t.Errorf("status = %s", got.PaymentStatus.String)
	}
	if _, _, err := f.pay.Void(f.ctx, f.staff, 999999, "x"); !errors.Is(err, payments.ErrNotFound) {
		t.Errorf("unknown payment: %v", err)
	}
	f.invariantsHold()
}

// R-F1-5 and WORKFLOWS §1.5: cancelling an order that holds a prepayment refunds it.
func TestCancelRefundsPrepayment(t *testing.T) {
	f := setup(t)
	o := f.order(2, "READY", orders.Item{ProductID: f.soda, Quantity: 3, ExpectedUnitPriceRial: 50000}) // 330 000
	cash, _, err := f.payment(o.ID, payments.MethodCash, 165_000)
	f.must(err)

	if _, err := f.orders.Cancel(f.ctx, f.staff, o.ID, orders.CancelInput{}); !errors.Is(err, orders.ErrPaymentState) {
		t.Fatalf("cancel without refund permission: %v", err)
	}
	if got := f.get(o.ID); got.Status != "READY" || got.RefundedRial != 0 {
		t.Fatalf("failed cancel changed the order: %+v", got)
	}

	d, err := f.orders.Cancel(f.ctx, f.staff, o.ID, orders.CancelInput{AllowRefund: true, Note: "customer left",
		Waste: []orders.WasteLine{{ProductID: f.soda, Quantity: 1}}})
	f.must(err)
	if d.Order.Status != "CANCELLED" || d.Order.PaymentStatus.String != "REFUNDED" || d.Order.RefundedRial != 165_000 {
		t.Fatalf("cancelled order: %+v", d.Order)
	}
	var refunds []store.Payment
	for _, p := range d.Payments {
		if p.Kind == "REFUND" {
			refunds = append(refunds, p)
		}
	}
	if len(refunds) != 1 || refunds[0].RefundOfPaymentID.Int64 != cash.ID || refunds[0].Method != "CASH" || refunds[0].AmountRial != 165_000 {
		t.Errorf("refunds = %+v", refunds)
	}
	var reserved, onHand int32
	f.must(f.pool.QueryRow(f.ctx, `SELECT reserved, on_hand FROM inventory_balances WHERE product_id = $1`, f.soda).Scan(&reserved, &onHand))
	if reserved != 0 || onHand != 99 {
		t.Errorf("stock after cancel with waste: reserved=%d on_hand=%d", reserved, onHand)
	}
	var details string
	f.must(f.pool.QueryRow(f.ctx, `SELECT details::text FROM audit_logs WHERE action = 'order.cancel' ORDER BY id DESC LIMIT 1`).Scan(&details))
	if want := `"refunded": {"CASH": 165000}`; !strings.Contains(details, want) {
		t.Errorf("cancel audit %s lacks %s", details, want)
	}
	f.invariantsHold()
}

// R-F1-2: cancel racing a payment never leaves money on a cancelled order.
func TestCancelRacingPayment(t *testing.T) {
	f := setup(t)
	for round := 0; round < 20; round++ {
		o := f.order(1, "PREPARING")
		var wg sync.WaitGroup
		var payErr, cancelErr error
		start := make(chan struct{})
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, _, payErr = f.payment(o.ID, payments.MethodCash, 40_000) }()
		go func() {
			defer wg.Done()
			<-start
			_, cancelErr = f.orders.Cancel(f.ctx, f.staff, o.ID, orders.CancelInput{AllowRefund: true})
		}()
		close(start)
		wg.Wait()
		f.must(cancelErr)
		got := f.get(o.ID)
		if got.Status != "CANCELLED" || got.PaidRial != got.RefundedRial {
			t.Fatalf("round %d: %s paid=%d refunded=%d", round, got.Status, got.PaidRial, got.RefundedRial)
		}
		if payErr == nil && got.RefundedRial != 40_000 {
			t.Fatalf("round %d: payment landed but was not refunded", round)
		}
		if payErr != nil && (!isState(payErr) || got.PaidRial != 0) {
			t.Fatalf("round %d: payment error %v, paid %d", round, payErr, got.PaidRial)
		}
	}
	f.invariantsHold()
}

// R-F1-4: refund racing a void on a fully paid completed order.
func TestRefundRacingVoid(t *testing.T) {
	f := setup(t)
	for round := 0; round < 15; round++ {
		o := f.order(2, "COMPLETED")
		first, _, err := f.payment(o.ID, payments.MethodCash, 100_000)
		f.must(err)
		_, _, err = f.payment(o.ID, payments.MethodCardTerminal, 80_000)
		f.must(err)
		var wg sync.WaitGroup
		var refundErr, voidErr error
		start := make(chan struct{})
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, _, refundErr = f.pay.Refund(f.ctx, f.staff, o.ID, "") }()
		go func() { defer wg.Done(); <-start; _, _, voidErr = f.pay.Void(f.ctx, f.staff, first.ID, "wrong") }()
		close(start)
		wg.Wait()
		got := f.get(o.ID)
		switch {
		case refundErr == nil && voidErr != nil:
			if got.RefundedRial != 180_000 || got.PaidRial != 180_000 || !isState(voidErr) {
				t.Fatalf("round %d refund won: %+v void=%v", round, got, voidErr)
			}
		case voidErr == nil && refundErr != nil:
			if got.RefundedRial != 0 || got.PaidRial != 80_000 || !isState(refundErr) {
				t.Fatalf("round %d void won: %+v refund=%v", round, got, refundErr)
			}
		default:
			t.Fatalf("round %d: refund=%v void=%v", round, refundErr, voidErr)
		}
	}
	f.invariantsHold() // includes: no REFUND mirrors a voided payment
}
