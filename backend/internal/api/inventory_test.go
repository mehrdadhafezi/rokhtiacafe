package api_test

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func withKey(k string) reqOpt { return func(r *http.Request) { r.Header.Set("Idempotency-Key", k) } }

func today() string {
	tehran, _ := time.LoadLocation("Asia/Tehran")
	return time.Now().In(tehran).Format("2006-01-02")
}

func TestInventoryAPI(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, soda, _ := seedCatalog(t, admin)

	buy := map[string]any{"product_id": soda, "quantity": 10, "unit_cost_rial": 1000, "purchased_on": today(), "supplier": "عمده‌فروشی"}
	resp, body := admin.do(http.MethodPost, "/api/v1/admin/purchases", buy)
	expect(t, resp, body, http.StatusBadRequest, "BAD_REQUEST") // Idempotency-Key required

	key := newUUID()
	resp, body = admin.do(http.MethodPost, "/api/v1/admin/purchases", buy, withKey(key))
	expect(t, resp, body, http.StatusCreated, "")
	purchaseID := body["id"].(float64)
	if body["purchased_on"] != today() || body["total_cost_rial"].(float64) != 10000 || body["supplier"] != "عمده‌فروشی" {
		t.Errorf("purchase = %v", body)
	}
	resp, body = admin.do(http.MethodPost, "/api/v1/admin/purchases", buy, withKey(key))
	expect(t, resp, body, http.StatusOK, "") // replay
	if body["id"].(float64) != purchaseID {
		t.Errorf("replay returned a different purchase: %v", body)
	}
	buy["quantity"] = 11
	resp, body = admin.do(http.MethodPost, "/api/v1/admin/purchases", buy, withKey(key))
	expect(t, resp, body, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED")
	resp, body = admin.do(http.MethodPost, "/api/v1/admin/purchases",
		map[string]any{"product_id": espresso, "quantity": 1, "unit_cost_rial": 1, "purchased_on": today()}, withKey(newUUID()))
	expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")

	resp, body = admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/inventory/%v/adjustments", soda),
		map[string]any{"direction": "OUT", "quantity": 4, "reason_code": "DAMAGED", "note": "dropped crate"}, withKey(newUUID()))
	expect(t, resp, body, http.StatusCreated, "")
	if body["value_delta_rial"].(float64) != -4000 || body["on_hand_after"].(float64) != 6 {
		t.Errorf("adjustment = %v", body)
	}
	resp, body = admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/inventory/%v/adjustments", soda),
		map[string]any{"direction": "OUT", "quantity": 7, "reason_code": "DAMAGED", "note": "x"}, withKey(newUUID()))
	expect(t, resp, body, http.StatusConflict, "INSUFFICIENT_STOCK")

	resp, body = admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/purchases/%v/void", purchaseID), map[string]any{"reason": "late"})
	expect(t, resp, body, http.StatusConflict, "PURCHASE_ALREADY_CONSUMED")

	resp, body = admin.do(http.MethodGet, "/api/v1/admin/inventory", nil)
	expect(t, resp, body, http.StatusOK, "")
	var sodaRow map[string]any
	for _, it := range body["items"].([]any) {
		if it.(map[string]any)["product_id"].(float64) == soda {
			sodaRow = it.(map[string]any)
		}
	}
	if sodaRow["on_hand"].(float64) != 6 || sodaRow["available"].(float64) != 6 || sodaRow["avg_unit_cost_rial"].(float64) != 1000 ||
		sodaRow["inventory_value_rial"].(float64) != 6000 || sodaRow["threshold"].(float64) != 3 || sodaRow["low"] != false {
		t.Errorf("inventory row = %v", sodaRow)
	}

	resp, body = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/admin/inventory/%v/movements?limit=1", soda), nil)
	expect(t, resp, body, http.StatusOK, "")
	items := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["movement_type"] != "ADJUSTMENT_OUT" || body["next_before_id"] == nil {
		t.Errorf("movements page = %v", body)
	}
	resp, body = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/admin/inventory/%v/movements?before_id=%v", soda, body["next_before_id"]), nil)
	if items := body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["movement_type"] != "PURCHASE" {
		t.Errorf("second page = %v", body)
	}
	resp, body = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/admin/purchases?product_id=%v&from=%s&to=%s", soda, today(), today()), nil)
	expect(t, resp, body, http.StatusOK, "")
	if items := body["items"].([]any); len(items) != 1 {
		t.Errorf("purchases = %v", body)
	}

	var audits int
	_ = e.pool.QueryRow(e.ctx, `SELECT count(*) FROM audit_logs WHERE action IN ('purchase.create','inventory.adjust')`).Scan(&audits)
	if audits != 2 {
		t.Errorf("audit rows = %d, want 2", audits)
	}
}

