package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/orders"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
)

// TrackingHeader carries the customer's order token; it never appears in a URL (SECURITY §10).
const TrackingHeader = "X-Tracking-Token"

// ClientIDHeader is a random per-device id kept in localStorage, used only for rate limiting.
const ClientIDHeader = "X-Client-Id"

var clientIDRE = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)

func (a *API) writeOrderError(w http.ResponseWriter, r *http.Request, err error) {
	var bad orders.ValidationError
	var illegal orders.IllegalTransitionError
	var short orders.InsufficientStockError
	var unavailable orders.UnavailableError
	var price orders.PriceChangedError
	switch {
	case errors.As(err, &bad):
		httpx.ValidationError(w, bad)
	case errors.As(err, &illegal):
		httpx.JSON(w, http.StatusConflict, httpx.ErrorBody{Error: httpx.ErrorDetail{Code: "ILLEGAL_TRANSITION",
			Message: "status change not allowed", Details: map[string]any{"current_status": illegal.Current}}})
	case errors.As(err, &short):
		httpx.JSON(w, http.StatusConflict, httpx.ErrorBody{Error: httpx.ErrorDetail{Code: "INSUFFICIENT_STOCK",
			Message: "not enough stock", Details: map[string]any{"product_ids": short.ProductIDs}}})
	case errors.As(err, &unavailable):
		httpx.JSON(w, http.StatusUnprocessableEntity, httpx.ErrorBody{Error: httpx.ErrorDetail{Code: "PRODUCT_UNAVAILABLE",
			Message: "product unavailable", Details: map[string]any{"product_ids": unavailable.ProductIDs}}})
	case errors.As(err, &price):
		httpx.JSON(w, http.StatusConflict, httpx.ErrorBody{Error: httpx.ErrorDetail{Code: "PRICE_CHANGED",
			Message: "prices changed; confirm the new prices", Details: map[string]any{"items": price.Items}}})
	case errors.Is(err, orders.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case errors.Is(err, orders.ErrIdempotencyReused):
		httpx.Error(w, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key was used for a different request")
	case errors.Is(err, orders.ErrOrderExpired):
		httpx.Error(w, http.StatusConflict, "ORDER_EXPIRED", "the order expired before it was accepted")
	case errors.Is(err, orders.ErrPaymentState):
		httpx.Error(w, http.StatusConflict, "PAYMENT_STATE_CONFLICT", "the order holds a payment")
	case errors.Is(err, orders.ErrOrderingClosed):
		httpx.Error(w, http.StatusServiceUnavailable, "ORDERING_CLOSED", "ordering is closed")
	case errors.Is(err, orders.ErrOrderingBusy):
		httpx.Error(w, http.StatusServiceUnavailable, "ORDERING_BUSY", "too many open orders, try again shortly")
	case errors.Is(err, orders.ErrTooManyOpenOrders):
		w.Header().Set("Retry-After", "60")
		httpx.Error(w, http.StatusTooManyRequests, "TOO_MANY_OPEN_ORDERS", "too many open orders from this network")
	default:
		a.internal(w, r, err)
	}
}

// rateLimitError carries the limiter's wait out of orders.Create.
type rateLimitError struct{ wait time.Duration }

func (rateLimitError) Error() string { return "rate limited" }

// ---- views ----

type publicItemView struct {
	ProductID     int64  `json:"product_id"`
	Name          string `json:"name"`
	Quantity      int32  `json:"quantity"`
	UnitPriceRial int64  `json:"unit_price_rial"`
	LineTotalRial int64  `json:"line_total_rial"`
}

type publicOrderView struct {
	OrderNumber   int32            `json:"order_number"`
	TrackingToken string           `json:"tracking_token,omitempty"`
	Status        string           `json:"status"`
	PaymentStatus string           `json:"payment_status"`
	TotalRial     int64            `json:"total_rial"`
	CustomerLabel *string          `json:"customer_label"`
	ExpiresAt     time.Time        `json:"expires_at"`
	Expired       bool             `json:"expired"`
	CreatedAt     time.Time        `json:"created_at"`
	ReadyAt       *time.Time       `json:"ready_at"`
	CompletedAt   *time.Time       `json:"completed_at"`
	CancelReason  *string          `json:"cancel_reason"`
	Items         []publicItemView `json:"items"`
}

func optTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func toPublicOrder(d orders.Detail, now time.Time) publicOrderView {
	o := d.Order
	v := publicOrderView{OrderNumber: o.DisplayNumber, TrackingToken: d.Token, Status: o.Status,
		PaymentStatus: o.PaymentStatus.String, TotalRial: o.TotalRial, CustomerLabel: optText(o.CustomerLabel),
		ExpiresAt: o.ExpiresAt.Time, CreatedAt: o.CreatedAt.Time, ReadyAt: optTime(o.ReadyAt),
		CompletedAt: optTime(o.CompletedAt), CancelReason: optText(o.CancelReason), Items: []publicItemView{}}
	v.Expired = o.Status == orders.StatusPending && !now.Before(o.ExpiresAt.Time)
	for _, it := range d.Items {
		v.Items = append(v.Items, publicItemView{ProductID: it.ProductID, Name: it.ProductName, Quantity: it.Quantity,
			UnitPriceRial: it.UnitPriceRial, LineTotalRial: it.LineTotalRial})
	}
	return v
}

type adminItemView struct {
	ID                int64  `json:"id"`
	ProductID         int64  `json:"product_id"`
	Name              string `json:"name"`
	InventoryMode     string `json:"inventory_mode"`
	Quantity          int32  `json:"quantity"`
	UnitPriceRial     int64  `json:"unit_price_rial"`
	LineTotalRial     int64  `json:"line_total_rial"`
	WastedQuantity    int32  `json:"wasted_quantity"`
	CogsRial          *int64 `json:"cogs_rial,omitempty"`           // cost fields: ADMIN only
	EstimatedCostRial *int64 `json:"estimated_cost_rial,omitempty"` // cost fields: ADMIN only
}

type adminOrderView struct {
	ID            int64           `json:"id"`
	OrderNumber   int32           `json:"order_number"`
	BusinessDate  string          `json:"business_date"`
	Status        string          `json:"status"`
	PaymentStatus string          `json:"payment_status"`
	TotalRial     int64           `json:"total_rial"`
	PaidRial      int64           `json:"paid_rial"`
	RefundedRial  int64           `json:"refunded_rial"`
	CustomerLabel *string         `json:"customer_label"`
	CustomerNote  *string         `json:"customer_note"`
	ExpiresAt     time.Time       `json:"expires_at"`
	CreatedAt     time.Time       `json:"created_at"`
	AcceptedAt    *time.Time      `json:"accepted_at"`
	ReadyAt       *time.Time      `json:"ready_at"`
	CompletedAt   *time.Time      `json:"completed_at"`
	CancelledAt   *time.Time      `json:"cancelled_at"`
	CancelReason  *string         `json:"cancel_reason"`
	CancelNote    *string         `json:"cancel_note"`
	AcceptedBy    *int64          `json:"accepted_by"`
	CompletedBy   *int64          `json:"completed_by"`
	CancelledBy   *int64          `json:"cancelled_by"`
	Items         []adminItemView `json:"items"`
}

func toAdminOrder(d orders.Detail, showCost bool) adminOrderView {
	o := d.Order
	v := adminOrderView{ID: o.ID, OrderNumber: o.DisplayNumber, BusinessDate: o.BusinessDate.Time.Format(time.DateOnly),
		Status: o.Status, PaymentStatus: o.PaymentStatus.String, TotalRial: o.TotalRial, PaidRial: o.PaidRial,
		RefundedRial: o.RefundedRial, CustomerLabel: optText(o.CustomerLabel), CustomerNote: optText(o.CustomerNote),
		ExpiresAt: o.ExpiresAt.Time, CreatedAt: o.CreatedAt.Time, AcceptedAt: optTime(o.AcceptedAt), ReadyAt: optTime(o.ReadyAt),
		CompletedAt: optTime(o.CompletedAt), CancelledAt: optTime(o.CancelledAt), CancelReason: optText(o.CancelReason),
		CancelNote: optText(o.CancelNote), AcceptedBy: optInt64(o.AcceptedBy), CompletedBy: optInt64(o.CompletedBy),
		CancelledBy: optInt64(o.CancelledBy), Items: []adminItemView{}}
	for _, it := range d.Items {
		iv := adminItemView{ID: it.ID, ProductID: it.ProductID, Name: it.ProductName, InventoryMode: it.InventoryMode,
			Quantity: it.Quantity, UnitPriceRial: it.UnitPriceRial, LineTotalRial: it.LineTotalRial, WastedQuantity: it.WastedQuantity}
		if showCost {
			iv.CogsRial = optInt64(it.CogsRial)
			iv.EstimatedCostRial = optInt64(it.EstimatedCostRial)
		}
		v.Items = append(v.Items, iv)
	}
	return v
}

// ---- public ----

// POST /public/orders 🔁
func (a *API) createOrder(w http.ResponseWriter, r *http.Request) {
	key, ok := httpx.IdempotencyKey(w, r)
	if !ok {
		return
	}
	ip := a.clientIP(r)
	clientID := strings.ToLower(r.Header.Get(ClientIDHeader))
	// Rate limits are charged inside Create, under the creation lock and after the replay check,
	// so retries of a request that already exists or is still in flight are never limited.
	admit := func() error {
		if ok, wait := a.d.OrderPerIP.Allow(ip.String()); !ok {
			return rateLimitError{wait}
		}
		if clientIDRE.MatchString(clientID) {
			if ok, wait := a.d.OrderPerClient.Allow(clientID); !ok {
				return rateLimitError{wait}
			}
		}
		return nil
	}
	var in struct {
		CustomerLabel string        `json:"customer_label"`
		CustomerNote  string        `json:"customer_note"`
		Items         []orders.Item `json:"items"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	d, created, err := a.d.Orders.Create(r.Context(), orders.CreateInput{
		Key: key, ClientIP: ip, Label: in.CustomerLabel, Note: in.CustomerNote, Items: in.Items, Admit: admit})
	var limited rateLimitError
	if errors.As(err, &limited) {
		rateLimited(w, limited.wait)
		return
	}
	if err != nil {
		a.writeOrderError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, toPublicOrder(d, time.Now()))
}

func (a *API) trackLimit(w http.ResponseWriter, r *http.Request) bool {
	if a.d.TrackPerIP == nil {
		return true
	}
	if ok, wait := a.d.TrackPerIP.Allow(a.clientIP(r).String()); !ok {
		rateLimited(w, wait)
		return false
	}
	return true
}

// GET /public/order — header X-Tracking-Token.
func (a *API) trackOrder(w http.ResponseWriter, r *http.Request) {
	if !a.trackLimit(w, r) {
		return
	}
	d, err := a.d.Orders.Track(r.Context(), r.Header.Get(TrackingHeader))
	if err != nil {
		a.writeOrderError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, toPublicOrder(d, time.Now()))
}

// POST /public/order/cancel — header X-Tracking-Token.
func (a *API) customerCancel(w http.ResponseWriter, r *http.Request) {
	if !a.trackLimit(w, r) {
		return
	}
	d, err := a.d.Orders.CustomerCancel(r.Context(), r.Header.Get(TrackingHeader), a.clientIP(r))
	if err != nil {
		a.writeOrderError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toPublicOrder(d, time.Now()))
}

// ---- admin ----

var validStatuses = map[string]bool{orders.StatusPending: true, orders.StatusPreparing: true, orders.StatusReady: true,
	orders.StatusCompleted: true, orders.StatusCancelled: true}

// GET /admin/orders?status=PENDING,PREPARING&date=YYYY-MM-DD&before_id&limit
func (a *API) listOrders(w http.ResponseWriter, r *http.Request) {
	before, limit, ok := page(w, r)
	if !ok {
		return
	}
	p := store.ListOrdersParams{BeforeID: before, RowLimit: limit}
	if v := r.URL.Query().Get("status"); v != "" {
		for _, s := range strings.Split(v, ",") {
			s = strings.ToUpper(strings.TrimSpace(s))
			if !validStatuses[s] {
				httpx.ValidationError(w, map[string]string{"status": "unknown status " + s})
				return
			}
			p.Statuses = append(p.Statuses, s)
		}
	}
	if p.BusinessDate, ok = parseDate(w, "date", r.URL.Query().Get("date")); !ok {
		return
	}
	list, err := a.d.Orders.List(r.Context(), p)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	showCost := canSeeCost(r)
	out := make([]adminOrderView, len(list))
	for i, d := range list {
		out[i] = toAdminOrder(d, showCost)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out,
		"next_before_id": nextBefore(list, limit, func(d orders.Detail) int64 { return d.Order.ID })})
}

// GET /admin/orders/{id}
func (a *API) getOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	d, err := a.d.Orders.Get(r.Context(), id)
	if err != nil {
		a.writeOrderError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toAdminOrder(d, canSeeCost(r)))
}

func (a *API) transition(op func(*API, *http.Request, int64) (orders.Detail, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		d, err := op(a, r, id)
		if err != nil {
			a.writeOrderError(w, r, err)
			return
		}
		httpx.JSON(w, http.StatusOK, toAdminOrder(d, canSeeCost(r)))
	}
}

func opAccept(a *API, r *http.Request, id int64) (orders.Detail, error) {
	return a.d.Orders.Accept(r.Context(), a.actor(r), id)
}
func opReady(a *API, r *http.Request, id int64) (orders.Detail, error) {
	return a.d.Orders.Ready(r.Context(), a.actor(r), id)
}
func opComplete(a *API, r *http.Request, id int64) (orders.Detail, error) {
	return a.d.Orders.Complete(r.Context(), a.actor(r), id)
}

// POST /admin/orders/{id}/cancel {note?, waste?: [{product_id, quantity}]}
func (a *API) cancelOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Note  string             `json:"note"`
		Waste []orders.WasteLine `json:"waste"`
	}
	if r.ContentLength != 0 && !httpx.DecodeJSON(w, r, &in) {
		return
	}
	d, err := a.d.Orders.Cancel(r.Context(), a.actor(r), id, orders.CancelInput{Note: in.Note, Waste: in.Waste})
	if err != nil {
		a.writeOrderError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toAdminOrder(d, canSeeCost(r)))
}

// PUT /admin/ordering {open} — emergency kill switch (in memory, audited).
func (a *API) setOrdering(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Open *bool `json:"open"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if in.Open == nil {
		httpx.ValidationError(w, map[string]string{"open": "required"})
		return
	}
	if err := a.d.Orders.SetOrderingOpen(r.Context(), a.actor(r), *in.Open); err != nil {
		a.internal(w, r, err)
		return
	}
	now := time.Now()
	httpx.JSON(w, http.StatusOK, map[string]any{"switch_open": a.d.Orders.SwitchOpen(), "open_now": a.d.Orders.IsOpen(now)})
}
