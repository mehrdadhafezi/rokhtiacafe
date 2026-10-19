package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/inventory"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
)

func (a *API) writeInventoryError(w http.ResponseWriter, r *http.Request, err error) {
	var bad inventory.ValidationError
	switch {
	case errors.As(err, &bad):
		httpx.ValidationError(w, bad)
	case errors.Is(err, inventory.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "not found")
	case errors.Is(err, inventory.ErrInsufficientStock):
		httpx.Error(w, http.StatusConflict, "INSUFFICIENT_STOCK", "not enough available stock")
	case errors.Is(err, inventory.ErrPurchaseConsumed):
		httpx.Error(w, http.StatusConflict, "PURCHASE_ALREADY_CONSUMED", "stock has left since this purchase; correct it with a count adjustment")
	case errors.Is(err, inventory.ErrIdempotencyReused):
		httpx.Error(w, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key was used for a different request")
	default:
		a.internal(w, r, err)
	}
}

func optInt64(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	x := v.Int64
	return &x
}

func optText(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	x := v.String
	return &x
}

// ---- balances ----

type inventoryView struct {
	ProductID          int64  `json:"product_id"`
	Name               string `json:"name"`
	IsActive           bool   `json:"is_active"`
	OnHand             int32  `json:"on_hand"`
	Reserved           int32  `json:"reserved"`
	Available          int32  `json:"available"`
	Threshold          *int32 `json:"threshold"`
	Low                bool   `json:"low"`
	AvgUnitCostRial    *int64 `json:"avg_unit_cost_rial,omitempty"` // cost fields: ADMIN only
	InventoryValueRial *int64 `json:"inventory_value_rial,omitempty"`
	LastUnitCostRial   *int64 `json:"last_unit_cost_rial,omitempty"`
}

// GET /admin/inventory
func (a *API) listInventory(w http.ResponseWriter, r *http.Request) {
	rows, err := a.inventory.ListInventory(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	showCost := canSeeCost(r)
	out := make([]inventoryView, len(rows))
	for i, row := range rows {
		v := inventoryView{ProductID: row.ProductID, Name: row.Name, IsActive: row.IsActive, OnHand: row.OnHand,
			Reserved: row.Reserved, Available: row.OnHand - row.Reserved}
		if row.LowStockThreshold.Valid {
			t := row.LowStockThreshold.Int32
			v.Threshold = &t
			v.Low = v.Available <= t
		}
		if showCost {
			value := row.InventoryValueRial
			v.InventoryValueRial = &value
			if row.OnHand > 0 {
				avg := (value + int64(row.OnHand)/2) / int64(row.OnHand)
				v.AvgUnitCostRial = &avg
			}
			v.LastUnitCostRial = optInt64(row.LastUnitCostRial)
		}
		out[i] = v
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out})
}

// ---- movements ----

type movementView struct {
	ID              int64     `json:"id"`
	ProductID       int64     `json:"product_id"`
	MovementType    string    `json:"movement_type"`
	ReasonCode      *string   `json:"reason_code"`
	QuantityDelta   int32     `json:"quantity_delta"`
	ValueDeltaRial  int64     `json:"value_delta_rial"`
	OnHandAfter     int32     `json:"on_hand_after"`
	ValueAfterRial  int64     `json:"value_after_rial"`
	OrderID         *int64    `json:"order_id"`
	PurchaseEntryID *int64    `json:"purchase_entry_id"`
	Note            *string   `json:"note"`
	ActorUserID     *int64    `json:"actor_user_id"`
	CreatedAt       time.Time `json:"created_at"`
}

func toMovement(m store.InventoryMovement) movementView {
	return movementView{ID: m.ID, ProductID: m.ProductID, MovementType: m.MovementType, ReasonCode: optText(m.ReasonCode),
		QuantityDelta: m.QuantityDelta, ValueDeltaRial: m.ValueDeltaRial, OnHandAfter: m.OnHandAfter,
		ValueAfterRial: m.ValueAfterRial, OrderID: optInt64(m.OrderID), PurchaseEntryID: optInt64(m.PurchaseEntryID),
		Note: optText(m.Note), ActorUserID: optInt64(m.ActorUserID), CreatedAt: m.CreatedAt.Time}
}

// page parses ?limit (1-100, default 50) and ?before_id.
func page(w http.ResponseWriter, r *http.Request) (pgtype.Int8, int32, bool) {
	q := r.URL.Query()
	limit := int32(50)
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			httpx.ValidationError(w, map[string]string{"limit": "1-100"})
			return pgtype.Int8{}, 0, false
		}
		limit = int32(n)
	}
	var before pgtype.Int8
	if v := q.Get("before_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			httpx.ValidationError(w, map[string]string{"before_id": "invalid"})
			return before, 0, false
		}
		before = pgtype.Int8{Int64: n, Valid: true}
	}
	return before, limit, true
}

func nextBefore[T any](items []T, limit int32, id func(T) int64) *int64 {
	if len(items) < int(limit) || len(items) == 0 {
		return nil
	}
	v := id(items[len(items)-1])
	return &v
}

