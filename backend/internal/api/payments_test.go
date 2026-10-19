package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestPaymentsAPI(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, _, _ := seedCatalog(t, admin)
	e.createUser("barista", "OPERATOR", "barista-pass-1")
	op := e.newClient().login("barista", "barista-pass-1")

	resp, body := placeOrder(e.newClient(), newUUID(), line(espresso, 2, 950000)) // 1 900 000
	expect(t, resp, body, http.StatusCreated, "")
	_, list := op.do(http.MethodGet, "/api/v1/admin/orders?status=PENDING", nil)
	id := list["items"].([]any)[0].(map[string]any)["id"].(float64)
	payPath := fmt.Sprintf("/api/v1/admin/orders/%v/payments", id)

	resp, body = op.do(http.MethodPost, payPath, map[string]any{"method": "CASH", "amount_rial": 1000000}, withKey(newUUID()))
	expect(t, resp, body, http.StatusConflict, "PAYMENT_STATE_CONFLICT") // PENDING
	op.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/accept", id), nil)

	resp, body = op.do(http.MethodPost, payPath, map[string]any{"method": "CASH", "amount_rial": 1000000})
	expect(t, resp, body, http.StatusBadRequest, "BAD_REQUEST") // Idempotency-Key required
	key := newUUID()
	resp, body = op.do(http.MethodPost, payPath, map[string]any{"method": "CASH", "amount_rial": 1000000}, withKey(key))
	expect(t, resp, body, http.StatusCreated, "")
	cashID := body["payment"].(map[string]any)["id"].(float64)
	if m := body["order"].(map[string]any); m["remaining_rial"].(float64) != 900000 || m["payment_status"] != "UNPAID" {
		t.Errorf("after cash: %v", m)
	}
	resp, body = op.do(http.MethodPost, payPath, map[string]any{"method": "CASH", "amount_rial": 1000000}, withKey(key))
	expect(t, resp, body, http.StatusOK, "") // replay
	resp, body = op.do(http.MethodPost, payPath, map[string]any{"method": "CASH", "amount_rial": 1}, withKey(key))
	expect(t, resp, body, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED")
	resp, body = op.do(http.MethodPost, payPath, map[string]any{"method": "CARD_TERMINAL", "amount_rial": 1000000}, withKey(newUUID()))
	expect(t, resp, body, http.StatusConflict, "PAYMENT_STATE_CONFLICT") // overpay

	// OPERATOR may not void or refund.
	resp, body = op.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/payments/%v/void", cashID), map[string]any{"reason": "x"})
	expect(t, resp, body, http.StatusForbidden, "FORBIDDEN")
	resp, body = op.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/refund", id), nil)
	expect(t, resp, body, http.StatusForbidden, "FORBIDDEN")

	resp, body = admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/payments/%v/void", cashID), map[string]any{"reason": "entered on the wrong order"})
	expect(t, resp, body, http.StatusOK, "")
	if body["order"].(map[string]any)["paid_rial"].(float64) != 0 {
		t.Errorf("after void: %v", body)
	}
	resp, body = op.do(http.MethodPost, payPath, map[string]any{"method": "CARD_TERMINAL", "amount_rial": 1900000}, withKey(newUUID()))
	expect(t, resp, body, http.StatusCreated, "")
	for _, step := range []string{"ready", "complete"} {
		op.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/%s", id, step), nil)
	}
	resp, body = admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/refund", id), map[string]any{"note": "wrong drink"})
	expect(t, resp, body, http.StatusOK, "")
	if refunds := body["refunds"].([]any); len(refunds) != 1 || refunds[0].(map[string]any)["method"] != "CARD_TERMINAL" {
		t.Errorf("refunds = %v", body)
	}

	resp, body = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/admin/orders/%v", id), nil)
	expect(t, resp, body, http.StatusOK, "")
	if body["payment_status"] != "REFUNDED" || len(body["payments"].([]any)) != 3 { // void cash, card, refund
		t.Errorf("order detail = %v", body)
	}
}

// Operators can cancel an order that holds a prepayment; the prepayment is refunded.
func TestOperatorCancelRefundsPrepayment(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, _, _ := seedCatalog(t, admin)
	e.createUser("barista", "OPERATOR", "barista-pass-1")
	op := e.newClient().login("barista", "barista-pass-1")
	resp, _ := placeOrder(e.newClient(), newUUID(), line(espresso, 1, 950000))
	if resp.StatusCode != http.StatusCreated {
		t.Fatal(resp.Status)
	}
	_, list := op.do(http.MethodGet, "/api/v1/admin/orders", nil)
	id := list["items"].([]any)[0].(map[string]any)["id"].(float64)
	op.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/accept", id), nil)
	// Split prepayment: part cash, part card.
	op.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/payments", id), map[string]any{"method": "CASH", "amount_rial": 500000}, withKey(newUUID()))
	op.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/payments", id), map[string]any{"method": "CARD_TERMINAL", "amount_rial": 450000}, withKey(newUUID()))

	resp, body := op.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/cancel", id), map[string]any{"note": "out of milk"})
	expect(t, resp, body, http.StatusOK, "")
	if body["status"] != "CANCELLED" || body["payment_status"] != "REFUNDED" || body["refunded_rial"].(float64) != 950000 {
		t.Errorf("cancel = %v", body)
	}
	// API-CONTRACT §5.2: the response says how much to hand back per method.
	refunds, _ := json.Marshal(body["refunds"])
	if string(refunds) != `[{"amount_rial":500000,"method":"CASH"},{"amount_rial":450000,"method":"CARD_TERMINAL"}]` {
		t.Errorf("refunds = %s", refunds)
	}
	// Repeating the cancel refunds nothing more.
	resp, body = op.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/cancel", id), nil)
	expect(t, resp, body, http.StatusOK, "")
	if r, ok := body["refunds"].([]any); !ok || len(r) != 0 {
		t.Errorf("repeat cancel refunds = %v", body["refunds"])
	}

	// An order without money returns an empty refund list.
	placeOrder(e.newClient(), newUUID(), line(espresso, 1, 950000))
	_, list = op.do(http.MethodGet, "/api/v1/admin/orders?status=PENDING", nil)
	unpaid := list["items"].([]any)[0].(map[string]any)["id"].(float64)
	resp, body = op.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/cancel", unpaid), nil)
	expect(t, resp, body, http.StatusOK, "")
	if r, ok := body["refunds"].([]any); !ok || len(r) != 0 {
		t.Errorf("unpaid cancel refunds = %v", body["refunds"])
	}
}
