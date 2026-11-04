package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/orders"
)

// C3: authenticated customer ordering and private history.

func placeOrderWith(c *client, key string, opts []reqOpt, items ...map[string]any) (*http.Response, map[string]any) {
	all := append([]reqOpt{withKey(key), withHeader("X-Client-Id", "11111111-2222-4333-8444-555555555555")}, opts...)
	return c.do(http.MethodPost, "/api/v1/public/orders",
		map[string]any{"customer_label": "سارا", "customer_note": "بدون شکر", "items": items}, all...)
}

func mustUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		t.Fatal(err)
	}
	return u
}

// newCustomer registers an account and returns its browser and id.
func newCustomer(t *testing.T, e *env, phone, first string) (*client, int64) {
	t.Helper()
	c := e.newClient()
	resp, body := c.do(http.MethodPost, customerPath+"register", map[string]string{
		"first_name": first, "last_name": "مشتری", "phone": phone, "password": "secure-cafe-2026"})
	expect(t, resp, body, http.StatusCreated, "")
	c.csrf = body["csrf_token"].(string)
	return c, int64(body["customer"].(map[string]any)["id"].(float64))
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func orderIDByNumber(t *testing.T, admin *client, number float64) float64 {
	t.Helper()
	_, list := admin.do(http.MethodGet, "/api/v1/admin/orders", nil)
	for _, it := range list["items"].([]any) {
		o := it.(map[string]any)
		if o["order_number"] == number {
			return o["id"].(float64)
		}
	}
	t.Fatalf("order %v not found", number)
	return 0
}

func TestCustomerOrderIsLinkedAndPrivate(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, soda, _ := seedCatalog(t, admin)
	stockSoda(t, admin, soda)
	a, aID := newCustomer(t, e, "09121111111", "آرش")
	b, _ := newCustomer(t, e, "09122222222", "بهار")

	resp, body := placeOrder(a, newUUID(), line(soda, 2, 450000), line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusCreated, "")
	if len(body["tracking_token"].(string)) != 43 {
		t.Fatalf("authenticated order lost its tracking token: %v", body)
	}
	orderID := orderIDByNumber(t, admin, body["order_number"].(float64))
	if n := e.count("SELECT count(*) FROM customer_order_links WHERE order_id=$1 AND customer_id=$2", int64(orderID), aID); n != 1 {
		t.Fatalf("link rows = %d", n)
	}
	if n := e.count("SELECT count(*) FROM customer_events WHERE customer_id=$1 AND event_type='ORDER_CREATED' AND order_id=$2", aID, int64(orderID)); n != 1 {
		t.Fatalf("ORDER_CREATED events = %d", n)
	}

	// A guest order next to it stays anonymous.
	resp, body = placeOrder(e.newClient(), newUUID(), line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusCreated, "")
	guestID := orderIDByNumber(t, admin, body["order_number"].(float64))
	if n := e.count("SELECT count(*) FROM customer_order_links WHERE order_id=$1", int64(guestID)); n != 0 {
		t.Fatal("guest order was attributed to an account")
	}

	resp, body = a.do(http.MethodGet, "/api/v1/customer/orders", nil)
	expect(t, resp, body, http.StatusOK, "")
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Error("history must be no-store")
	}
	items := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("A history = %v", body)
	}
	o := items[0].(map[string]any)
	if o["id"].(float64) != orderID || o["total_rial"].(float64) != 1850000 || len(o["items"].([]any)) != 2 || o["status"] != "PENDING" {
		t.Errorf("A order = %v", o)
	}
	raw, _ := json.Marshal(body)
	for _, leak := range []string{"tracking", "cogs", "estimated_cost", "client_key", "request_hash", "accepted_by", "ip"} {
		if strings.Contains(string(raw), `"`+leak) {
			t.Errorf("history exposes %q: %s", leak, raw)
		}
	}

	resp, body = b.do(http.MethodGet, "/api/v1/customer/orders", nil)
	expect(t, resp, body, http.StatusOK, "")
	if len(body["items"].([]any)) != 0 {
		t.Fatalf("B sees A's orders: %v", body)
	}
	// Unowned, guest and missing ids answer the same 404.
	for _, id := range []float64{orderID, guestID, 999999} {
		resp, body = b.do(http.MethodGet, fmt.Sprintf("/api/v1/customer/orders/%v", id), nil)
		expect(t, resp, body, http.StatusNotFound, "NOT_FOUND")
	}
	resp, body = a.do(http.MethodGet, fmt.Sprintf("/api/v1/customer/orders/%v", guestID), nil)
	expect(t, resp, body, http.StatusNotFound, "NOT_FOUND")
	resp, body = a.do(http.MethodGet, fmt.Sprintf("/api/v1/customer/orders/%v", orderID), nil)
	expect(t, resp, body, http.StatusOK, "")
	if body["items"].([]any)[0].(map[string]any)["unit_price_rial"] == nil {
		t.Errorf("detail lacks recorded prices: %v", body)
	}

	// Staff sessions and anonymous browsers get nothing.
	for _, c := range []*client{admin, e.newClient()} {
		resp, body = c.do(http.MethodGet, "/api/v1/customer/orders", nil)
		expect(t, resp, body, http.StatusUnauthorized, "UNAUTHENTICATED")
		resp, body = c.do(http.MethodGet, fmt.Sprintf("/api/v1/customer/orders/%v", orderID), nil)
		expect(t, resp, body, http.StatusUnauthorized, "UNAUTHENTICATED")
	}

	resp, body = a.do(http.MethodGet, "/api/v1/customer/orders?limit=101", nil)
	expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")

	// Activity: curated, own events only, never an IP.
	resp, body = a.do(http.MethodGet, "/api/v1/customer/activity", nil)
	expect(t, resp, body, http.StatusOK, "")
	raw, _ = json.Marshal(body)
	if strings.Contains(string(raw), "127.0.0.1") || strings.Contains(string(raw), `"ip"`) {
		t.Errorf("activity exposes the IP: %s", raw)
	}
	types := []string{}
	for _, it := range body["items"].([]any) {
		types = append(types, it.(map[string]any)["type"].(string))
	}
	if strings.Join(types, ",") != "ORDER_CREATED,REGISTERED" {
		t.Errorf("activity = %v", types)
	}
	ev := body["items"].([]any)[0].(map[string]any)
	if ev["order_id"].(float64) != orderID || ev["order_number"] == nil {
		t.Errorf("order event = %v", ev)
	}
}