// GET /admin/inventory/{id}/movements
func (a *API) listMovements(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	before, limit, ok := page(w, r)
	if !ok {
		return
	}
	rows, err := a.inventory.ListMovements(r.Context(), id, before, limit)
	if err != nil {
		a.writeInventoryError(w, r, err)
		return
	}
	out := make([]movementView, len(rows))
	for i, m := range rows {
		out[i] = toMovement(m)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out,
		"next_before_id": nextBefore(rows, limit, func(m store.InventoryMovement) int64 { return m.ID })})
}

// POST /admin/inventory/{id}/adjustments 🔁
func (a *API) adjustInventory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	key, ok := httpx.IdempotencyKey(w, r)
	if !ok {
		return
	}
	var in struct {
		Direction    string `json:"direction"`
		Quantity     int32  `json:"quantity"`
		UnitCostRial *int64 `json:"unit_cost_rial"`
		ReasonCode   string `json:"reason_code"`
		Note         string `json:"note"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	m, replay, err := a.inventory.Adjust(r.Context(), a.actor(r), key, id, inventory.AdjustInput{
		Direction: in.Direction, Quantity: in.Quantity, UnitCostRial: in.UnitCostRial, ReasonCode: in.ReasonCode, Note: in.Note,
	})
	if err != nil {
		a.writeInventoryError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	httpx.JSON(w, status, toMovement(m))
}

// ---- purchases ----

type purchaseView struct {
	ID            int64      `json:"id"`
	ProductID     int64      `json:"product_id"`
	Quantity      int32      `json:"quantity"`
	UnitCostRial  int64      `json:"unit_cost_rial"`
	TotalCostRial int64      `json:"total_cost_rial"`
	Supplier      *string    `json:"supplier"`
	PurchasedOn   string     `json:"purchased_on"`
	Note          *string    `json:"note"`
	CreatedBy     int64      `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
	VoidedAt      *time.Time `json:"voided_at"`
	VoidedBy      *int64     `json:"voided_by"`
	VoidReason    *string    `json:"void_reason"`
}

func toPurchase(p store.PurchaseEntry) purchaseView {
	v := purchaseView{ID: p.ID, ProductID: p.ProductID, Quantity: p.Quantity, UnitCostRial: p.UnitCostRial,
		TotalCostRial: p.TotalCostRial, Supplier: optText(p.Supplier), PurchasedOn: p.PurchasedOn.Time.Format("2006-01-02"),
		Note: optText(p.Note), CreatedBy: p.CreatedBy, CreatedAt: p.CreatedAt.Time, VoidedBy: optInt64(p.VoidedBy),
		VoidReason: optText(p.VoidReason)}
	if p.VoidedAt.Valid {
		t := p.VoidedAt.Time
		v.VoidedAt = &t
	}
	return v
}

func parseDate(w http.ResponseWriter, field, v string) (pgtype.Date, bool) {
	if v == "" {
		return pgtype.Date{}, true
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		httpx.ValidationError(w, map[string]string{field: "YYYY-MM-DD"})
		return pgtype.Date{}, false
	}
	return pgtype.Date{Time: t, Valid: true}, true
}

// GET /admin/purchases?product_id&from&to&before_id&limit
func (a *API) listPurchases(w http.ResponseWriter, r *http.Request) {
	before, limit, ok := page(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	p := store.ListPurchasesParams{BeforeID: before, RowLimit: limit}
	if v := q.Get("product_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			httpx.ValidationError(w, map[string]string{"product_id": "invalid"})
			return
		}
		p.ProductID = pgtype.Int8{Int64: n, Valid: true}
	}
	if p.FromDate, ok = parseDate(w, "from", q.Get("from")); !ok {
		return
	}
	if p.ToDate, ok = parseDate(w, "to", q.Get("to")); !ok {
		return
	}
	rows, err := a.inventory.ListPurchases(r.Context(), p)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	out := make([]purchaseView, len(rows))
	for i, pe := range rows {
		out[i] = toPurchase(pe)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out,
		"next_before_id": nextBefore(rows, limit, func(p store.PurchaseEntry) int64 { return p.ID })})
}

// POST /admin/purchases 🔁
func (a *API) createPurchase(w http.ResponseWriter, r *http.Request) {
	key, ok := httpx.IdempotencyKey(w, r)
	if !ok {
		return
	}
	var in struct {
		ProductID    int64  `json:"product_id"`
		Quantity     int32  `json:"quantity"`
		UnitCostRial int64  `json:"unit_cost_rial"`
		PurchasedOn  string `json:"purchased_on"`
		Supplier     string `json:"supplier"`
		Note         string `json:"note"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	pe, replay, err := a.inventory.Purchase(r.Context(), a.actor(r), key, inventory.PurchaseInput(in))
	if err != nil {
		a.writeInventoryError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	httpx.JSON(w, status, toPurchase(pe))
}

// POST /admin/purchases/{id}/void
func (a *API) voidPurchase(w http.ResponseWriter, r *http.Request) {
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
	pe, err := a.inventory.VoidPurchase(r.Context(), a.actor(r), id, in.Reason)
	if err != nil {
		a.writeInventoryError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toPurchase(pe))
}
