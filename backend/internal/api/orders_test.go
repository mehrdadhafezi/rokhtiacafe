package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

// stockSoda puts 5 units of the seeded soda in stock.
func stockSoda(t *testing.T, admin *client, soda float64) {
	t.Helper()
	resp, body := admin.do(http.MethodPost, "/api/v1/admin/purchases",
		map[string]any{"product_id": soda, "quantity": 5, "unit_cost_rial": 1000, "purchased_on": today()}, withKey(newUUID()))
	expect(t, resp, body, http.StatusCreated, "")
}

func placeOrder(c *client, key string, items ...map[string]any) (*http.Response, map[string]any) {
	return c.do(http.MethodPost, "/api/v1/public/orders",
		map[string]any{"customer_label": "سارا", "customer_note": "بدون شکر", "items": items},
		withKey(key), withHeader("X-Client-Id", "11111111-2222-4333-8444-555555555555"))
}

func line(id float64, qty int, price int) map[string]any {
	return map[string]any{"product_id": id, "quantity": qty, "expected_unit_price_rial": price}
}

// End-to-end through HTTP: customer orders and tracks; operator prepares and completes.
func TestOrderFlowOverHTTP(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, soda, _ := seedCatalog(t, admin)
	stockSoda(t, admin, soda)
	e.createUser("barista", "OPERATOR", "barista-pass-1")
	op := e.newClient().login("barista", "barista-pass-1")
	customer := e.newClient()

	key := newUUID()
	resp, body := placeOrder(customer, key, line(soda, 2, 450000), line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusCreated, "")
	token, _ := body["tracking_token"].(string)
	if body["order_number"].(float64) != 1 || body["status"] != "PENDING" || body["total_rial"].(float64) != 1850000 || len(token) != 43 {
		t.Fatalf("created = %v", body)
	}
	for _, c := range resp.Cookies() {
		t.Errorf("public order set a cookie: %v", c.Name)
	}
	resp, body = placeOrder(customer, key, line(soda, 2, 450000), line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusOK, "") // replay
	if body["tracking_token"] != token {
		t.Error("replay returned a different token")
	}

	resp, body = customer.do(http.MethodGet, "/api/v1/public/order", nil, withHeader("X-Tracking-Token", token))
	expect(t, resp, body, http.StatusOK, "")
	if body["status"] != "PENDING" || body["tracking_token"] != nil || body["expired"] != false || len(body["items"].([]any)) != 2 {
		t.Errorf("tracking = %v", body)
	}
	resp, body = customer.do(http.MethodGet, "/api/v1/public/order", nil)
	expect(t, resp, body, http.StatusNotFound, "NOT_FOUND")

	resp, body = op.do(http.MethodGet, "/api/v1/admin/orders?status=PENDING", nil)
	expect(t, resp, body, http.StatusOK, "")
	items := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["customer_note"] != "بدون شکر" {
		t.Fatalf("pending list = %v", body)
	}
	id := items[0].(map[string]any)["id"].(float64)
	for _, step := range []string{"accept", "ready", "complete"} {
		resp, body = op.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/%s", id, step), nil)
		expect(t, resp, body, http.StatusOK, "")
	}
	if body["status"] != "COMPLETED" {
		t.Errorf("after complete: %v", body)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "cogs_rial") || strings.Contains(string(raw), "estimated_cost_rial") {
		t.Errorf("OPERATOR sees cost fields: %s", raw)
	}
	resp, body = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/admin/orders/%v", id), nil)
	expect(t, resp, body, http.StatusOK, "")
	raw, _ = json.Marshal(body)
	if !strings.Contains(string(raw), `"cogs_rial":2000`) {
		t.Errorf("ADMIN order detail lacks cogs: %s", raw)
	}

	resp, body = op.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/cancel", id), nil)
	expect(t, resp, body, http.StatusConflict, "ILLEGAL_TRANSITION")
	if body["error"].(map[string]any)["details"].(map[string]any)["current_status"] != "COMPLETED" {
		t.Errorf("details = %v", body)
	}
	resp, body = customer.do(http.MethodGet, "/api/v1/public/order", nil, withHeader("X-Tracking-Token", token))
	if body["status"] != "COMPLETED" || body["completed_at"] == nil {
		t.Errorf("customer sees %v", body)
	}
}