func TestCustomerOrderNeedsCSRFAndOrigin(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, _, _ := seedCatalog(t, admin)
	a, _ := newCustomer(t, e, "09121111111", "آرش")

	resp, body := placeOrderWith(a, newUUID(), []reqOpt{noCSRF}, line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusForbidden, "CSRF_FAILED")
	resp, body = placeOrderWith(a, newUUID(), []reqOpt{withCSRF("not-the-token-of-this-session-xxxxx")}, line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusForbidden, "CSRF_FAILED")
	resp, body = placeOrderWith(a, newUUID(), []reqOpt{withOrigin("https://evil.example")}, line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusForbidden, "CSRF_FAILED")
	// A staff CSRF token is not a customer CSRF token.
	resp, body = placeOrderWith(a, newUUID(), []reqOpt{withCSRF(admin.csrf)}, line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusForbidden, "CSRF_FAILED")
	if n := e.count("SELECT count(*) FROM orders"); n != 0 {
		t.Fatalf("a refused request created %d orders", n)
	}
	// A staff browser without a customer cookie orders as a guest, exactly as before.
	resp, body = placeOrder(admin, newUUID(), line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusCreated, "")
	if n := e.count("SELECT count(*) FROM customer_order_links"); n != 0 {
		t.Fatal("staff session attributed an order to a customer")
	}
}

