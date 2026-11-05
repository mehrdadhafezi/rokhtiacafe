package api_test

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/api"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/testdb"
)

// C6 release gate: a sale completed on Tehran business day D and refunded on D+1.
//
// Every business timestamp (completed_at, payments.created_at, …) comes from PostgreSQL's now(),
// so the test runs against a disposable PostgreSQL whose system clock it controls: libfaketime
// re-reads CLOCK_FAKETIME_FILE on every call (deploy/test/clock/run.sh starts that server). The Go
// clocks of the server under test (orders, staff and customer sessions) follow the same offset.
// Nothing bypasses immutability: no row is updated, no trigger disabled, no timestamp rewritten.
//
// Needs CLOCK_TEST_DATABASE_ADMIN_URL, CLOCK_MIGRATION_DATABASE_URL, CLOCK_DATABASE_URL and
// CLOCK_FAKETIME_FILE; skipped without them unless REQUIRE_CLOCK_DB_TESTS=1.

type testClock struct {
	t      *testing.T
	e      *env
	file   string
	offset atomic.Int64 // nanoseconds added to the real time
}

func (c *testClock) now() time.Time { return time.Now().Add(time.Duration(c.offset.Load())) }

// set moves the database and the server clocks to target and waits until PostgreSQL agrees.
func (c *testClock) set(target time.Time) {
	c.t.Helper()
	secs := int64(time.Until(target).Round(time.Second) / time.Second)
	if err := os.WriteFile(c.file, []byte(fmt.Sprintf("%+d\n", secs)), 0o644); err != nil {
		c.t.Fatal(err)
	}
	c.offset.Store(secs * int64(time.Second))
	deadline := time.Now().Add(15 * time.Second)
	for {
		var db time.Time
		if err := c.e.pool.QueryRow(c.e.ctx, "SELECT now()").Scan(&db); err != nil {
			c.t.Fatal(err)
		}
		if d := db.Sub(c.now()); d > -3*time.Second && d < 3*time.Second {
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("the database clock (%s) does not follow %s (want %s): is libfaketime loaded?", db, c.file, c.now())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestCrossDayRefundTehran(t *testing.T) {
	file := os.Getenv("CLOCK_FAKETIME_FILE")
	if file == "" {
		if os.Getenv("REQUIRE_CLOCK_DB_TESTS") == "1" {
			t.Fatal("REQUIRE_CLOCK_DB_TESTS=1 but CLOCK_FAKETIME_FILE is not set")
		}
		t.Skip("CLOCK_FAKETIME_FILE not set; run deploy/test/clock/run.sh")
	}
	db := testdb.NewFrom(t, "CLOCK_")
	clock := &testClock{t: t, file: file}
	t.Cleanup(func() { _ = os.WriteFile(file, []byte("+0\n"), 0o644) })
	e := newEnvOn(t, db, func(d *api.Deps) {
		d.Orders.SetClock(clock.now)
		d.Auth.Sessions.SetClock(clock.now)
	})
	e.api.SetCustomerClock(clock.now)
	clock.e = e

	tehran, _ := time.LoadLocation("Asia/Tehran")
	today := time.Now().In(tehran)
	day := time.Date(today.Year(), today.Month(), today.Day()-3, 0, 0, 0, 0, tehran) // D
	next := day.AddDate(0, 0, 1)                                                     // D+1, 00:00 Tehran
	D, D1 := day.Format(time.DateOnly), next.Format(time.DateOnly)

	// ---- business day D, 23:40 Tehran ----
	clock.set(next.Add(-20 * time.Minute))
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, _, _ := seedCatalog(t, admin)
	a, aID := newCustomer(t, e, "09121111111", "آرش")
	order := func(c *client, qty int) float64 {
		t.Helper()
		resp, body := placeOrder(c, newUUID(), line(espresso, qty, 950000))
		expect(t, resp, body, http.StatusCreated, "")
		return orderIDByNumber(t, admin, body["order_number"].(float64))
	}
	sold := order(a, 2) // 1 900 000, paid in cash, completed on D, refunded on D+1
	orderFlow(t, admin, sold, "accept", "pay:CASH:1900000", "ready", "complete")
	credit := order(a, 1) // 950 000, completed unpaid on D, paid by card on D+1
	orderFlow(t, admin, credit, "accept", "ready", "complete")
	guest := order(e.newClient(), 1) // guest, 950 000, card, D
	orderFlow(t, admin, guest, "accept", "pay:CARD_TERMINAL:950000", "ready", "complete")

	// ---- business day D+1, 00:20 Tehran ----
	clock.set(next.Add(20 * time.Minute))
	orderFlow(t, admin, sold, "refund")
	orderFlow(t, admin, credit, "pay:CARD_TERMINAL:950000")

	// The rows carry the real (database) times on the expected Tehran dates.
	tehranDate := func(sql string, id float64) string {
		t.Helper()
		var d time.Time
		if err := e.pool.QueryRow(e.ctx, sql, int64(id)).Scan(&d); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return d.Format(time.DateOnly)
	}
	for name, pair := range map[string][2]string{
		"sale business_date":   {tehranDate("SELECT business_date FROM orders WHERE id = $1", sold), D},
		"sale completed_at":    {tehranDate("SELECT (completed_at AT TIME ZONE 'Asia/Tehran')::date FROM orders WHERE id = $1", sold), D},
		"sale payment":         {tehranDate("SELECT (min(created_at) AT TIME ZONE 'Asia/Tehran')::date FROM payments WHERE order_id = $1 AND kind = 'PAYMENT'", sold), D},
		"refund row":           {tehranDate("SELECT (created_at AT TIME ZONE 'Asia/Tehran')::date FROM payments WHERE order_id = $1 AND kind = 'REFUND'", sold), D1},
		"late payment":         {tehranDate("SELECT (created_at AT TIME ZONE 'Asia/Tehran')::date FROM payments WHERE order_id = $1", credit), D1},
		"credit sale complete": {tehranDate("SELECT (completed_at AT TIME ZONE 'Asia/Tehran')::date FROM orders WHERE id = $1", credit), D},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s on %s, want %s", name, pair[0], pair[1])
		}
	}

	sum := func(m any) float64 {
		x := m.(map[string]any)
		return x["CASH"].(float64) + x["CARD_TERMINAL"].(float64)
	}
	summary := func(from, to string) (map[string]any, []any) {
		t.Helper()
		resp, body := admin.do(http.MethodGet, "/api/v1/admin/reports/summary?from="+from+"&to="+to, nil)
		expect(t, resp, body, http.StatusOK, "")
		return body["totals"].(map[string]any), body["daily"].([]any)
	}
	type figures struct{ orders, gross, salesRefunds, revenue, received, refunds, collected float64 }
	fromSummary := func(f map[string]any) figures {
		return figures{f["orders"].(float64), f["gross_sales_rial"].(float64), f["sales_refunds_rial"].(float64),
			f["revenue_rial"].(float64), sum(f["payments_received"]), sum(f["refunds_recorded"]), sum(f["collected"])}
	}
	customers := func(from, to string) map[string]any {
		t.Helper()
		resp, body := admin.do(http.MethodGet, "/api/v1/admin/reports/customers?from="+from+"&to="+to, nil)
		expect(t, resp, body, http.StatusOK, "")
		return body["totals"].(map[string]any)
	}
	fromCustomers := func(f map[string]any) figures {
		return figures{f["completed_orders"].(float64), f["gross_sales_rial"].(float64), f["sales_refunds_rial"].(float64),
			f["revenue_rial"].(float64), f["payments_received_rial"].(float64), f["refunds_recorded_rial"].(float64),
			f["net_collected_rial"].(float64)}
	}

	// Revenue is recognised at completion (D); the refund reduces revenue on the day it is recorded
	// (D+1); cash is bucketed by the day each payment or refund row was recorded.
	wantAll := map[string]figures{ // whole café
		D:             {orders: 3, gross: 3800000, salesRefunds: 0, revenue: 3800000, received: 2850000, refunds: 0, collected: 2850000},
		D1:            {orders: 0, gross: 0, salesRefunds: 1900000, revenue: -1900000, received: 950000, refunds: 1900000, collected: -950000},
		D + ".." + D1: {orders: 3, gross: 3800000, salesRefunds: 1900000, revenue: 1900000, received: 3800000, refunds: 1900000, collected: 1900000},
	}
	wantCustomer := map[string]figures{ // customer A only (the guest's 950 000 sale and card payment are not attributed)
		D:             {orders: 2, gross: 2850000, salesRefunds: 0, revenue: 2850000, received: 1900000, refunds: 0, collected: 1900000},
		D1:            {orders: 0, gross: 0, salesRefunds: 1900000, revenue: -1900000, received: 950000, refunds: 1900000, collected: -950000},
		D + ".." + D1: {orders: 2, gross: 2850000, salesRefunds: 1900000, revenue: 950000, received: 2850000, refunds: 1900000, collected: 950000},
	}
	for rng, want := range wantAll {
		from, to, _ := strings.Cut(rng, "..")
		if to == "" {
			to = from
		}
		st, daily := summary(from, to)
		if got := fromSummary(st); got != want {
			t.Errorf("summary %s:\n got  %+v\n want %+v", rng, got, want)
		}
		if got := fromCustomers(customers(from, to)); got != wantCustomer[rng] {
			t.Errorf("customer report %s:\n got  %+v\n want %+v", rng, got, wantCustomer[rng])
		}
		// The daily entries of the combined range equal the single-day reports.
		if from != to {
			if len(daily) != 2 {
				t.Fatalf("daily = %v", daily)
			}
			for i, d := range []string{D, D1} {
				entry := daily[i].(map[string]any)
				if entry["date"] != d || fromSummary(entry) != wantAll[d] {
					t.Errorf("daily %s = %+v, want %+v", entry["date"], fromSummary(entry), wantAll[d])
				}
			}
		}
		if p := st["prepayment_refunds_rial"].(float64); p != 0 {
			t.Errorf("summary %s: a refund of a completed sale counted as a prepayment refund (%v)", rng, p)
		}
	}
	// Point in time: nothing is owed any more (the credit sale was paid on D+1, the sale refunded).
	_, body := admin.do(http.MethodGet, "/api/v1/admin/reports/summary?from="+D+"&to="+D, nil)
	if r := body["receivables"].(map[string]any)["amount_rial"].(float64); r != 0 {
		t.Errorf("receivables = %v", r)
	}
	// All-time customer totals agree with the combined range.
	_, body = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/admin/customers/%d", aID), nil)
	tot := body["totals"].(map[string]any)
	if tot["payments_received_rial"] != 2850000.0 || tot["refunds_recorded_rial"] != 1900000.0 ||
		tot["net_collected_rial"] != 950000.0 || tot["completed_revenue_rial"] != 950000.0 {
		t.Errorf("customer totals = %v", tot)
	}
	// The customer still sees both orders, the refunded one as refunded.
	_, hist := a.do(http.MethodGet, "/api/v1/customer/orders", nil)
	if n := len(hist["items"].([]any)); n != 2 {
		t.Errorf("customer history = %v", hist)
	}
}
