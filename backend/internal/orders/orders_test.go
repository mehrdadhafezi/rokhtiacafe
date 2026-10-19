package orders_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"sort"
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
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/testdb"
)

const (
	sodaPrice     = 50_000
	cakePrice     = 100_000
	espressoPrice = 90_000
)

type fx struct {
	t                    *testing.T
	ctx                  context.Context
	pool                 *pgxpool.Pool
	cfg                  orders.Config
	svc                  *orders.Service
	inv                  *inventory.Service
	staff                audit.Actor
	soda, cake, espresso int64
	ip                   netip.Addr
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
	f := &fx{t: t, ctx: ctx, pool: pool, ip: netip.MustParseAddr("203.0.113.10"),
		cfg: orders.Config{PendingTTL: 20 * time.Minute, MaxPending: 40, MaxPendingPerClient: 15, Hours: hours, Location: tehran, Secret: secret},
		inv: &inventory.Service{Pool: pool, Location: tehran}}
	f.svc = orders.New(pool, f.cfg)

	var uid int64
	f.must(pool.QueryRow(ctx, `INSERT INTO users (username, display_name, password_hash, role) VALUES ('admin','A','x','ADMIN') RETURNING id`).Scan(&uid))
	f.staff = audit.UserActor(uid, netip.MustParseAddr("127.0.0.1"))
	f.must(pool.QueryRow(ctx, `INSERT INTO categories (name) VALUES ('c') RETURNING id`).Scan(new(int64)))
	f.soda = f.product("Soda", sodaPrice, "TRACKED", nil)
	f.cake = f.product("Cake", cakePrice, "TRACKED", nil)
	est := int64(30_000)
	f.espresso = f.product("Espresso", espressoPrice, "UNTRACKED", &est)
	f.stock(f.soda, 5, 1000)
	f.stock(f.cake, 10, 3000)
	return f
}

func (f *fx) product(name string, price int64, mode string, estimate *int64) int64 {
	f.t.Helper()
	var id int64
	f.must(f.pool.QueryRow(f.ctx, `INSERT INTO products (category_id, name, price_rial, inventory_mode, estimated_unit_cost_rial)
		VALUES (1, $1, $2, $3, $4) RETURNING id`, name, price, mode, estimate).Scan(&id))
	if mode == "TRACKED" {
		_, err := f.pool.Exec(f.ctx, `INSERT INTO inventory_balances (product_id) VALUES ($1)`, id)
		f.must(err)
	}
	return id
}

