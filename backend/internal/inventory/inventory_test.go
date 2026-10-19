package inventory_test

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	mrand "math/rand/v2"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/audit"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/check"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/database"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/inventory"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/migrate"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/testdb"
)

type fx struct {
	t                  *testing.T
	ctx                context.Context
	pool               *pgxpool.Pool
	svc                *inventory.Service
	actor              audit.Actor
	tracked, untracked int64
	today              string
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
	f := &fx{t: t, ctx: ctx, pool: pool, svc: &inventory.Service{Pool: pool, Location: tehran},
		today: time.Now().In(tehran).Format("2006-01-02")}

	var uid int64
	f.must(pool.QueryRow(ctx, `INSERT INTO users (username, display_name, password_hash, role) VALUES ('admin','A','x','ADMIN') RETURNING id`).Scan(&uid))
	f.actor = audit.UserActor(uid, netip.MustParseAddr("127.0.0.1"))
	f.must(pool.QueryRow(ctx, `INSERT INTO categories (name) VALUES ('c') RETURNING id`).Scan(new(int64)))
	f.must(pool.QueryRow(ctx, `INSERT INTO products (category_id, name, price_rial, inventory_mode, low_stock_threshold)
		VALUES (1, 'Soda', 50000, 'TRACKED', 3) RETURNING id`).Scan(&f.tracked))
	f.must(pool.QueryRow(ctx, `INSERT INTO products (category_id, name, price_rial, inventory_mode)
		VALUES (1, 'Espresso', 90000, 'UNTRACKED') RETURNING id`).Scan(&f.untracked))
	_, err = pool.Exec(ctx, `INSERT INTO inventory_balances (product_id) VALUES ($1)`, f.tracked)
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

func (f *fx) purchase(qty int32, cost int64) store.PurchaseEntry {
	f.t.Helper()
	p, _, err := f.svc.Purchase(f.ctx, f.actor, newKey(), inventory.PurchaseInput{
		ProductID: f.tracked, Quantity: qty, UnitCostRial: cost, PurchasedOn: f.today})
	f.must(err)
	return p
}

func (f *fx) out(qty int32) (store.InventoryMovement, error) {
	m, _, err := f.svc.Adjust(f.ctx, f.actor, newKey(), f.tracked, inventory.AdjustInput{
		Direction: "OUT", Quantity: qty, ReasonCode: inventory.ReasonDamaged, Note: "broken"})
	return m, err
}

func (f *fx) balance() store.InventoryBalance {
	f.t.Helper()
	var b store.InventoryBalance
	f.must(f.pool.QueryRow(f.ctx, `SELECT product_id, on_hand, reserved, inventory_value_rial, last_unit_cost_rial,
		low_stock_alerted_at, updated_at FROM inventory_balances WHERE product_id = $1`, f.tracked).Scan(
		&b.ProductID, &b.OnHand, &b.Reserved, &b.InventoryValueRial, &b.LastUnitCostRial, &b.LowStockAlertedAt, &b.UpdatedAt))
	return b
}

func (f *fx) expectBalance(onHand int32, value int64) {
	f.t.Helper()
	if b := f.balance(); b.OnHand != onHand || b.InventoryValueRial != value {
		f.t.Fatalf("balance on_hand=%d value=%d, want %d/%d", b.OnHand, b.InventoryValueRial, onHand, value)
	}
}

// invariantsHold runs the same reconciliation as `rokh check` (INV-1..4 etc.).
func (f *fx) invariantsHold() {
	f.t.Helper()
	v, err := check.Run(f.ctx, store.New(f.pool))
	f.must(err)
	if len(v) != 0 {
		f.t.Fatalf("invariant violations: %v", v)
	}
}

// AC-18
func TestWeightedAverageCost(t *testing.T) {
	f := setup(t)
	f.purchase(10, 1000)
	f.purchase(10, 2000)
	f.expectBalance(20, 30000)
	if b := f.balance(); b.LastUnitCostRial.Int64 != 2000 {
		t.Errorf("last unit cost = %v", b.LastUnitCostRial)
	}

	m, err := f.out(5)
	f.must(err)
	if m.ValueDeltaRial != -7500 || m.OnHandAfter != 15 || m.ValueAfterRial != 22500 {
		t.Errorf("first outflow: %+v", m)
	}
	m, err = f.out(15)
	f.must(err)
	if m.ValueDeltaRial != -22500 {
		t.Errorf("last outflow cost = %d, want 22500", m.ValueDeltaRial)
	}
	f.expectBalance(0, 0)
	f.invariantsHold()
}

func TestAdjustmentInUsesGivenCostAndOutflowsSumToValue(t *testing.T) {
	f := setup(t)
	cost := int64(1000)
	_, _, err := f.svc.Adjust(f.ctx, f.actor, newKey(), f.tracked, inventory.AdjustInput{
		Direction: "IN", Quantity: 3, UnitCostRial: &cost, ReasonCode: inventory.ReasonOpeningBalance, Note: "opening count"})
	f.must(err)
	f.expectBalance(3, 3000)
	_, err = f.out(3)
	f.must(err)

	// 3 units worth 999 in total: one-by-one outflows cost 333 each and leave exactly zero.
	f.purchase(3, 333)
	var costs []int64
	for i := 0; i < 3; i++ {
		m, err := f.out(1)
		f.must(err)
		costs = append(costs, -m.ValueDeltaRial)
	}
	if costs[0]+costs[1]+costs[2] != 999 {
		t.Errorf("costs %v do not sum to the stock value 999", costs)
	}
	f.expectBalance(0, 0)
	f.invariantsHold()
}

// R-F4-1, R-F4-2
func TestPurchaseVoidIsExact(t *testing.T) {
	f := setup(t)
	p := f.purchase(10, 1000)
	_, err := f.svc.VoidPurchase(f.ctx, f.actor, p.ID, "wrong product")
	f.must(err)
	f.expectBalance(0, 0)

	f.purchase(10, 1000)
	b := f.purchase(10, 3000)
	f.expectBalance(20, 40000)
	voided, err := f.svc.VoidPurchase(f.ctx, f.actor, b.ID, "typo in cost")
	f.must(err)
	if !voided.VoidedAt.Valid || voided.VoidReason.String != "typo in cost" {
		t.Errorf("void not recorded: %+v", voided)
	}
	f.expectBalance(10, 10000) // exactly back to the pre-purchase state, no clamping
	var delta int64
	f.must(f.pool.QueryRow(f.ctx, `SELECT value_delta_rial FROM inventory_movements WHERE movement_type = 'PURCHASE_VOID' AND purchase_entry_id = $1`, b.ID).Scan(&delta))
	if delta != -30000 {
		t.Errorf("void value delta = %d, want -30000", delta)
	}
	f.invariantsHold()
}

// R-F4-3
func TestPurchaseVoidRejectedAfterConsumption(t *testing.T) {
	f := setup(t)
	p := f.purchase(10, 1000)
	_, err := f.out(1)
	f.must(err)
	before := f.balance()
	_, err = f.svc.VoidPurchase(f.ctx, f.actor, p.ID, "late")
	if !errors.Is(err, inventory.ErrPurchaseConsumed) {
		t.Fatalf("err = %v, want ErrPurchaseConsumed", err)
	}
	if after := f.balance(); after.OnHand != before.OnHand || after.InventoryValueRial != before.InventoryValueRial {
		t.Errorf("balance changed on rejected void")
	}
	// A later purchase can still be voided: only outflows *after* it matter.
	q := f.purchase(5, 2000)
	_, err = f.svc.VoidPurchase(f.ctx, f.actor, q.ID, "duplicate entry")
	f.must(err)
	f.invariantsHold()
}

// R-F4-4, R-F4-5
func TestPurchaseVoidRespectsReservationsAndIsIdempotent(t *testing.T) {
	f := setup(t)
	p := f.purchase(5, 1000)
	_, err := f.pool.Exec(f.ctx, `UPDATE inventory_balances SET reserved = 1 WHERE product_id = $1`, f.tracked)
	f.must(err)
	if _, err := f.svc.VoidPurchase(f.ctx, f.actor, p.ID, "x"); !errors.Is(err, inventory.ErrInsufficientStock) {
		t.Fatalf("void with reserved stock: %v", err)
	}
	_, err = f.pool.Exec(f.ctx, `UPDATE inventory_balances SET reserved = 0 WHERE product_id = $1`, f.tracked)
	f.must(err)
	_, err = f.svc.VoidPurchase(f.ctx, f.actor, p.ID, "x")
	f.must(err)
	again, err := f.svc.VoidPurchase(f.ctx, f.actor, p.ID, "again")
	f.must(err)
	if again.VoidReason.String != "x" {
		t.Errorf("second void changed the record: %+v", again)
	}
	var n int
	f.must(f.pool.QueryRow(f.ctx, `SELECT count(*) FROM inventory_movements WHERE movement_type = 'PURCHASE_VOID'`).Scan(&n))
	if n != 1 {
		t.Errorf("void movements = %d, want 1", n)
	}
	if _, err := f.svc.VoidPurchase(f.ctx, f.actor, 99999, "x"); !errors.Is(err, inventory.ErrNotFound) {
		t.Errorf("unknown purchase: %v", err)
	}
	if _, err := f.svc.VoidPurchase(f.ctx, f.actor, p.ID, "  "); err == nil {
		t.Error("blank reason accepted")
	}
}

// AC-20
func TestAdjustmentRules(t *testing.T) {
	f := setup(t)
	f.purchase(4, 1000)
	_, err := f.pool.Exec(f.ctx, `UPDATE inventory_balances SET reserved = 2 WHERE product_id = $1`, f.tracked)
	f.must(err)
	if _, err := f.out(3); !errors.Is(err, inventory.ErrInsufficientStock) {
		t.Errorf("OUT beyond available (reserved stock) = %v", err)
	}
	if _, err := f.out(2); err != nil {
		t.Errorf("OUT of available stock: %v", err)
	}

	cost := int64(10)
	bad := []inventory.AdjustInput{
		{Direction: "IN", Quantity: 1, ReasonCode: inventory.ReasonOpeningBalance, Note: "n"},                // no cost
		{Direction: "IN", Quantity: 1, UnitCostRial: &cost, ReasonCode: inventory.ReasonDamaged, Note: "n"},  // OUT-only reason
		{Direction: "OUT", Quantity: 1, UnitCostRial: &cost, ReasonCode: inventory.ReasonDamaged, Note: "n"}, // cost on OUT
		{Direction: "OUT", Quantity: 1, ReasonCode: inventory.ReasonOpeningBalance, Note: "n"},               // IN-only reason
		{Direction: "OUT", Quantity: 1, ReasonCode: inventory.ReasonDamaged, Note: "   "},                    // blank note
		{Direction: "OUT", Quantity: 0, ReasonCode: inventory.ReasonDamaged, Note: "n"},                      // zero qty
		{Direction: "SIDEWAYS", Quantity: 1, ReasonCode: inventory.ReasonDamaged, Note: "n"},                 // bad direction
	}
	for i, in := range bad {
		var v inventory.ValidationError
		if _, _, err := f.svc.Adjust(f.ctx, f.actor, newKey(), f.tracked, in); !errors.As(err, &v) {
			t.Errorf("case %d accepted: %v", i, err)
		}
	}
	var v inventory.ValidationError
	if _, _, err := f.svc.Adjust(f.ctx, f.actor, newKey(), f.untracked, inventory.AdjustInput{
		Direction: "IN", Quantity: 1, UnitCostRial: &cost, ReasonCode: inventory.ReasonOpeningBalance, Note: "n"}); !errors.As(err, &v) {
		t.Errorf("adjusting an UNTRACKED product: %v", err)
	}
	if _, _, err := f.svc.Purchase(f.ctx, f.actor, newKey(), inventory.PurchaseInput{
		ProductID: f.untracked, Quantity: 1, UnitCostRial: 1, PurchasedOn: f.today}); !errors.As(err, &v) {
		t.Errorf("purchasing an UNTRACKED product: %v", err)
	}
	tehran, _ := time.LoadLocation("Asia/Tehran")
	tomorrow := time.Now().In(tehran).AddDate(0, 0, 1).Format(time.DateOnly)
	for _, d := range []string{tomorrow, "2999-01-01"} {
		if _, _, err := f.svc.Purchase(f.ctx, f.actor, newKey(), inventory.PurchaseInput{
			ProductID: f.tracked, Quantity: 1, UnitCostRial: 1, PurchasedOn: d}); !errors.As(err, &v) {
			t.Errorf("future purchase date %s: %v", d, err)
		}
	}
	yesterday := time.Now().In(tehran).AddDate(0, 0, -1).Format(time.DateOnly)
	for _, d := range []string{f.today, yesterday} {
		if _, _, err := f.svc.Purchase(f.ctx, f.actor, newKey(), inventory.PurchaseInput{
			ProductID: f.tracked, Quantity: 1, UnitCostRial: 1, PurchasedOn: d}); err != nil {
			t.Errorf("purchase dated %s rejected: %v", d, err)
		}
	}
}

func TestIdempotentPurchaseAndAdjustment(t *testing.T) {
	f := setup(t)
	key := newKey()
	in := inventory.PurchaseInput{ProductID: f.tracked, Quantity: 3, UnitCostRial: 500, PurchasedOn: f.today, Supplier: " Shop "}
	p1, replay, err := f.svc.Purchase(f.ctx, f.actor, key, in)
	f.must(err)
	if replay {
		t.Error("first call reported as replay")
	}
	p2, replay, err := f.svc.Purchase(f.ctx, f.actor, key, in)
	f.must(err)
	if !replay || p2.ID != p1.ID {
		t.Errorf("replay = %v id %d vs %d", replay, p2.ID, p1.ID)
	}
	in.Quantity = 4
	if _, _, err := f.svc.Purchase(f.ctx, f.actor, key, in); !errors.Is(err, inventory.ErrIdempotencyReused) {
		t.Errorf("different body same key: %v", err)
	}
	f.expectBalance(3, 1500)

	// Concurrent duplicates: exactly one purchase.
	key = newKey()
	in = inventory.PurchaseInput{ProductID: f.tracked, Quantity: 1, UnitCostRial: 100, PurchasedOn: f.today}
	var wg sync.WaitGroup
	errs := make([]error, 10)
	start := make(chan struct{})
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, errs[i] = f.svc.Purchase(f.ctx, f.actor, key, in)
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent duplicate %d: %v", i, err)
		}
	}
	f.expectBalance(4, 1600)

	akey := newKey()
	adj := inventory.AdjustInput{Direction: "OUT", Quantity: 1, ReasonCode: inventory.ReasonExpired, Note: "expired"}
	m1, _, err := f.svc.Adjust(f.ctx, f.actor, akey, f.tracked, adj)
	f.must(err)
	m2, replay, err := f.svc.Adjust(f.ctx, f.actor, akey, f.tracked, adj)
	f.must(err)
	if !replay || m1.ID != m2.ID {
		t.Errorf("adjustment replay: %v %d %d", replay, m1.ID, m2.ID)
	}
	adj.Quantity = 2
	if _, _, err := f.svc.Adjust(f.ctx, f.actor, akey, f.tracked, adj); !errors.Is(err, inventory.ErrIdempotencyReused) {
		t.Errorf("adjustment key reuse: %v", err)
	}
	f.expectBalance(3, 1200)
	f.invariantsHold()
}

