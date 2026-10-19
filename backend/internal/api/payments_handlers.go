package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/payments"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
)

type paymentView struct {
	ID                int64      `json:"id"`
	OrderID           int64      `json:"order_id"`
	Kind              string     `json:"kind"`
	Method            string     `json:"method"`
	AmountRial        int64      `json:"amount_rial"`
	RefundOfPaymentID *int64     `json:"refund_of_payment_id"`
	Note              *string    `json:"note"`
	CreatedBy         int64      `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	VoidedAt          *time.Time `json:"voided_at"`
	VoidedBy          *int64     `json:"voided_by"`
	VoidReason        *string    `json:"void_reason"`
}

func toPaymentView(p store.Payment) paymentView {
	return paymentView{ID: p.ID, OrderID: p.OrderID, Kind: p.Kind, Method: p.Method, AmountRial: p.AmountRial,
		RefundOfPaymentID: optInt64(p.RefundOfPaymentID), Note: optText(p.Note), CreatedBy: p.CreatedBy,
		CreatedAt: p.CreatedAt.Time, VoidedAt: optTime(p.VoidedAt), VoidedBy: optInt64(p.VoidedBy), VoidReason: optText(p.VoidReason)}
}

// orderMoney is the order's payment state after a payment operation.
type orderMoney struct {
	OrderID       int64  `json:"order_id"`
	Status        string `json:"status"`
	PaymentStatus string `json:"payment_status"`
	TotalRial     int64  `json:"total_rial"`
	PaidRial      int64  `json:"paid_rial"`
	RefundedRial  int64  `json:"refunded_rial"`
	RemainingRial int64  `json:"remaining_rial"`
}

func toOrderMoney(o store.Order) orderMoney {
	remaining := o.TotalRial - o.PaidRial
	if o.RefundedRial > 0 {
		remaining = 0
	}
	return orderMoney{OrderID: o.ID, Status: o.Status, PaymentStatus: o.PaymentStatus.String, TotalRial: o.TotalRial,
		PaidRial: o.PaidRial, RefundedRial: o.RefundedRial, RemainingRial: remaining}
}

func (a *API) writePaymentError(w http.ResponseWriter, r *http.Request, err error) {
	var bad payments.ValidationError
	var state payments.StateError
	switch {
	case errors.As(err, &bad):
		httpx.ValidationError(w, bad)
	case errors.As(err, &state):
		httpx.Error(w, http.StatusConflict, "PAYMENT_STATE_CONFLICT", state.Reason)
	case errors.Is(err, payments.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "not found")
	case errors.Is(err, payments.ErrIdempotencyReused):
		httpx.Error(w, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key was used for a different request")
	default:
		a.internal(w, r, err)
	}
}

// POST /admin/orders/{id}/payments 🔁 {method, amount_rial, note?}
func (a *API) recordPayment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	key, ok := httpx.IdempotencyKey(w, r)
	if !ok {
		return
	}
	var in struct {
		Method     string `json:"method"`
		AmountRial int64  `json:"amount_rial"`
		Note       string `json:"note"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	p, o, replay, err := a.payments.Record(r.Context(), a.actor(r), id, key, payments.RecordInput(in))
	if err != nil {
		a.writePaymentError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	httpx.JSON(w, status, map[string]any{"payment": toPaymentView(p), "order": toOrderMoney(o)})
}

// POST /admin/orders/{id}/refund {note?} — full refund of a completed, fully paid order.
func (a *API) refundOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if r.ContentLength != 0 && !httpx.DecodeJSON(w, r, &in) {
		return
	}
	o, refunds, err := a.payments.Refund(r.Context(), a.actor(r), id, in.Note)
	if err != nil {
		a.writePaymentError(w, r, err)
		return
	}
	views := make([]paymentView, len(refunds))
	for i, p := range refunds {
		views[i] = toPaymentView(p)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"refunds": views, "order": toOrderMoney(o)})
}

// POST /admin/payments/{id}/void {reason}
func (a *API) voidPayment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	p, o, err := a.payments.Void(r.Context(), a.actor(r), id, in.Reason)
	if err != nil {
		a.writePaymentError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"payment": toPaymentView(p), "order": toOrderMoney(o)})
}