func (f *fx) stock(productID int64, qty int32, cost int64) {
	f.t.Helper()
	_, _, err := f.inv.Purchase(f.ctx, f.staff, newKey(), inventory.PurchaseInput{ProductID: productID, Quantity: qty,
		UnitCostRial: cost, PurchasedOn: time.Now().In(f.cfg.Location).Format(time.DateOnly)})
	f.must(err)
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

func item(productID int64, qty int32, price int64) orders.Item {
	return orders.Item{ProductID: productID, Quantity: qty, ExpectedUnitPriceRial: price}
}

func (f *fx) order(svc *orders.Service, key pgtype.UUID, items ...orders.Item) (orders.Detail, bool, error) {
	return svc.Create(f.ctx, orders.CreateInput{Key: key, ClientIP: f.ip, Label: "Sara", Items: items})
}

func (f *fx) mustOrder(items ...orders.Item) orders.Detail {
	f.t.Helper()
	d, created, err := f.order(f.svc, newKey(), items...)
	f.must(err)
	if !created {
		f.t.Fatal("new key reported as replay")
	}
	return d
}

func (f *fx) balance(productID int64) (onHand, reserved int32, value int64) {
	f.t.Helper()
	f.must(f.pool.QueryRow(f.ctx, `SELECT on_hand, reserved, inventory_value_rial FROM inventory_balances WHERE product_id = $1`, productID).
		Scan(&onHand, &reserved, &value))
	return
}

func (f *fx) invariantsHold() {
	f.t.Helper()
	v, err := check.Run(f.ctx, store.New(f.pool))
	f.must(err)
	if len(v) != 0 {
		f.t.Fatalf("invariant violations: %v", v)
	}
}

func (f *fx) toReady(id int64) {
	f.t.Helper()
	_, err := f.svc.Accept(f.ctx, f.staff, id)
	f.must(err)
	_, err = f.svc.Ready(f.ctx, f.staff, id)
	f.must(err)
}

// AC-02, AC-22 (reservation part)
func TestCreateOrder(t *testing.T) {
	f := setup(t)
	d := f.mustOrder(item(f.soda, 2, sodaPrice), item(f.espresso, 1, espressoPrice))
	o := d.Order
	if o.Status != "PENDING" || o.PaymentStatus.String != "UNPAID" || o.TotalRial != 2*sodaPrice+espressoPrice || o.DisplayNumber != 1 {
		t.Errorf("order = %+v", o)
	}
	if len(d.Token) != 43 || len(d.Items) != 2 || d.Items[0].UnitPriceRial != sodaPrice || d.Items[1].ProductName != "Espresso" {
		t.Errorf("token %q items %+v", d.Token, d.Items)
	}
	if !o.ExpiresAt.Time.After(o.CreatedAt.Time.Add(19 * time.Minute)) {
		t.Errorf("expires_at = %v", o.ExpiresAt.Time)
	}
	if _, reserved, _ := f.balance(f.soda); reserved != 2 {
		t.Errorf("soda reserved = %d", reserved)
	}
	var n int
	f.must(f.pool.QueryRow(f.ctx, `SELECT count(*) FROM inventory_balances WHERE product_id = $1`, f.espresso).Scan(&n))
	if n != 0 {
		t.Error("UNTRACKED product got a balance/reservation")
	}
	if d2 := f.mustOrder(item(f.cake, 1, cakePrice)); d2.Order.DisplayNumber != 2 {
		t.Errorf("second order number = %d", d2.Order.DisplayNumber)
	}
	f.invariantsHold()
}

// AC-03, AC-04, R-F3-5, R-F11-2
func TestIdempotentReplay(t *testing.T) {
	f := setup(t)
	key := newKey()
	d1, created, err := f.order(f.svc, key, item(f.soda, 1, sodaPrice))
	f.must(err)
	if !created {
		t.Fatal("first call not created")
	}
	d2, created, err := f.order(f.svc, key, item(f.soda, 1, sodaPrice))
	f.must(err)
	if created || d2.Order.ID != d1.Order.ID || d2.Token != d1.Token || d2.Order.DisplayNumber != d1.Order.DisplayNumber {
		t.Errorf("replay differs: %+v vs %+v", d2.Order, d1.Order)
	}
	if _, reserved, _ := f.balance(f.soda); reserved != 1 {
		t.Errorf("reserved = %d after replay, want 1", reserved)
	}
	if _, _, err := f.order(f.svc, key, item(f.soda, 2, sodaPrice)); !errors.Is(err, orders.ErrIdempotencyReused) {
		t.Errorf("different body: %v", err)
	}

	// R-F3-5: the replay reflects the order's current state.
	_, err = f.svc.Accept(f.ctx, f.staff, d1.Order.ID)
	f.must(err)
	d3, _, err := f.order(f.svc, key, item(f.soda, 1, sodaPrice))
	f.must(err)
	if d3.Order.Status != "PREPARING" {
		t.Errorf("replay status = %s", d3.Order.Status)
	}

	// R-F11-2: only the hash is stored; it matches the token.
	var stored []byte
	f.must(f.pool.QueryRow(f.ctx, `SELECT tracking_token_hash FROM orders WHERE id = $1`, d1.Order.ID).Scan(&stored))
	if !bytes.Equal(stored, orders.HashToken(d1.Token)) || bytes.Contains(stored, []byte(d1.Token)) {
		t.Error("tracking token not stored as its hash")
	}
	f.invariantsHold()
}

// R-F3-1, R-F3-2
func TestConcurrentDuplicatesCreateOneOrder(t *testing.T) {
	f := setup(t)
	last := f.product("Last bottle", 30_000, "TRACKED", nil)
	f.stock(last, 1, 500)
	key := newKey()
	var wg sync.WaitGroup
	results := make([]orders.Detail, 10)
	errs := make([]error, 10)
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], _, errs[i] = f.order(f.svc, key, item(last, 1, 30_000))
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("duplicate %d failed (final unit must not report INSUFFICIENT_STOCK): %v", i, err)
		}
		if results[i].Order.ID != results[0].Order.ID || results[i].Token != results[0].Token {
			t.Errorf("duplicate %d returned a different order", i)
		}
	}
	var n int
	f.must(f.pool.QueryRow(f.ctx, `SELECT count(*) FROM orders`).Scan(&n))
	if _, reserved, _ := f.balance(last); n != 1 || reserved != 1 {
		t.Errorf("orders=%d reserved=%d, want 1/1", n, reserved)
	}
	f.invariantsHold()
}