func TestConcurrentOutflowsNeverOversell(t *testing.T) {
	f := setup(t)
	f.purchase(10, 777)
	var wg sync.WaitGroup
	results := make([]error, 25)
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, results[i] = f.out(1)
		}()
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, inventory.ErrInsufficientStock):
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 10 {
		t.Errorf("%d outflows succeeded, want exactly 10", ok)
	}
	f.expectBalance(0, 0)
	f.invariantsHold()
}

// Void and an outflow racing: whichever commits first wins; the ledger stays consistent.
func TestVoidRacingOutflowStaysConsistent(t *testing.T) {
	f := setup(t)
	for round := 0; round < 15; round++ {
		p := f.purchase(2, 1000)
		var wg sync.WaitGroup
		var voidErr, outErr error
		start := make(chan struct{})
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, voidErr = f.svc.VoidPurchase(f.ctx, f.actor, p.ID, "race") }()
		go func() { defer wg.Done(); <-start; _, outErr = f.out(1) }()
		close(start)
		wg.Wait()
		if voidErr != nil && !errors.Is(voidErr, inventory.ErrPurchaseConsumed) && !errors.Is(voidErr, inventory.ErrInsufficientStock) {
			t.Fatalf("round %d void: %v", round, voidErr)
		}
		if outErr != nil && !errors.Is(outErr, inventory.ErrInsufficientStock) {
			t.Fatalf("round %d out: %v", round, outErr)
		}
		f.invariantsHold()
	}
}

