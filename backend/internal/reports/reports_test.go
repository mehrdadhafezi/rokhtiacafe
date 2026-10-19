package reports_test

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/audit"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/check"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/config"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/database"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/expenses"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/inventory"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/migrate"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/orders"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/payments"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/reports"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/testdb"
)

const (
	sodaPrice     = 50_000
	espressoPrice = 90_000
	espressoEst   = 30_000
)

type fx struct {
	t              *testing.T
	ctx            context.Context
	pool           *pgxpool.Pool
	tehran         *time.Location
	orders         *orders.Service
	pay            *payments.Service
	inv            *inventory.Service
	exp            *expenses.Service
	rep            *reports.Service
	staff          audit.Actor
	soda, espresso int64
	n              byte
	today          string
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
	f := &fx{t: t, ctx: ctx, pool: pool, tehran: tehran,
		orders: orders.New(pool, orders.Config{PendingTTL: 20 * time.Minute, MaxPending: 1000, MaxPendingPerClient: 1000, Hours: hours, Location: tehran, Secret: secret}),
		pay:    &payments.Service{Pool: pool}, inv: &inventory.Service{Pool: pool, Location: tehran},
		exp: &expenses.Service{Pool: pool, Location: tehran}, rep: &reports.Service{Pool: pool, Location: tehran},
		today: time.Now().In(tehran).Format(time.DateOnly)}
	var uid int64
	f.must(pool.QueryRow(ctx, `INSERT INTO users (username, display_name, password_hash, role) VALUES ('admin','A','x','ADMIN') RETURNING id`).Scan(&uid))
	f.staff = audit.UserActor(uid, netip.MustParseAddr("127.0.0.1"))
	f.must(pool.QueryRow(ctx, `INSERT INTO categories (name) VALUES ('c') RETURNING id`).Scan(new(int64)))
	f.must(pool.QueryRow(ctx, `INSERT INTO products (category_id, name, price_rial, inventory_mode) VALUES (1, 'Soda', $1, 'TRACKED') RETURNING id`, sodaPrice).Scan(&f.soda))
	f.must(pool.QueryRow(ctx, `INSERT INTO products (category_id, name, price_rial, inventory_mode, estimated_unit_cost_rial) VALUES (1, 'Espresso', $1, 'UNTRACKED', $2) RETURNING id`, espressoPrice, espressoEst).Scan(&f.espresso))
	_, err = pool.Exec(ctx, `INSERT INTO inventory_balances (product_id) VALUES ($1)`, f.soda)
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

func (f *fx) order(status string, items ...orders.Item) int64 {
	f.t.Helper()
	f.n++
	d, _, err := f.orders.Create(f.ctx, orders.CreateInput{Key: newKey(), ClientIP: netip.AddrFrom4([4]byte{10, 0, 0, f.n}), Items: items})
	f.must(err)
	steps := map[string][]func(context.Context, audit.Actor, int64) (orders.Detail, error){
		"PREPARING": {f.orders.Accept}, "READY": {f.orders.Accept, f.orders.Ready},
		"COMPLETED": {f.orders.Accept, f.orders.Ready, f.orders.Complete},
	}
	for _, s := range steps[status] {
		_, err := s(f.ctx, f.staff, d.Order.ID)
		f.must(err)
	}
	return d.Order.ID
}

func (f *fx) pays(orderID int64, method string, amount int64) store.Payment {
	f.t.Helper()
	p, _, _, err := f.pay.Record(f.ctx, f.staff, orderID, newKey(), payments.RecordInput{Method: method, AmountRial: amount})
	f.must(err)
	return p
}

func (f *fx) sodaItem(q int32) orders.Item {
	return orders.Item{ProductID: f.soda, Quantity: q, ExpectedUnitPriceRial: sodaPrice}
}
func (f *fx) espressoItem(q int32) orders.Item {
	return orders.Item{ProductID: f.espresso, Quantity: q, ExpectedUnitPriceRial: espressoPrice}
}

func (f *fx) todayRange() reports.Range {
	r, err := reports.ParseRange(f.today, f.today)
	f.must(err)
	return r
}

func (f *fx) invariantsHold() {
	f.t.Helper()
	v, err := check.Run(f.ctx, store.New(f.pool))
	f.must(err)
	if len(v) != 0 {
		f.t.Fatalf("invariant violations: %v", v)
	}
}

// AC-28, AC-29, R-F9-1/3/4/5: a hand-computed fixture.
func TestSummaryMatchesHandComputedFixture(t *testing.T) {
	f := setup(t)
	cost := int64(1000)
	_, _, err := f.inv.Adjust(f.ctx, f.staff, newKey(), f.soda, inventory.AdjustInput{Direction: "IN", Quantity: 10, UnitCostRial: &cost,
		ReasonCode: inventory.ReasonOpeningBalance, Note: "opening count"}) // no P&L effect
	f.must(err)
	_, _, err = f.inv.Purchase(f.ctx, f.staff, newKey(), inventory.PurchaseInput{ProductID: f.soda, Quantity: 10, UnitCostRial: 1000, PurchasedOn: f.today})
	f.must(err) // purchases 10 000, information only

	// A: completed, unpaid → revenue 100 000, COGS 2 000, receivable 100 000.
	f.order("COMPLETED", f.sodaItem(2))
	// B: completed, paid by card → revenue 90 000, COGS 0, estimated cost 30 000.
	b := f.order("COMPLETED", f.espressoItem(1))
	f.pays(b, payments.MethodCardTerminal, 90_000)
	// C: completed, paid cash, refunded → gross 180 000, sales refund 180 000.
	c := f.order("COMPLETED", f.espressoItem(2))
	f.pays(c, payments.MethodCash, 180_000)
	_, _, err = f.pay.Refund(f.ctx, f.staff, c, "complaint")
	f.must(err)
	// D: prepaid cash, cancelled with one soda and one espresso wasted → prepayment refund 140 000,
	//    waste loss 1 000 (soda at average cost), estimated waste 30 000.
	d := f.order("READY", f.sodaItem(1), f.espressoItem(1))
	f.pays(d, payments.MethodCash, 140_000)
	_, err = f.orders.Cancel(f.ctx, f.staff, d, orders.CancelInput{AllowRefund: true,
		Waste: []orders.WasteLine{{ProductID: f.soda, Quantity: 1}, {ProductID: f.espresso, Quantity: 1}}})
	f.must(err)
	// Adjustments: OUT 1 damaged (loss 1 000), IN 1 count correction at 1 000 (gain 1 000).
	_, _, err = f.inv.Adjust(f.ctx, f.staff, newKey(), f.soda, inventory.AdjustInput{Direction: "OUT", Quantity: 1, ReasonCode: inventory.ReasonDamaged, Note: "broken"})
	f.must(err)
	_, _, err = f.inv.Adjust(f.ctx, f.staff, newKey(), f.soda, inventory.AdjustInput{Direction: "IN", Quantity: 1, UnitCostRial: &cost, ReasonCode: inventory.ReasonCountCorrection, Note: "found"})
	f.must(err)
	// Expenses: rent 500 000 counted; 100 000 voided and excluded.
	_, _, err = f.exp.Create(f.ctx, f.staff, newKey(), expenses.Input{Category: "RENT", AmountRial: 500_000, ExpenseDate: f.today, Description: "rent"})
	f.must(err)
	wrong, _, err := f.exp.Create(f.ctx, f.staff, newKey(), expenses.Input{Category: "OTHER", AmountRial: 100_000, ExpenseDate: f.today, Description: "typo"})
	f.must(err)
	_, err = f.exp.Void(f.ctx, f.staff, wrong.ID, "duplicate")
	f.must(err)

	s, err := f.rep.Summary(f.ctx, f.todayRange())
	f.must(err)
	tot := s.Totals
	want := map[string][2]int64{
		"gross_sales":        {tot.GrossSalesRial, 370_000},
		"sales_refunds":      {tot.SalesRefundsRial, 180_000},
		"revenue":            {tot.RevenueRial, 190_000},
		"orders":             {tot.Orders, 3},
		"average_order":      {tot.AverageOrderValueRial, 123_333},
		"cogs":               {tot.COGSRial, 2_000},
		"gross_profit":       {tot.GrossProfitRial, 188_000},
		"inventory_losses":   {tot.InventoryLossesRial, 2_000},
		"inventory_gains":    {tot.InventoryGainsRial, 1_000},
		"expenses":           {tot.ExpensesRial, 500_000},
		"operating_result":   {tot.OperatingResultRial, -313_000},
		"purchases":          {tot.PurchasesRial, 10_000},
		"prepayment_refunds": {tot.PrepaymentRefundsRial, 140_000},
		"payments_cash":      {tot.PaymentsReceived["CASH"], 320_000},
		"payments_card":      {tot.PaymentsReceived["CARD_TERMINAL"], 90_000},
		"refunds_cash":       {tot.RefundsRecorded["CASH"], 320_000},
		"collected_cash":     {tot.Collected["CASH"], 0},
		"collected_card":     {tot.Collected["CARD_TERMINAL"], 90_000},
		"estimated_waste":    {tot.EstimatedWasteRial, 30_000},
		"receivable_orders":  {s.Receivables.Orders, 1},
		"receivable_amount":  {s.Receivables.AmountRial, 100_000},
		"rent":               {s.ExpensesByCategory["RENT"], 500_000},
		"other":              {s.ExpensesByCategory["OTHER"], 0},
	}
	for name, v := range want {
		if v[0] != v[1] {
			t.Errorf("%s = %d, want %d", name, v[0], v[1])
		}
	}
	if len(s.Daily) != 1 || s.Daily[0].Date != f.today || s.Daily[0].RevenueRial != 190_000 {
		t.Errorf("daily = %+v", s.Daily)
	}

	// R-F9-5: for completed orders, net revenue = net cash from completed orders + receivables.
	var completedCash int64
	f.must(f.pool.QueryRow(f.ctx, `SELECT COALESCE(sum(CASE WHEN p.kind = 'PAYMENT' THEN p.amount_rial ELSE -p.amount_rial END), 0)
		FROM payments p JOIN orders o ON o.id = p.order_id WHERE o.status = 'COMPLETED' AND p.voided_at IS NULL`).Scan(&completedCash))
	if tot.RevenueRial != completedCash+s.Receivables.AmountRial {
		t.Errorf("revenue %d != completed cash %d + receivables %d", tot.RevenueRial, completedCash, s.Receivables.AmountRial)
	}

	// Product report: actual COGS for TRACKED, estimated margin only for UNTRACKED (F8).
	lines, err := f.rep.Products(f.ctx, f.todayRange())
	f.must(err)
	byID := map[int64]reports.ProductLine{}
	for _, l := range lines {
		byID[l.ProductID] = l
	}
	if s := byID[f.soda]; s.Quantity != 2 || s.RevenueRial != 100_000 || s.COGSRial != 2_000 || s.GrossProfitRial != 98_000 || s.EstimatedCostRial != nil {
		t.Errorf("soda line = %+v", s)
	}
	if e := byID[f.espresso]; e.Quantity != 3 || e.RevenueRial != 270_000 || e.COGSRial != 0 || e.EstimatedCostRial == nil ||
		*e.EstimatedCostRial != 90_000 || *e.EstimatedMargin != 180_000 || e.LinesNoEstimate != 0 {
		t.Errorf("espresso line = %+v", e)
	}
	f.invariantsHold()
}

// R-F9-2 / R-F9-6: payment timing and voided payments.
func TestReceivablesAndVoidedPayments(t *testing.T) {
	f := setup(t)
	o := f.order("COMPLETED", f.espressoItem(2)) // 180 000
	p := f.pays(o, payments.MethodCash, 100_000)
	s, err := f.rep.Summary(f.ctx, f.todayRange())
	f.must(err)
	if s.Receivables.AmountRial != 80_000 || s.Totals.Collected["CASH"] != 100_000 {
		t.Fatalf("partial: receivable %d collected %d", s.Receivables.AmountRial, s.Totals.Collected["CASH"])
	}
	_, _, err = f.pay.Void(f.ctx, f.staff, p.ID, "wrong order")
	f.must(err)
	s, err = f.rep.Summary(f.ctx, f.todayRange())
	f.must(err)
	if s.Receivables.AmountRial != 180_000 || s.Totals.Collected["CASH"] != 0 || s.Totals.RevenueRial != 180_000 {
		t.Errorf("after void: receivable %d collected %d revenue %d", s.Receivables.AmountRial, s.Totals.Collected["CASH"], s.Totals.RevenueRial)
	}
	list, err := f.rep.Receivables(f.ctx)
	f.must(err)
	if len(list) != 1 || list[0].OrderID != o || list[0].OutstandingRial != 180_000 {
		t.Errorf("receivables = %+v", list)
	}
	f.pays(o, payments.MethodCardTerminal, 180_000)
	if list, _ := f.rep.Receivables(f.ctx); len(list) != 0 {
		t.Errorf("fully paid order still listed: %+v", list)
	}
}

// AC-30: day boundaries follow Asia/Tehran, not UTC.
func TestBusinessDayBoundaries(t *testing.T) {
	f := setup(t)
	at := func(day string, hh, mm int) time.Time {
		d, _ := time.Parse(time.DateOnly, day)
		return time.Date(d.Year(), d.Month(), d.Day(), hh, mm, 0, 0, f.tehran)
	}
	// 23:50 Tehran on the 18th is 20:20 UTC the same day; 00:10 Tehran on the 19th is 20:40 UTC on the 18th.
	insert := func(n int, completed time.Time, total int64) {
		created := completed.Add(-10 * time.Minute)
		var token, req, client [32]byte
		_, _ = rand.Read(token[:])
		_, _ = rand.Read(req[:])
		_, _ = rand.Read(client[:])
		var id int64
		f.must(f.pool.QueryRow(f.ctx, `INSERT INTO orders (business_date, display_number, tracking_token_hash, idempotency_key,
			request_hash, client_key, total_rial, expires_at, status, created_at, accepted_at, ready_at, completed_at)
			VALUES ($1, $2, $3, gen_random_uuid(), $4, $5, $6, $7, 'COMPLETED', $8, $8, $8, $9) RETURNING id`,
			pgtype.Date{Time: time.Date(created.In(f.tehran).Year(), created.In(f.tehran).Month(), created.In(f.tehran).Day(), 0, 0, 0, 0, time.UTC), Valid: true},
			n, token[:], req[:], client[:], total, created.Add(20*time.Minute), created, completed).Scan(&id))
		_, err := f.pool.Exec(f.ctx, `INSERT INTO order_items (order_id, product_id, product_name, inventory_mode, unit_price_rial, quantity, line_total_rial, cogs_rial)
			VALUES ($1, $2, 'Espresso', 'UNTRACKED', $3, 1, $3, 0)`, id, f.espresso, total)
		f.must(err)
		_, err = f.pool.Exec(f.ctx, `INSERT INTO payments (order_id, kind, method, amount_rial, idempotency_key, created_by, created_at)
			VALUES ($1, 'PAYMENT', 'CASH', $2, gen_random_uuid(), $3, $4)`, id, total, f.staff.UserID, completed)
		f.must(err)
		_, err = f.pool.Exec(f.ctx, `UPDATE orders SET paid_rial = $2 WHERE id = $1`, id, total)
		f.must(err)
	}
	insert(1, at("2026-10-18", 23, 50), 10_000)
	insert(2, at("2026-10-19", 0, 10), 20_000)
	insert(3, at("2026-10-19", 23, 59), 40_000)
	insert(4, at("2026-10-20", 0, 0), 80_000)

	r, _ := reports.ParseRange("2026-10-19", "2026-10-19")
	s, err := f.rep.Summary(f.ctx, r)
	f.must(err)
	if s.Totals.GrossSalesRial != 60_000 || s.Totals.Orders != 2 || s.Totals.Collected["CASH"] != 60_000 {
		t.Errorf("2026-10-19 Tehran: gross %d orders %d cash %d, want 60000/2/60000", s.Totals.GrossSalesRial, s.Totals.Orders, s.Totals.Collected["CASH"])
	}
	r, _ = reports.ParseRange("2026-10-18", "2026-10-20")
	s, err = f.rep.Summary(f.ctx, r)
	f.must(err)
	got := map[string]int64{}
	for _, d := range s.Daily {
		got[d.Date] = d.GrossSalesRial
	}
	if len(s.Daily) != 3 || got["2026-10-18"] != 10_000 || got["2026-10-19"] != 60_000 || got["2026-10-20"] != 80_000 || s.Totals.GrossSalesRial != 150_000 {
		t.Errorf("daily = %v total %d", got, s.Totals.GrossSalesRial)
	}
	f.invariantsHold()
}

func TestRangeValidationAndEmptyDays(t *testing.T) {
	f := setup(t)
	for _, c := range [][2]string{{"", ""}, {"2026-10-20", "2026-10-19"}, {"2026-01-01", "2027-01-02"}, {"2026-13-01", "2026-13-02"}} {
		if _, err := reports.ParseRange(c[0], c[1]); !errors.Is(err, reports.ErrRange) {
			t.Errorf("range %v accepted", c)
		}
	}
	r, err := reports.ParseRange("2026-03-01", "2026-03-31") // a full Gregorian month with no data
	f.must(err)
	s, err := f.rep.Summary(f.ctx, r)
	f.must(err)
	if len(s.Daily) != 31 || s.Totals.RevenueRial != 0 || s.Totals.Collected["CASH"] != 0 {
		t.Errorf("empty month: %d days, revenue %d", len(s.Daily), s.Totals.RevenueRial)
	}
	if _, err := reports.ParseRange("2025-10-21", "2026-10-20"); err != nil {
		t.Errorf("366-day range rejected: %v", err)
	}
}

func TestExpenses(t *testing.T) {
	f := setup(t)
	key := newKey()
	in := expenses.Input{Category: "INGREDIENTS", AmountRial: 250_000, ExpenseDate: f.today, Description: " milk and beans "}
	e1, replay, err := f.exp.Create(f.ctx, f.staff, key, in)
	f.must(err)
	if replay || e1.Description != "milk and beans" {
		t.Errorf("create = %+v replay %v", e1, replay)
	}
	e2, replay, err := f.exp.Create(f.ctx, f.staff, key, in)
	f.must(err)
	if !replay || e2.ID != e1.ID {
		t.Error("replay failed")
	}
	in.AmountRial = 1
	if _, _, err := f.exp.Create(f.ctx, f.staff, key, in); !errors.Is(err, expenses.ErrIdempotencyReused) {
		t.Errorf("reused key: %v", err)
	}
	var bad expenses.ValidationError
	tomorrow := time.Now().In(f.tehran).AddDate(0, 0, 1).Format(time.DateOnly)
	for _, in := range []expenses.Input{
		{Category: "FUN", AmountRial: 1, ExpenseDate: f.today, Description: "x"},
		{Category: "RENT", AmountRial: 0, ExpenseDate: f.today, Description: "x"},
		{Category: "RENT", AmountRial: 1, ExpenseDate: tomorrow, Description: "x"},
		{Category: "RENT", AmountRial: 1, ExpenseDate: "19/10/2026", Description: "x"},
		{Category: "RENT", AmountRial: 1, ExpenseDate: f.today, Description: "  "},
	} {
		if _, _, err := f.exp.Create(f.ctx, f.staff, newKey(), in); !errors.As(err, &bad) {
			t.Errorf("%+v accepted: %v", in, err)
		}
	}
	v, err := f.exp.Void(f.ctx, f.staff, e1.ID, "entered twice")
	f.must(err)
	again, err := f.exp.Void(f.ctx, f.staff, e1.ID, "again")
	f.must(err)
	if !v.VoidedAt.Valid || again.VoidReason.String != "entered twice" {
		t.Errorf("void = %+v / %+v", v, again)
	}
	if _, err := f.exp.Void(f.ctx, f.staff, 99999, "x"); !errors.Is(err, expenses.ErrNotFound) {
		t.Errorf("unknown expense: %v", err)
	}
	list, err := f.exp.List(f.ctx, store.ListExpensesParams{RowLimit: 10})
	f.must(err)
	if len(list) != 1 {
		t.Errorf("list = %d", len(list))
	}
}

// Review finding: a drink sold before and after its estimate was set gets a margin over the
// estimated lines only, never revenue of unestimated lines minus partial costs.
func TestEstimatedMarginCoversOnlyEstimatedLines(t *testing.T) {
	f := setup(t)
	_, err := f.pool.Exec(f.ctx, `UPDATE products SET estimated_unit_cost_rial = NULL WHERE id = $1`, f.espresso)
	f.must(err)
	f.order("COMPLETED", f.espressoItem(2)) // 180 000, no estimate
	_, err = f.pool.Exec(f.ctx, `UPDATE products SET estimated_unit_cost_rial = 30000 WHERE id = $1`, f.espresso)
	f.must(err)
	f.order("COMPLETED", f.espressoItem(1)) // 90 000, estimate 30 000

	lines, err := f.rep.Products(f.ctx, f.todayRange())
	f.must(err)
	if len(lines) != 1 {
		t.Fatalf("lines = %+v", lines)
	}
	l := lines[0]
	if l.RevenueRial != 270_000 || l.EstimatedRevenueRial == nil || *l.EstimatedRevenueRial != 90_000 ||
		*l.EstimatedCostRial != 30_000 || *l.EstimatedMargin != 60_000 || l.LinesNoEstimate != 1 {
		t.Errorf("line = %+v (estimated revenue %v, cost %v, margin %v)", l, deref(l.EstimatedRevenueRial), deref(l.EstimatedCostRial), deref(l.EstimatedMargin))
	}
}

func deref(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// Review finding: InSnapshot reads one committed state for the whole transaction.
func TestSnapshotIgnoresConcurrentCommits(t *testing.T) {
	f := setup(t)
	o := f.order("COMPLETED", f.espressoItem(1)) // 90 000 receivable
	var before, after int64
	err := store.InSnapshot(f.ctx, f.pool, func(q *store.Queries) error {
		r, err := q.ReportReceivablesTotal(f.ctx)
		if err != nil {
			return err
		}
		before = r.Amount
		f.pays(o, payments.MethodCash, 90_000) // commits on another connection mid-snapshot
		r, err = q.ReportReceivablesTotal(f.ctx)
		after = r.Amount
		return err
	})
	f.must(err)
	if before != 90_000 || after != 90_000 {
		t.Errorf("snapshot saw %d then %d, want 90000 both times", before, after)
	}
	s, err := f.rep.Summary(f.ctx, f.todayRange())
	f.must(err)
	if s.Receivables.AmountRial != 0 || s.Totals.Collected["CASH"] != 90_000 {
		t.Errorf("after commit: receivable %d cash %d", s.Receivables.AmountRial, s.Totals.Collected["CASH"])
	}
}