// AC-05: no oversell under concurrency.
func TestConcurrentOrdersNeverOversell(t *testing.T) {
	f := setup(t) // soda stock 5
	var wg sync.WaitGroup
	errs := make([]error, 20)
	start := make(chan struct{})
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, errs[i] = f.svc.Create(f.ctx, orders.CreateInput{Key: newKey(),
				ClientIP: netip.AddrFrom4([4]byte{10, 0, 0, byte(i)}), Items: []orders.Item{item(f.soda, 1, sodaPrice)}})
		}()
	}
	close(start)
	wg.Wait()
	ok, short := 0, 0
	for _, err := range errs {
		var s orders.InsufficientStockError
		switch {
		case err == nil:
			ok++
		case errors.As(err, &s):
			short++
			if len(s.ProductIDs) != 1 || s.ProductIDs[0] != f.soda {
				t.Errorf("short ids = %v", s.ProductIDs)
			}
		default:
			t.Errorf("unexpected: %v", err)
		}
	}
	if ok != 5 || short != 15 {
		t.Errorf("ok=%d short=%d, want 5/15", ok, short)
	}
	if onHand, reserved, _ := f.balance(f.soda); reserved != 5 || onHand != 5 {
		t.Errorf("on_hand=%d reserved=%d", onHand, reserved)
	}
	f.invariantsHold()
}

// AC-06, AC-07, R-F13-1, R-F3-3
func TestCreateRejections(t *testing.T) {
	f := setup(t)
	if _, err := f.pool.Exec(f.ctx, `UPDATE products SET is_available = false WHERE id = $1`, f.cake); err != nil {
		t.Fatal(err)
	}
	var un orders.UnavailableError
	if _, _, err := f.order(f.svc, newKey(), item(f.soda, 1, sodaPrice), item(f.cake, 1, cakePrice), item(9999, 1, 10)); !errors.As(err, &un) || len(un.ProductIDs) != 2 {
		t.Errorf("unavailable = %v %v", err, un.ProductIDs)
	}

	var pc orders.PriceChangedError
	if _, _, err := f.order(f.svc, newKey(), item(f.soda, 1, sodaPrice-10)); !errors.As(err, &pc) ||
		len(pc.Items) != 1 || pc.Items[0].CurrentUnitPriceRial != sodaPrice {
		t.Errorf("price change = %v %+v", err, pc.Items)
	}
	if _, reserved, _ := f.balance(f.soda); reserved != 0 {
		t.Errorf("rejected orders reserved stock: %d", reserved)
	}

	var bad orders.ValidationError
	for _, items := range [][]orders.Item{nil, {item(f.soda, 21, sodaPrice)}, {item(f.soda, 1, sodaPrice), item(f.soda, 1, sodaPrice)}, {item(f.soda, 1, 0)}} {
		if _, _, err := f.order(f.svc, newKey(), items...); !errors.As(err, &bad) {
			t.Errorf("items %v accepted: %v", items, err)
		}
	}

	// Kill switch closes ordering, but a replay of an existing order still succeeds (R-F3-3).
	key := newKey()
	_, _, err := f.order(f.svc, key, item(f.soda, 1, sodaPrice))
	f.must(err)
	f.must(f.svc.SetOrderingOpen(f.ctx, f.staff, false))
	if _, _, err := f.order(f.svc, newKey(), item(f.soda, 1, sodaPrice)); !errors.Is(err, orders.ErrOrderingClosed) {
		t.Errorf("closed: %v", err)
	}
	if _, created, err := f.order(f.svc, key, item(f.soda, 1, sodaPrice)); err != nil || created {
		t.Errorf("replay after closing: created=%v err=%v", created, err)
	}
	f.must(f.svc.SetOrderingOpen(f.ctx, f.staff, true))

	night, _ := config.ParseOrderingHours("03:00-03:01")
	cfg := f.cfg
	cfg.Hours = night
	closed := orders.New(f.pool, cfg)
	if !closed.IsOpen(time.Now()) {
		if _, _, err := f.order(closed, newKey(), item(f.soda, 1, sodaPrice)); !errors.Is(err, orders.ErrOrderingClosed) {
			t.Errorf("outside hours: %v", err)
		}
	}
	f.invariantsHold()
}