// AC-19: after a randomized sequence of operations the balance equals the ledger, value is
// never negative and zero stock means zero value.
func TestRandomizedLedgerInvariants(t *testing.T) {
	f := setup(t)
	rng := mrand.New(mrand.NewPCG(42, 2026))
	var purchases []int64
	for i := 0; i < 200; i++ {
		switch rng.IntN(5) {
		case 0, 1:
			p := f.purchase(int32(1+rng.IntN(8)), int64(rng.IntN(5000)))
			purchases = append(purchases, p.ID)
		case 2:
			if len(purchases) > 0 {
				_, err := f.svc.VoidPurchase(f.ctx, f.actor, purchases[rng.IntN(len(purchases))], "random void")
				if err != nil && !errors.Is(err, inventory.ErrPurchaseConsumed) && !errors.Is(err, inventory.ErrInsufficientStock) {
					t.Fatal(err)
				}
			}
		case 3:
			cost := int64(rng.IntN(3000))
			_, _, err := f.svc.Adjust(f.ctx, f.actor, newKey(), f.tracked, inventory.AdjustInput{
				Direction: "IN", Quantity: int32(1 + rng.IntN(4)), UnitCostRial: &cost, ReasonCode: inventory.ReasonCountCorrection, Note: "count"})
			f.must(err)
		case 4:
			if _, err := f.out(int32(1 + rng.IntN(6))); err != nil && !errors.Is(err, inventory.ErrInsufficientStock) {
				t.Fatal(err)
			}
		}
		if b := f.balance(); b.InventoryValueRial < 0 || (b.OnHand == 0 && b.InventoryValueRial != 0) {
			t.Fatalf("step %d: on_hand=%d value=%d", i, b.OnHand, b.InventoryValueRial)
		}
	}
	f.invariantsHold()
}