// OPERATOR sees quantities only (SECURITY §3) and cannot change stock.
func TestInventoryOperatorAccess(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, _, soda, _ := seedCatalog(t, admin)
	admin.do(http.MethodPost, "/api/v1/admin/purchases",
		map[string]any{"product_id": soda, "quantity": 2, "unit_cost_rial": 1000, "purchased_on": today()}, withKey(newUUID()))
	e.createUser("barista", "OPERATOR", "barista-pass-1")
	op := e.newClient().login("barista", "barista-pass-1")

	resp, body := op.do(http.MethodGet, "/api/v1/admin/inventory", nil)
	expect(t, resp, body, http.StatusOK, "")
	raw, _ := json.Marshal(body)
	for _, field := range []string{"avg_unit_cost_rial", "inventory_value_rial", "last_unit_cost_rial"} {
		if strings.Contains(string(raw), field) {
			t.Errorf("OPERATOR sees %s: %s", field, raw)
		}
	}
	for _, r := range []struct {
		method, path string
	}{
		{http.MethodGet, fmt.Sprintf("/api/v1/admin/inventory/%v/movements", soda)},
		{http.MethodPost, fmt.Sprintf("/api/v1/admin/inventory/%v/adjustments", soda)},
		{http.MethodGet, "/api/v1/admin/purchases"},
		{http.MethodPost, "/api/v1/admin/purchases"},
		{http.MethodPost, "/api/v1/admin/purchases/1/void"},
	} {
		resp, body := op.do(r.method, r.path, map[string]any{}, withKey(newUUID()))
		expect(t, resp, body, http.StatusForbidden, "FORBIDDEN")
	}
}

// Review finding: a lock timeout is reported as 503 BUSY_RETRY (safe to retry), not 500.
func TestLockTimeoutReturnsBusyRetry(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, _, soda, _ := seedCatalog(t, admin)

	tx, err := e.pool.Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(e.ctx)
	if _, err := tx.Exec(e.ctx, "SELECT 1 FROM inventory_balances WHERE product_id = $1 FOR UPDATE", int64(soda)); err != nil {
		t.Fatal(err)
	}

	key := newUUID()
	buy := map[string]any{"product_id": soda, "quantity": 1, "unit_cost_rial": 1000, "purchased_on": today()}
	resp, body := admin.do(http.MethodPost, "/api/v1/admin/purchases", buy, withKey(key)) // waits for lock_timeout (3s)
	expect(t, resp, body, http.StatusServiceUnavailable, "BUSY_RETRY")
	if resp.Header.Get("Retry-After") == "" {
		t.Error("missing Retry-After")
	}
	tx.Rollback(e.ctx)

	resp, body = admin.do(http.MethodPost, "/api/v1/admin/purchases", buy, withKey(key)) // retry with the same key
	expect(t, resp, body, http.StatusCreated, "")
	var n int
	_ = e.pool.QueryRow(e.ctx, "SELECT count(*) FROM purchase_entries").Scan(&n)
	if n != 1 {
		t.Errorf("purchases after retry = %d, want 1", n)
	}
}