// AC-07 caps and R-F3-4: caps hold under concurrency (serialized by the creation lock).
func TestPendingCapsUnderConcurrency(t *testing.T) {
	f := setup(t)
	cfg := f.cfg
	cfg.MaxPending = 5
	svc := orders.New(f.pool, cfg)
	for i := 0; i < 4; i++ {
		_, _, err := svc.Create(f.ctx, orders.CreateInput{Key: newKey(), ClientIP: netip.AddrFrom4([4]byte{10, 1, 0, byte(i)}),
			Items: []orders.Item{item(f.espresso, 1, espressoPrice)}})
		f.must(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 10)
	start := make(chan struct{})
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, errs[i] = svc.Create(f.ctx, orders.CreateInput{Key: newKey(), ClientIP: netip.AddrFrom4([4]byte{10, 2, 0, byte(i)}),
				Items: []orders.Item{item(f.espresso, 1, espressoPrice)}})
		}()
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, orders.ErrOrderingBusy) {
			t.Errorf("unexpected: %v", err)
		}
	}
	var pending int
	f.must(f.pool.QueryRow(f.ctx, `SELECT count(*) FROM orders WHERE status = 'PENDING'`).Scan(&pending))
	if ok != 1 || pending != 5 {
		t.Errorf("ok=%d pending=%d, want 1 and 5", ok, pending)
	}

	cfg = f.cfg
	cfg.MaxPendingPerClient = 2
	perClient := orders.New(f.pool, cfg)
	shared := netip.MustParseAddr("198.51.100.77")
	for i := 0; i < 2; i++ {
		_, _, err := perClient.Create(f.ctx, orders.CreateInput{Key: newKey(), ClientIP: shared, Items: []orders.Item{item(f.espresso, 1, espressoPrice)}})
		f.must(err)
	}
	if _, _, err := perClient.Create(f.ctx, orders.CreateInput{Key: newKey(), ClientIP: shared, Items: []orders.Item{item(f.espresso, 1, espressoPrice)}}); !errors.Is(err, orders.ErrTooManyOpenOrders) {
		t.Errorf("per-client cap: %v", err)
	}
	if _, _, err := perClient.Create(f.ctx, orders.CreateInput{Key: newKey(), ClientIP: netip.MustParseAddr("198.51.100.78"), Items: []orders.Item{item(f.espresso, 1, espressoPrice)}}); err != nil {
		t.Errorf("other network blocked: %v", err)
	}
}

// R-F3-6
func TestDailyNumbersUniqueAndGapFreeUnderConcurrency(t *testing.T) {
	f := setup(t)
	var wg sync.WaitGroup
	nums := make([]int32, 30)
	start := make(chan struct{})
	for i := range nums {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			d, _, err := f.svc.Create(f.ctx, orders.CreateInput{Key: newKey(), ClientIP: netip.AddrFrom4([4]byte{10, 3, byte(i / 10), byte(i)}),
				Items: []orders.Item{item(f.espresso, 1, espressoPrice)}})
			if err != nil {
				t.Error(err)
				return
			}
			nums[i] = d.Order.DisplayNumber
		}()
	}
	close(start)
	wg.Wait()
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
	for i, n := range nums {
		if n != int32(i+1) {
			t.Fatalf("numbers %v are not 1..30", nums)
		}
	}
}