// WORKFLOWS §4.1 / AC-39 (detection part): one alert per episode, re-armed by inflow only.
func TestLowStockEpisodes(t *testing.T) {
	f := setup(t)
	var adminID int64 = f.actor.UserID
	_, err := f.pool.Exec(f.ctx, `INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth) VALUES ($1, 'https://fcm.googleapis.com/a', 'k', 'a')`, adminID)
	f.must(err)
	var opID int64
	f.must(f.pool.QueryRow(f.ctx, `INSERT INTO users (username, display_name, password_hash, role) VALUES ('barista','B','x','OPERATOR') RETURNING id`).Scan(&opID))
	_, err = f.pool.Exec(f.ctx, `INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth) VALUES ($1, 'https://fcm.googleapis.com/b', 'k', 'a')`, opID)
	f.must(err)

	events := func() int {
		var n int
		f.must(f.pool.QueryRow(f.ctx, `SELECT count(*) FROM notification_events WHERE event_type = 'LOW_STOCK'`).Scan(&n))
		return n
	}

	f.purchase(6, 100) // threshold is 3
	if f.balance().LowStockAlertedAt.Valid || events() != 0 {
		t.Fatal("alert raised above threshold")
	}
	_, err = f.out(3) // available 3 = threshold → alert
	f.must(err)
	if !f.balance().LowStockAlertedAt.Valid || events() != 1 {
		t.Fatalf("expected one alert (ADMIN only), got %d", events())
	}
	var body, url string
	f.must(f.pool.QueryRow(f.ctx, `SELECT payload->>'body', payload->>'url' FROM notification_events WHERE event_type = 'LOW_STOCK'`).Scan(&body, &url))
	if body != "Soda: ۳ عدد باقی مانده" || url != "/admin/inventory?filter=low" {
		t.Errorf("alert payload: %q %q (Persian digits, inventory link)", body, url)
	}
	_, err = f.out(1)
	f.must(err)
	if events() != 1 {
		t.Error("same episode must not alert again")
	}
	f.purchase(1, 100) // available 3: still at threshold, no re-arm
	if !f.balance().LowStockAlertedAt.Valid {
		t.Error("re-armed without rising above the threshold")
	}
	f.purchase(5, 100) // available 8 → re-arm
	if f.balance().LowStockAlertedAt.Valid {
		t.Error("inflow above threshold did not re-arm")
	}
	_, err = f.out(6) // immediately: a new episode must alert even within the same second
	f.must(err)
	if events() != 2 {
		t.Errorf("second episode events = %d, want 2", events())
	}
}
