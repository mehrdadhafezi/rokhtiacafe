package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/auth"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/expenses"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/reports"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
)

// ---- expenses ----

type expenseView struct {
	ID          int64      `json:"id"`
	Category    string     `json:"category"`
	AmountRial  int64      `json:"amount_rial"`
	ExpenseDate string     `json:"expense_date"`
	Description string     `json:"description"`
	CreatedBy   int64      `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	VoidedAt    *time.Time `json:"voided_at"`
	VoidedBy    *int64     `json:"voided_by"`
	VoidReason  *string    `json:"void_reason"`
}

func toExpense(e store.Expense) expenseView {
	return expenseView{ID: e.ID, Category: e.Category, AmountRial: e.AmountRial, ExpenseDate: e.ExpenseDate.Time.Format(time.DateOnly),
		Description: e.Description, CreatedBy: e.CreatedBy, CreatedAt: e.CreatedAt.Time, VoidedAt: optTime(e.VoidedAt),
		VoidedBy: optInt64(e.VoidedBy), VoidReason: optText(e.VoidReason)}
}

func (a *API) writeExpenseError(w http.ResponseWriter, r *http.Request, err error) {
	var bad expenses.ValidationError
	switch {
	case errors.As(err, &bad):
		httpx.ValidationError(w, bad)
	case errors.Is(err, expenses.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "expense not found")
	case errors.Is(err, expenses.ErrIdempotencyReused):
		httpx.Error(w, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key was used for a different request")
	default:
		a.internal(w, r, err)
	}
}

// GET /admin/expenses?from&to&category&before_id&limit
func (a *API) listExpenses(w http.ResponseWriter, r *http.Request) {
	before, limit, ok := page(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	p := store.ListExpensesParams{BeforeID: before, RowLimit: limit}
	if p.FromDate, ok = parseDate(w, "from", q.Get("from")); !ok {
		return
	}
	if p.ToDate, ok = parseDate(w, "to", q.Get("to")); !ok {
		return
	}
	if c := q.Get("category"); c != "" {
		if !expenses.Categories[c] {
			httpx.ValidationError(w, map[string]string{"category": "unknown"})
			return
		}
		p.Category = pgtype.Text{String: c, Valid: true}
	}
	rows, err := a.expenses.List(r.Context(), p)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	out := make([]expenseView, len(rows))
	for i, e := range rows {
		out[i] = toExpense(e)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out,
		"next_before_id": nextBefore(rows, limit, func(e store.Expense) int64 { return e.ID })})
}

// POST /admin/expenses 🔁 {category, amount_rial, expense_date, description}
func (a *API) createExpense(w http.ResponseWriter, r *http.Request) {
	key, ok := httpx.IdempotencyKey(w, r)
	if !ok {
		return
	}
	var in struct {
		Category    string `json:"category"`
		AmountRial  int64  `json:"amount_rial"`
		ExpenseDate string `json:"expense_date"`
		Description string `json:"description"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	e, replay, err := a.expenses.Create(r.Context(), a.actor(r), key, expenses.Input(in))
	if err != nil {
		a.writeExpenseError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	httpx.JSON(w, status, toExpense(e))
}

// POST /admin/expenses/{id}/void {reason}
func (a *API) voidExpense(w http.ResponseWriter, r *http.Request) {
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
	e, err := a.expenses.Void(r.Context(), a.actor(r), id, in.Reason)
	if err != nil {
		a.writeExpenseError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toExpense(e))
}

// ---- reports ----

func reportRange(w http.ResponseWriter, r *http.Request) (reports.Range, bool) {
	rng, err := reports.ParseRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		httpx.ValidationError(w, map[string]string{"from": "YYYY-MM-DD", "to": "YYYY-MM-DD, not before from, at most 366 days"})
		return rng, false
	}
	return rng, true
}

// GET /admin/reports/summary?from=YYYY-MM-DD&to=YYYY-MM-DD (business dates, inclusive)
func (a *API) reportSummary(w http.ResponseWriter, r *http.Request) {
	rng, ok := reportRange(w, r)
	if !ok {
		return
	}
	s, err := a.reports.Summary(r.Context(), rng)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

// GET /admin/reports/products?from&to
func (a *API) reportProducts(w http.ResponseWriter, r *http.Request) {
	rng, ok := reportRange(w, r)
	if !ok {
		return
	}
	lines, err := a.reports.Products(r.Context(), rng)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"from": rng.From.Format(time.DateOnly), "to": rng.To.Format(time.DateOnly), "items": lines})
}

// GET /admin/reports/receivables
func (a *API) reportReceivables(w http.ResponseWriter, r *http.Request) {
	items, err := a.reports.Receivables(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	var total int64
	for _, it := range items {
		total += it.OutstandingRial
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items, "total_outstanding_rial": total})
}

// ---- dashboard ----

type lowStockView struct {
	ProductID int64  `json:"product_id"`
	Name      string `json:"name"`
	Available int32  `json:"available"`
	Threshold int32  `json:"threshold"`
}

// GET /admin/dashboard — operational view; today's revenue only with dashboard.revenue (ADMIN).
func (a *API) dashboard(w http.ResponseWriter, r *http.Request) {
	q := store.New(a.d.Pool)
	counts, err := q.DashboardOpenCounts(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	open := map[string]int64{"PENDING": 0, "PREPARING": 0, "READY": 0}
	for _, c := range counts {
		open[c.Status] = c.N
	}
	now := time.Now().In(a.d.Location)
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, a.d.Location)
	todayOrders, err := q.DashboardOrdersSince(r.Context(), pgtype.Timestamptz{Time: startOfDay, Valid: true})
	if err != nil {
		a.internal(w, r, err)
		return
	}
	low, err := q.DashboardLowStock(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	lowView := make([]lowStockView, len(low))
	for i, l := range low {
		lowView[i] = lowStockView{ProductID: l.ProductID, Name: l.Name, Available: l.Available, Threshold: l.Threshold}
	}
	out := map[string]any{"open_counts": open, "today_orders": todayOrders, "low_stock": lowView, "ordering_open": a.orderingOpen(now)}
	if auth.Can(auth.FromContext(r.Context()).Role, auth.ActDashboardRevenue) {
		s, err := a.reports.Summary(r.Context(), a.reports.Today())
		if err != nil {
			a.internal(w, r, err)
			return
		}
		out["today_revenue_rial"] = s.Totals.RevenueRial
	}
	httpx.JSON(w, http.StatusOK, out)
}