func TestPublicOrderErrors(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, soda, cake := seedCatalog(t, admin)
	stockSoda(t, admin, soda)
	c := e.newClient()

	resp, body := c.do(http.MethodPost, "/api/v1/public/orders", map[string]any{"items": []any{line(soda, 1, 450000)}})
	expect(t, resp, body, http.StatusBadRequest, "BAD_REQUEST") // Idempotency-Key required

	resp, body = placeOrder(c, newUUID(), line(soda, 1, 400000))
	expect(t, resp, body, http.StatusConflict, "PRICE_CHANGED")
	changed := body["error"].(map[string]any)["details"].(map[string]any)["items"].([]any)[0].(map[string]any)
	if changed["current_unit_price_rial"].(float64) != 450000 {
		t.Errorf("price details = %v", changed)
	}
	resp, body = placeOrder(c, newUUID(), line(cake, 1, 1200000)) // cake has no stock
	expect(t, resp, body, http.StatusConflict, "INSUFFICIENT_STOCK")
	admin.do(http.MethodPatch, fmt.Sprintf("/api/v1/admin/products/%v/availability", espresso), map[string]any{"is_available": false})
	resp, body = placeOrder(c, newUUID(), line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusUnprocessableEntity, "PRODUCT_UNAVAILABLE")
	resp, body = c.do(http.MethodPost, "/api/v1/public/orders",
		map[string]any{"items": []any{map[string]any{"product_id": soda, "quantity": 1, "expected_unit_price_rial": 450000, "price_rial": 1}}}, withKey(newUUID()))
	expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR") // no client prices beyond the expectation

	// Customer cancel through the header.
	resp, body = placeOrder(c, newUUID(), line(soda, 1, 450000))
	expect(t, resp, body, http.StatusCreated, "")
	token := body["tracking_token"].(string)
	resp, body = c.do(http.MethodPost, "/api/v1/public/order/cancel", nil, withHeader("X-Tracking-Token", token))
	expect(t, resp, body, http.StatusOK, "")
	if body["status"] != "CANCELLED" || body["cancel_reason"] != "CUSTOMER" {
		t.Errorf("customer cancel = %v", body)
	}
}

// Kill switch (ADMIN only) closes ordering and the menu reflects it.
func TestOrderingKillSwitch(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, _, _ := seedCatalog(t, admin)
	e.createUser("barista", "OPERATOR", "barista-pass-1")
	op := e.newClient().login("barista", "barista-pass-1")

	resp, body := op.do(http.MethodPut, "/api/v1/admin/ordering", map[string]any{"open": false})
	expect(t, resp, body, http.StatusForbidden, "FORBIDDEN")
	resp, body = admin.do(http.MethodPut, "/api/v1/admin/ordering", map[string]any{"open": false})
	expect(t, resp, body, http.StatusOK, "")
	if body["open_now"] != false {
		t.Errorf("switch = %v", body)
	}
	_, menu := e.newClient().do(http.MethodGet, "/api/v1/public/menu", nil)
	if menu["ordering"].(map[string]any)["open"] != false {
		t.Error("menu still shows ordering open")
	}
	resp, body = placeOrder(e.newClient(), newUUID(), line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusServiceUnavailable, "ORDERING_CLOSED")
	resp, body = admin.do(http.MethodPut, "/api/v1/admin/ordering", map[string]any{"open": true})
	expect(t, resp, body, http.StatusOK, "")
	resp, body = placeOrder(e.newClient(), newUUID(), line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusCreated, "")
}

// R-F11-4 (device part): the per-device limit applies to new orders only, never to replays.
func TestPerDeviceOrderRateLimit(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, _, _ := seedCatalog(t, admin)
	c := e.newClient()
	var firstKey string
	for i := 0; i < 6; i++ {
		key := newUUID()
		if i == 0 {
			firstKey = key
		}
		resp, body := placeOrder(c, key, line(espresso, 1, 950000))
		expect(t, resp, body, http.StatusCreated, "")
	}
	resp, body := placeOrder(c, newUUID(), line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusTooManyRequests, "RATE_LIMITED")
	resp, body = placeOrder(c, firstKey, line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusOK, "") // replay passes the limiter
}

// Review finding: simultaneous retries of one new key are not charged against rate limits.
func TestConcurrentRetriesOfNewKeyAreNotRateLimited(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, _, _ := seedCatalog(t, admin)
	key := newUUID()
	var wg sync.WaitGroup
	codes := make([]int, 10)
	start := make(chan struct{})
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, _ := placeOrder(e.newClient(), key, line(espresso, 1, 950000))
			codes[i] = resp.StatusCode
		}()
	}
	close(start)
	wg.Wait()
	created := 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusOK:
		default:
			t.Errorf("status %d among concurrent retries %v", c, codes)
		}
	}
	if created != 1 {
		t.Errorf("created %d orders, want 1 (%v)", created, codes)
	}
	// Only the one real order was charged: five more new orders from the device still fit.
	for i := 0; i < 5; i++ {
		resp, body := placeOrder(e.newClient(), newUUID(), line(espresso, 1, 950000))
		expect(t, resp, body, http.StatusCreated, "")
	}
	resp, body := placeOrder(e.newClient(), newUUID(), line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusTooManyRequests, "RATE_LIMITED")
}
