package api_test

import (
	"fmt"
	"net/http"
	"testing"
)

func TestReportsAndExpensesAPI(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, soda, _ := seedCatalog(t, admin)
	stockSoda(t, admin, soda)
	e.createUser("barista", "OPERATOR", "barista-pass-1")
	op := e.newClient().login("barista", "barista-pass-1")

	// One completed, card-paid order today.
	resp, body := placeOrder(e.newClient(), newUUID(), line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusCreated, "")
	_, list := op.do(http.MethodGet, "/api/v1/admin/orders?status=PENDING", nil)
	id := list["items"].([]any)[0].(map[string]any)["id"].(float64)
	op.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/accept", id), nil)
	op.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/payments", id), map[string]any{"method": "CARD_TERMINAL", "amount_rial": 950000}, withKey(newUUID()))
	op.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/ready", id), nil)
	op.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/complete", id), nil)

	resp, body = admin.do(http.MethodPost, "/api/v1/admin/expenses",
		map[string]any{"category": "UTILITIES", "amount_rial": 300000, "expense_date": today(), "description": "electricity"}, withKey(newUUID()))
	expect(t, resp, body, http.StatusCreated, "")
	expenseID := body["id"].(float64)
	resp, body = admin.do(http.MethodPost, "/api/v1/admin/expenses",
		map[string]any{"category": "UTILITIES", "amount_rial": 300000, "expense_date": today(), "description": "electricity"})
	expect(t, resp, body, http.StatusBadRequest, "BAD_REQUEST") // Idempotency-Key required
	resp, body = admin.do(http.MethodGet, "/api/v1/admin/expenses?category=UTILITIES&from="+today()+"&to="+today(), nil)
	expect(t, resp, body, http.StatusOK, "")
	if len(body["items"].([]any)) != 1 {
		t.Errorf("expenses = %v", body)
	}

	path := fmt.Sprintf("/api/v1/admin/reports/summary?from=%s&to=%s", today(), today())
	resp, body = admin.do(http.MethodGet, path, nil)
	expect(t, resp, body, http.StatusOK, "")
	tot := body["totals"].(map[string]any)
	if tot["revenue_rial"].(float64) != 950000 || tot["expenses_rial"].(float64) != 300000 || tot["operating_result_rial"].(float64) != 650000 ||
		tot["collected"].(map[string]any)["CARD_TERMINAL"].(float64) != 950000 || tot["refunds_recorded"].(map[string]any)["CASH"].(float64) != 0 {
		t.Errorf("totals = %v", tot)
	}
	if body["timezone"] != "Asia/Tehran" || len(body["daily"].([]any)) != 1 {
		t.Errorf("summary shape = %v", body)
	}

	admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/expenses/%v/void", expenseID), map[string]any{"reason": "wrong month"})
	_, body = admin.do(http.MethodGet, path, nil)
	if body["totals"].(map[string]any)["expenses_rial"].(float64) != 0 {
		t.Error("voided expense still counted")
	}

	resp, body = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/admin/reports/products?from=%s&to=%s", today(), today()), nil)
	expect(t, resp, body, http.StatusOK, "")
	item := body["items"].([]any)[0].(map[string]any)
	if item["product_id"].(float64) != espresso || item["estimated_cost_rial"].(float64) != 300000 || item["estimated_margin_rial"].(float64) != 650000 {
		t.Errorf("product report = %v", item)
	}
	resp, body = admin.do(http.MethodGet, "/api/v1/admin/reports/receivables", nil)
	expect(t, resp, body, http.StatusOK, "")

	for _, q := range []string{"", "?from=2026-10-20&to=2026-10-19", "?from=2026-01-01&to=2027-06-01", "?from=x&to=y"} {
		resp, body = admin.do(http.MethodGet, "/api/v1/admin/reports/summary"+q, nil)
		expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	}

	// OPERATOR: no reports, no expenses (SECURITY §3).
	for _, p := range []string{path, "/api/v1/admin/reports/products?from=2026-10-01&to=2026-10-02", "/api/v1/admin/reports/receivables", "/api/v1/admin/expenses"} {
		resp, body = op.do(http.MethodGet, p, nil)
		expect(t, resp, body, http.StatusForbidden, "FORBIDDEN")
	}
	resp, body = op.do(http.MethodPost, "/api/v1/admin/expenses", map[string]any{}, withKey(newUUID()))
	expect(t, resp, body, http.StatusForbidden, "FORBIDDEN")
}

func TestDashboard(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, soda, _ := seedCatalog(t, admin) // soda has low_stock_threshold 3
	stockSoda(t, admin, soda)                        // 5 units
	e.createUser("barista", "OPERATOR", "barista-pass-1")
	op := e.newClient().login("barista", "barista-pass-1")
	placeOrder(e.newClient(), newUUID(), line(soda, 2, 450000)) // available 3 → low
	placeOrder(e.newClient(), newUUID(), line(espresso, 1, 950000))

	resp, body := op.do(http.MethodGet, "/api/v1/admin/dashboard", nil)
	expect(t, resp, body, http.StatusOK, "")
	if body["open_counts"].(map[string]any)["PENDING"].(float64) != 2 || body["today_orders"].(float64) != 2 || body["ordering_open"] != true {
		t.Errorf("dashboard = %v", body)
	}
	low := body["low_stock"].([]any)
	if len(low) != 1 || low[0].(map[string]any)["product_id"].(float64) != soda || low[0].(map[string]any)["available"].(float64) != 3 {
		t.Errorf("low stock = %v", low)
	}
	if _, ok := body["today_revenue_rial"]; ok {
		t.Error("OPERATOR sees today's revenue")
	}
	_, body = admin.do(http.MethodGet, "/api/v1/admin/dashboard", nil)
	if v, ok := body["today_revenue_rial"]; !ok || v.(float64) != 0 {
		t.Errorf("ADMIN today_revenue_rial = %v", v)
	}
	if body["ordering_switch_open"] != true || body["ordering_hours"].(map[string]any)["opens_at"] == "" {
		t.Errorf("ordering state = %v / %v", body["ordering_switch_open"], body["ordering_hours"])
	}

	// The kill switch is reported separately from the effective state.
	resp, body = admin.do(http.MethodPut, "/api/v1/admin/ordering", map[string]any{"open": false})
	expect(t, resp, body, http.StatusOK, "")
	_, body = op.do(http.MethodGet, "/api/v1/admin/dashboard", nil)
	if body["ordering_switch_open"] != false || body["ordering_open"] != false {
		t.Errorf("after switch off: switch=%v open=%v", body["ordering_switch_open"], body["ordering_open"])
	}
}