func TestStaleCustomerCookieNeverBecomesAGuestOrder(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, _, _ := seedCatalog(t, admin)
	a, aID := newCustomer(t, e, "09121111111", "آرش")
	if _, err := e.pool.Exec(e.ctx, "UPDATE customer_sessions SET created_at = now() - interval '8 days', expires_at = now() - interval '1 day'"); err != nil {
		t.Fatal(err)
	}
	resp, body := placeOrder(a, newUUID(), line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusUnauthorized, "CUSTOMER_SESSION_EXPIRED")
	if n := e.count("SELECT count(*) FROM orders"); n != 0 {
		t.Fatal("stale session created an order")
	}
	// The cookie was cleared; ordering again is an explicit guest order.
	resp, body = placeOrder(a, newUUID(), line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusCreated, "")
	if n := e.count("SELECT count(*) FROM customer_order_links WHERE customer_id=$1", aID); n != 0 {
		t.Fatal("order attributed through an expired session")
	}

	// A forged cookie value is refused the same way.
	f := e.newClient()
	u, _ := url.Parse(e.srv.URL)
	f.http.Jar.SetCookies(u, []*http.Cookie{{Name: "rokh_customer", Value: strings.Repeat("A", 43), Path: "/"}})
	resp, body = placeOrder(f, newUUID(), line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusUnauthorized, "CUSTOMER_SESSION_EXPIRED")

	// A deactivated account cannot order, through the session check or the in-transaction lock.
	b, bID := newCustomer(t, e, "09122222222", "بهار")
	if _, err := e.pool.Exec(e.ctx, "UPDATE customers SET is_active=false WHERE id=$1", bID); err != nil {
		t.Fatal(err)
	}
	resp, body = placeOrder(b, newUUID(), line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusUnauthorized, "CUSTOMER_SESSION_EXPIRED")
	_, _, err := e.orders.Create(e.ctx, orders.CreateInput{Key: mustUUID(t, newUUID()), CustomerID: bID,
		Items: []orders.Item{{ProductID: int64(espresso), Quantity: 1, ExpectedUnitPriceRial: 950000}}})
	if err != orders.ErrCustomerInactive {
		t.Fatalf("inactive customer order: %v", err)
	}
}

func TestClientCannotChooseTheCustomer(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, _, _ := seedCatalog(t, admin)
	_, aID := newCustomer(t, e, "09121111111", "آرش")
	resp, body := e.newClient().do(http.MethodPost, "/api/v1/public/orders",
		map[string]any{"customer_id": aID, "items": []any{line(espresso, 1, 950000)}},
		withKey(newUUID()), withHeader("X-Client-Id", "11111111-2222-4333-8444-555555555555"))
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		t.Fatalf("body with customer_id accepted: %v", body)
	}
	if n := e.count("SELECT count(*) FROM customer_order_links"); n != 0 {
		t.Fatal("client-supplied customer_id attributed an order")
	}
}