// AC-12, AC-13, AC-22 (completion part), AC-18 via orders
func TestLifecycleAndCompletionCosts(t *testing.T) {
	f := setup(t)
	d := f.mustOrder(item(f.soda, 2, sodaPrice), item(f.espresso, 3, espressoPrice))
	id := d.Order.ID

	var ill orders.IllegalTransitionError
	if _, err := f.svc.Ready(f.ctx, f.staff, id); !errors.As(err, &ill) || ill.Current != "PENDING" {
		t.Errorf("PENDING→READY: %v", err)
	}
	if _, err := f.svc.Complete(f.ctx, f.staff, id); !errors.As(err, &ill) {
		t.Errorf("PENDING→COMPLETED: %v", err)
	}
	_, err := f.svc.Accept(f.ctx, f.staff, id)
	f.must(err)
	if again, err := f.svc.Accept(f.ctx, f.staff, id); err != nil || again.Order.Status != "PREPARING" {
		t.Errorf("double accept: %v %s", err, again.Order.Status)
	}
	_, err = f.svc.Ready(f.ctx, f.staff, id)
	f.must(err)
	done, err := f.svc.Complete(f.ctx, f.staff, id)
	f.must(err)
	if done.Order.Status != "COMPLETED" {
		t.Fatalf("status = %s", done.Order.Status)
	}
	for _, it := range done.Items {
		switch it.ProductID {
		case f.soda:
			if it.CogsRial.Int64 != 2000 || it.EstimatedCostRial.Valid { // 5 @ 1000 → 2 cost 2000
				t.Errorf("soda line costs %+v", it)
			}
		case f.espresso:
			if !it.CogsRial.Valid || it.CogsRial.Int64 != 0 || it.EstimatedCostRial.Int64 != 90_000 {
				t.Errorf("espresso line costs %+v", it)
			}
		}
	}
	if onHand, reserved, value := f.balance(f.soda); onHand != 3 || reserved != 0 || value != 3000 {
		t.Errorf("soda after sale: %d/%d/%d", onHand, reserved, value)
	}
	if again, err := f.svc.Complete(f.ctx, f.staff, id); err != nil || again.Order.Status != "COMPLETED" {
		t.Errorf("double complete: %v", err)
	}
	if _, err := f.svc.Cancel(f.ctx, f.staff, id, orders.CancelInput{}); !errors.As(err, &ill) || ill.Current != "COMPLETED" {
		t.Errorf("cancel completed: %v", err)
	}
	var sales int
	f.must(f.pool.QueryRow(f.ctx, `SELECT count(*) FROM inventory_movements WHERE movement_type = 'SALE' AND order_id = $1`, id).Scan(&sales))
	if sales != 1 {
		t.Errorf("SALE movements = %d", sales)
	}
	f.invariantsHold()
}

// AC-14: concurrent completion deducts once.
func TestConcurrentCompletionDeductsOnce(t *testing.T) {
	f := setup(t)
	for round := 0; round < 10; round++ {
		d := f.mustOrder(item(f.cake, 1, cakePrice))
		f.toReady(d.Order.ID)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, err := f.svc.Complete(f.ctx, f.staff, d.Order.ID); err != nil {
					t.Errorf("complete: %v", err)
				}
			}()
		}
		close(start)
		wg.Wait()
	}
	if onHand, reserved, _ := f.balance(f.cake); onHand != 0 || reserved != 0 {
		t.Errorf("cake after 10 sales: on_hand=%d reserved=%d", onHand, reserved)
	}
	f.invariantsHold()
}

// R-F1-3: complete racing cancel — exactly one wins and stock reflects only the winner.
func TestCompleteRacingCancel(t *testing.T) {
	f := setup(t)
	f.stock(f.cake, 20, 3000)
	for round := 0; round < 15; round++ {
		before, _, _ := f.balance(f.cake)
		d := f.mustOrder(item(f.cake, 1, cakePrice))
		f.toReady(d.Order.ID)
		var wg sync.WaitGroup
		var cErr, xErr error
		start := make(chan struct{})
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, cErr = f.svc.Complete(f.ctx, f.staff, d.Order.ID) }()
		go func() {
			defer wg.Done()
			<-start
			_, xErr = f.svc.Cancel(f.ctx, f.staff, d.Order.ID, orders.CancelInput{})
		}()
		close(start)
		wg.Wait()
		final, err := f.svc.Get(f.ctx, d.Order.ID)
		f.must(err)
		after, reserved, _ := f.balance(f.cake)
		switch final.Order.Status {
		case "COMPLETED":
			if cErr != nil || after != before-1 {
				t.Fatalf("round %d completed: err=%v on_hand %d→%d", round, cErr, before, after)
			}
		case "CANCELLED":
			if xErr != nil || after != before {
				t.Fatalf("round %d cancelled: err=%v on_hand %d→%d", round, xErr, before, after)
			}
		default:
			t.Fatalf("round %d status %s", round, final.Order.Status)
		}
		if reserved != 0 {
			t.Fatalf("round %d reserved = %d", round, reserved)
		}
	}
	f.invariantsHold()
}

// AC-10, AC-15, R-F2-5, AC-11
func TestCustomerCancelAndTracking(t *testing.T) {
	f := setup(t)
	d := f.mustOrder(item(f.soda, 2, sodaPrice))
	tr, err := f.svc.Track(f.ctx, d.Token)
	f.must(err)
	if tr.Order.ID != d.Order.ID || len(tr.Items) != 1 {
		t.Errorf("track = %+v", tr)
	}
	c, err := f.svc.CustomerCancel(f.ctx, d.Token, f.ip)
	f.must(err)
	if c.Order.Status != "CANCELLED" || c.Order.CancelReason.String != "CUSTOMER" || c.Order.CancelledBy.Valid {
		t.Errorf("customer cancel = %+v", c.Order)
	}
	if _, reserved, _ := f.balance(f.soda); reserved != 0 {
		t.Errorf("reserved = %d", reserved)
	}
	if _, err := f.svc.CustomerCancel(f.ctx, d.Token, f.ip); err != nil {
		t.Errorf("repeat customer cancel: %v", err)
	}

	accepted := f.mustOrder(item(f.soda, 1, sodaPrice))
	_, err = f.svc.Accept(f.ctx, f.staff, accepted.Order.ID)
	f.must(err)
	var ill orders.IllegalTransitionError
	if _, err := f.svc.CustomerCancel(f.ctx, accepted.Token, f.ip); !errors.As(err, &ill) || ill.Current != "PREPARING" {
		t.Errorf("customer cancel after accept: %v", err)
	}

	for _, tok := range []string{"", "nope", accepted.Token + "x"} {
		if _, err := f.svc.Track(f.ctx, tok); !errors.Is(err, orders.ErrNotFound) {
			t.Errorf("token %q: %v", tok, err)
		}
	}
	f.svc.SetClock(func() time.Time { return time.Now().Add(25 * time.Hour) })
	if _, err := f.svc.Track(f.ctx, accepted.Token); !errors.Is(err, orders.ErrNotFound) {
		t.Errorf("25h old token: %v", err)
	}
	f.invariantsHold()
}

// R-F2-1, R-F2-5, AC-16
func TestAcceptanceDeadlineAndExpiry(t *testing.T) {
	f := setup(t)
	cfg := f.cfg
	cfg.PendingTTL = 300 * time.Millisecond
	short := orders.New(f.pool, cfg)
	late, _, err := f.order(short, newKey(), item(f.soda, 2, sodaPrice))
	f.must(err)
	swept, _, err := f.order(short, newKey(), item(f.soda, 1, sodaPrice))
	f.must(err)
	cust, _, err := f.order(short, newKey(), item(f.soda, 1, sodaPrice))
	f.must(err)
	keep := f.mustOrder(item(f.soda, 1, sodaPrice)) // normal TTL
	_, err = f.svc.Accept(f.ctx, f.staff, keep.Order.ID)
	f.must(err)
	time.Sleep(400 * time.Millisecond)

	d, err := short.Accept(f.ctx, f.staff, late.Order.ID)
	if !errors.Is(err, orders.ErrOrderExpired) || d.Order.Status != "CANCELLED" || d.Order.CancelReason.String != "EXPIRED" {
		t.Fatalf("accept after deadline: %v %+v", err, d.Order)
	}
	var ill orders.IllegalTransitionError
	if _, err := short.Accept(f.ctx, f.staff, late.Order.ID); !errors.As(err, &ill) {
		t.Errorf("second accept: %v", err)
	}
	c, err := short.CustomerCancel(f.ctx, cust.Token, f.ip)
	if err != nil || c.Order.CancelReason.String != "EXPIRED" {
		t.Errorf("customer cancel of an expired order: %v %+v", err, c.Order)
	}

	n, err := short.ExpireDue(f.ctx)
	f.must(err)
	if n != 1 {
		t.Errorf("expired %d orders, want 1", n)
	}
	got, _ := f.svc.Get(f.ctx, swept.Order.ID)
	kept, _ := f.svc.Get(f.ctx, keep.Order.ID)
	if got.Order.CancelReason.String != "EXPIRED" || kept.Order.Status != "PREPARING" {
		t.Errorf("swept=%s/%s kept=%s", got.Order.Status, got.Order.CancelReason.String, kept.Order.Status)
	}
	if _, reserved, _ := f.balance(f.soda); reserved != 1 { // only the accepted order holds stock
		t.Errorf("reserved = %d, want 1", reserved)
	}
	f.invariantsHold()
}