func TestIdempotencyAcrossIdentities(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, soda, _ := seedCatalog(t, admin)
	stockSoda(t, admin, soda)
	a, aID := newCustomer(t, e, "09121111111", "آرش")
	b, _ := newCustomer(t, e, "09122222222", "بهار")
	guest := e.newClient()

	key := newUUID()
	resp, body := placeOrder(a, key, line(soda, 2, 450000))
	expect(t, resp, body, http.StatusCreated, "")
	token := body["tracking_token"]
	resp, body = placeOrder(a, key, line(soda, 2, 450000))
	expect(t, resp, body, http.StatusOK, "")
	if body["tracking_token"] != token {
		t.Error("replay by the owner returned another token")
	}
	for name, c := range map[string]*client{"customer B": b, "guest": guest} {
		resp, body = placeOrder(c, key, line(soda, 2, 450000))
		expect(t, resp, body, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED")
		raw, _ := json.Marshal(body)
		if strings.Contains(string(raw), "tracking_token") || strings.Contains(string(raw), "order_number") {
			t.Errorf("%s replay revealed the order: %s", name, raw)
		}
	}
	resp, body = placeOrder(a, key, line(soda, 1, 450000))
	expect(t, resp, body, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED")

	// A guest's key replayed by a customer neither reveals nor claims the guest order.
	gkey := newUUID()
	resp, body = placeOrder(guest, gkey, line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusCreated, "")
	resp, body = placeOrder(a, gkey, line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED")
	resp, body = placeOrder(guest, gkey, line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusOK, "") // the guest's own retry still replays

	if n := e.count("SELECT count(*) FROM orders"); n != 2 {
		t.Errorf("orders = %d", n)
	}
	if n := e.count("SELECT count(*) FROM customer_order_links WHERE customer_id=$1", aID); n != 1 {
		t.Errorf("A links = %d", n)
	}
	if n := e.count("SELECT count(*) FROM customer_events WHERE event_type='ORDER_CREATED'"); n != 1 {
		t.Errorf("ORDER_CREATED events = %d", n)
	}
	if n := e.count("SELECT reserved FROM inventory_balances WHERE product_id=$1", int64(soda)); n != 2 {
		t.Errorf("soda reserved = %d (replays must not reserve again)", n)
	}
}

func TestConcurrentReplayCreatesOneLinkedOrder(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, _, soda, _ := seedCatalog(t, admin)
	stockSoda(t, admin, soda)
	a, aID := newCustomer(t, e, "09121111111", "آرش")
	key := newUUID()
	const n = 6
	codes := make([]int, n)
	tokens := make([]string, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			resp, body := placeOrder(a, key, line(soda, 1, 450000))
			codes[i] = resp.StatusCode
			tokens[i], _ = body["tracking_token"].(string)
		}(i)
	}
	close(start)
	wg.Wait()
	created := 0
	for i, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusOK:
		default:
			t.Errorf("request %d: %d", i, c)
		}
		if tokens[i] != tokens[0] {
			t.Errorf("token %d differs", i)
		}
	}
	if created != 1 {
		t.Errorf("created = %d", created)
	}
	if c := e.count("SELECT count(*) FROM customer_order_links WHERE customer_id=$1", aID); c != 1 {
		t.Errorf("links = %d", c)
	}
	if c := e.count("SELECT count(*) FROM customer_events WHERE event_type='ORDER_CREATED' AND customer_id=$1", aID); c != 1 {
		t.Errorf("events = %d", c)
	}
	if c := e.count("SELECT reserved FROM inventory_balances WHERE product_id=$1", int64(soda)); c != 1 {
		t.Errorf("reserved = %d", c)
	}
}

func TestCustomerOrderRollsBackOnStockFailure(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, soda, _ := seedCatalog(t, admin)
	stockSoda(t, admin, soda) // 5 units
	a, _ := newCustomer(t, e, "09121111111", "آرش")
	resp, body := placeOrder(a, newUUID(), line(espresso, 1, 950000), line(soda, 6, 450000))
	expect(t, resp, body, http.StatusConflict, "INSUFFICIENT_STOCK")
	for _, q := range []string{"SELECT count(*) FROM orders", "SELECT count(*) FROM customer_order_links",
		"SELECT count(*) FROM customer_events WHERE event_type='ORDER_CREATED'",
		"SELECT reserved FROM inventory_balances"} {
		if n := e.count(q); n != 0 {
			t.Errorf("%s = %d after a failed order", q, n)
		}
	}
}