// R-F2-2: accept racing the expiry job at the deadline.
func TestAcceptRacingExpiry(t *testing.T) {
	f := setup(t)
	cfg := f.cfg
	cfg.PendingTTL = 150 * time.Millisecond
	short := orders.New(f.pool, cfg)
	for round := 0; round < 15; round++ {
		d, _, err := f.order(short, newKey(), item(f.cake, 1, cakePrice))
		f.must(err)
		time.Sleep(time.Duration(120+round*4) * time.Millisecond)
		var wg sync.WaitGroup
		var aErr error
		start := make(chan struct{})
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, aErr = short.Accept(f.ctx, f.staff, d.Order.ID) }()
		go func() { defer wg.Done(); <-start; _, _ = short.ExpireDue(f.ctx) }()
		close(start)
		wg.Wait()
		final, _ := f.svc.Get(f.ctx, d.Order.ID)
		var ill orders.IllegalTransitionError
		switch {
		case final.Order.Status == "PREPARING":
			if aErr != nil || !final.Order.AcceptedAt.Time.Before(final.Order.ExpiresAt.Time) {
				t.Fatalf("round %d: accepted %v at %v deadline %v", round, aErr, final.Order.AcceptedAt.Time, final.Order.ExpiresAt.Time)
			}
			_, err = f.svc.Cancel(f.ctx, f.staff, d.Order.ID, orders.CancelInput{}) // release for the next round
			f.must(err)
		case final.Order.Status == "CANCELLED" && final.Order.CancelReason.String == "EXPIRED":
			if aErr != nil && !errors.Is(aErr, orders.ErrOrderExpired) && !errors.As(aErr, &ill) {
				t.Fatalf("round %d accept error: %v", round, aErr)
			}
		default:
			t.Fatalf("round %d: %s/%s", round, final.Order.Status, final.Order.CancelReason.String)
		}
		f.invariantsHold()
	}
}

// R-F2-3: two expiry workers over many expired orders.
func TestParallelExpiryWorkers(t *testing.T) {
	f := setup(t)
	cfg := f.cfg
	cfg.PendingTTL = 200 * time.Millisecond
	short := orders.New(f.pool, cfg)
	for i := 0; i < 30; i++ {
		_, _, err := short.Create(f.ctx, orders.CreateInput{Key: newKey(), ClientIP: netip.AddrFrom4([4]byte{10, 4, 0, byte(i)}),
			Items: []orders.Item{item(f.cake, 1, cakePrice)}})
		if err != nil && !errors.As(err, new(orders.InsufficientStockError)) {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond)
	var wg sync.WaitGroup
	counts := make([]int, 2)
	for i := range counts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := short.ExpireDue(f.ctx)
			if err != nil {
				t.Error(err)
			}
			counts[i] = n
		}()
	}
	wg.Wait()
	var created int
	f.must(f.pool.QueryRow(f.ctx, `SELECT count(*) FROM orders`).Scan(&created))
	if counts[0]+counts[1] != created {
		t.Errorf("expired %v of %d orders", counts, created)
	}
	if _, reserved, _ := f.balance(f.cake); reserved != 0 {
		t.Errorf("reserved = %d after expiry", reserved)
	}
	f.invariantsHold()
}

// R-F7-1..3: cancel after preparation with waste.
func TestCancelWithWaste(t *testing.T) {
	f := setup(t)
	d := f.mustOrder(item(f.soda, 3, sodaPrice), item(f.espresso, 2, espressoPrice))
	var bad orders.ValidationError
	if _, err := f.svc.Cancel(f.ctx, f.staff, d.Order.ID, orders.CancelInput{Waste: []orders.WasteLine{{ProductID: f.soda, Quantity: 1}}}); !errors.As(err, &bad) {
		t.Errorf("waste on PENDING: %v", err)
	}
	f.toReady(d.Order.ID)
	for _, w := range [][]orders.WasteLine{{{ProductID: f.soda, Quantity: 4}}, {{ProductID: f.cake, Quantity: 1}}, {{ProductID: f.soda, Quantity: 1}, {ProductID: f.soda, Quantity: 1}}} {
		if _, err := f.svc.Cancel(f.ctx, f.staff, d.Order.ID, orders.CancelInput{Waste: w}); !errors.As(err, &bad) {
			t.Errorf("waste %v accepted: %v", w, err)
		}
	}
	c, err := f.svc.Cancel(f.ctx, f.staff, d.Order.ID, orders.CancelInput{Note: "customer left",
		Waste: []orders.WasteLine{{ProductID: f.soda, Quantity: 2}, {ProductID: f.espresso, Quantity: 1}}})
	f.must(err)
	if c.Order.Status != "CANCELLED" || c.Order.CancelReason.String != "STAFF" || c.Order.CancelNote.String != "customer left" {
		t.Errorf("cancel = %+v", c.Order)
	}
	onHand, reserved, value := f.balance(f.soda)
	if onHand != 3 || reserved != 0 || value != 3000 { // 5 @ 1000, 2 wasted
		t.Errorf("soda after waste: %d/%d/%d", onHand, reserved, value)
	}
	var wasteDelta int64
	f.must(f.pool.QueryRow(f.ctx, `SELECT value_delta_rial FROM inventory_movements WHERE movement_type = 'WASTE' AND order_id = $1`, d.Order.ID).Scan(&wasteDelta))
	if wasteDelta != -2000 {
		t.Errorf("waste value = %d", wasteDelta)
	}
	for _, it := range c.Items {
		if it.ProductID == f.espresso && (it.WastedQuantity != 1 || it.EstimatedCostRial.Int64 != 30_000) {
			t.Errorf("espresso waste line %+v", it)
		}
		if it.ProductID == f.soda && (it.WastedQuantity != 2 || it.CogsRial.Valid) {
			t.Errorf("soda waste line %+v", it)
		}
	}
	f.invariantsHold()
}

// AC-17: a price change after creation does not touch the order.
func TestPriceChangeAfterCreationKeepsSnapshot(t *testing.T) {
	f := setup(t)
	d := f.mustOrder(item(f.soda, 1, sodaPrice))
	if _, err := f.pool.Exec(f.ctx, `UPDATE products SET price_rial = 70000 WHERE id = $1`, f.soda); err != nil {
		t.Fatal(err)
	}
	f.toReady(d.Order.ID)
	done, err := f.svc.Complete(f.ctx, f.staff, d.Order.ID)
	f.must(err)
	if done.Order.TotalRial != sodaPrice || done.Items[0].UnitPriceRial != sodaPrice {
		t.Errorf("snapshot changed: %+v", done)
	}
	// New orders must confirm the new price.
	var pc orders.PriceChangedError
	if _, _, err := f.order(f.svc, newKey(), item(f.soda, 1, sodaPrice)); !errors.As(err, &pc) {
		t.Errorf("stale price accepted: %v", err)
	}
	if _, _, err := f.order(f.svc, newKey(), item(f.soda, 1, 70000)); err != nil {
		t.Errorf("confirmed price rejected: %v", err)
	}
}

func TestListOrders(t *testing.T) {
	f := setup(t)
	a := f.mustOrder(item(f.soda, 1, sodaPrice))
	b := f.mustOrder(item(f.espresso, 1, espressoPrice))
	_, err := f.svc.Accept(f.ctx, f.staff, b.Order.ID)
	f.must(err)
	list, err := f.svc.List(f.ctx, store.ListOrdersParams{Statuses: []string{"PENDING"}, RowLimit: 10})
	f.must(err)
	if len(list) != 1 || list[0].Order.ID != a.Order.ID || len(list[0].Items) != 1 {
		t.Errorf("pending list = %+v", list)
	}
	all, err := f.svc.List(f.ctx, store.ListOrdersParams{RowLimit: 10})
	f.must(err)
	if len(all) != 2 || all[0].Order.ID != b.Order.ID {
		t.Errorf("all = %d, newest first", len(all))
	}
}

// Review finding: the kill switch only changes when its audit row commits.
func TestOrderingSwitchUnchangedWhenAuditFails(t *testing.T) {
	f := setup(t)
	cancelled, cancel := context.WithCancel(f.ctx)
	cancel()
	if err := f.svc.SetOrderingOpen(cancelled, f.staff, false); err == nil {
		t.Fatal("expected an error with a cancelled context")
	}
	if !f.svc.SwitchOpen() {
		t.Fatal("switch closed although the change was not recorded")
	}
	f.must(f.svc.SetOrderingOpen(f.ctx, f.staff, false))
	if f.svc.SwitchOpen() {
		t.Fatal("switch did not close")
	}
	if err := f.svc.SetOrderingOpen(cancelled, f.staff, true); err == nil || f.svc.SwitchOpen() {
		t.Fatal("a failed reopen must leave ordering closed")
	}
	var n int
	f.must(f.pool.QueryRow(f.ctx, `SELECT count(*) FROM audit_logs WHERE action = 'ordering.switch'`).Scan(&n))
	if n != 1 {
		t.Errorf("switch audit rows = %d, want 1", n)
	}
}