func TestCustomerHistoryFollowsLifecycle(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, _, _ := seedCatalog(t, admin)
	a, _ := newCustomer(t, e, "09121111111", "آرش")

	// Completed, paid, then refunded.
	_, body := placeOrder(a, newUUID(), line(espresso, 2, 950000))
	id := orderIDByNumber(t, admin, body["order_number"].(float64))
	admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/accept", id), nil)
	resp, body := admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/payments", id),
		map[string]any{"method": "CASH", "amount_rial": 1900000}, withKey(newUUID()))
	expect(t, resp, body, http.StatusCreated, "")
	for _, step := range []string{"ready", "complete"} {
		admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/%s", id, step), nil)
	}
	resp, body = admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/refund", id), map[string]any{"note": "x"})
	expect(t, resp, body, http.StatusOK, "")
	resp, body = a.do(http.MethodGet, fmt.Sprintf("/api/v1/customer/orders/%v", id), nil)
	expect(t, resp, body, http.StatusOK, "")
	if body["status"] != "COMPLETED" || body["payment_status"] != "REFUNDED" || body["refunded_rial"].(float64) != 1900000 {
		t.Errorf("refunded order = %v", body)
	}
	kinds := []string{}
	for _, p := range body["payments"].([]any) {
		kinds = append(kinds, p.(map[string]any)["kind"].(string))
	}
	if strings.Join(kinds, ",") != "PAYMENT,REFUND" {
		t.Errorf("payments = %v", kinds)
	}

	// Cancelled by the customer through the tracking token of a linked order.
	_, body = placeOrder(a, newUUID(), line(espresso, 1, 950000))
	token := body["tracking_token"].(string)
	id2 := orderIDByNumber(t, admin, body["order_number"].(float64))
	resp, body = a.do(http.MethodPost, "/api/v1/public/order/cancel", nil, withHeader("X-Tracking-Token", token))
	expect(t, resp, body, http.StatusOK, "")
	resp, body = a.do(http.MethodGet, fmt.Sprintf("/api/v1/customer/orders/%v", id2), nil)
	if body["status"] != "CANCELLED" || body["cancel_reason"] != "CUSTOMER" {
		t.Errorf("cancelled order = %v", body)
	}
	resp, body = a.do(http.MethodGet, "/api/v1/customer/orders?limit=1", nil)
	expect(t, resp, body, http.StatusOK, "")
	if len(body["items"].([]any)) != 1 || body["next_before_id"] == nil {
		t.Fatalf("page 1 = %v", body)
	}
	resp, body = a.do(http.MethodGet, fmt.Sprintf("/api/v1/customer/orders?limit=1&before_id=%v", body["next_before_id"]), nil)
	if len(body["items"].([]any)) != 1 || body["items"].([]any)[0].(map[string]any)["id"].(float64) != id {
		t.Errorf("page 2 = %v", body)
	}
}

// C2 audit fixes.

func TestCustomerLoginRevokesThePresentedSession(t *testing.T) {
	e := newEnv(t)
	c, _ := newCustomer(t, e, "09121111111", "آرش")
	u, _ := url.Parse(e.srv.URL)
	old := c.http.Jar.Cookies(u)
	resp, body := c.do(http.MethodPost, customerPath+"login", map[string]string{"phone": "0912 111 1111", "password": "secure-cafe-2026"})
	expect(t, resp, body, http.StatusOK, "")
	if n := e.count("SELECT count(*) FROM customer_sessions"); n != 1 {
		t.Errorf("sessions after re-login = %d (the previous one must be revoked)", n)
	}
	stale := e.newClient()
	stale.http.Jar.SetCookies(u, old)
	resp, body = stale.do(http.MethodGet, customerPath+"me", nil)
	expect(t, resp, body, http.StatusUnauthorized, "UNAUTHENTICATED")
}

func TestPasswordChangeRevokesEverySessionAndRotates(t *testing.T) {
	e := newEnv(t)
	c, _ := newCustomer(t, e, "09121111111", "آرش")
	u, _ := url.Parse(e.srv.URL)
	before := c.http.Jar.Cookies(u)
	resp, body := c.do(http.MethodPost, customerPath+"password", map[string]string{
		"current_password": "secure-cafe-2026", "new_password": "new-secure-2027"})
	expect(t, resp, body, http.StatusOK, "")
	c.csrf = body["csrf_token"].(string)
	resp, body = c.do(http.MethodGet, customerPath+"me", nil)
	expect(t, resp, body, http.StatusOK, "")
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(u, before)
	old := &client{e: e, http: &http.Client{Jar: jar}}
	resp, body = old.do(http.MethodGet, customerPath+"me", nil)
	expect(t, resp, body, http.StatusUnauthorized, "UNAUTHENTICATED")
}

func TestInvalidPhonesDoNotShareARateLimitBucket(t *testing.T) {
	e := newEnv(t)
	c := e.newClient()
	for i := 0; i < 5; i++ {
		resp, body := c.do(http.MethodPost, customerPath+"register", map[string]string{
			"first_name": "x", "last_name": "y", "phone": "not-a-phone", "password": "secure-cafe-2026"})
		expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	}
	newCustomer(t, e, "۰۹۱۲-۳۳۳-۳۳۳۳", "سیما")
}
