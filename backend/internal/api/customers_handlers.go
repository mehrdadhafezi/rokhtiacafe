package api

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/customer"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/export"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/orders"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/reports"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
)

// Customer history (/customer/*) and manager customer views (/admin/customers, customer
// reports). Ownership is read only from customer_order_links, filtered in SQL by the
// authenticated customer; every response is no-store.

func noStore(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h(w, r)
	}
}

// ---- views ----

type customerItemView struct {
	Name          string `json:"name"`
	Quantity      int32  `json:"quantity"`
	UnitPriceRial int64  `json:"unit_price_rial"`
	LineTotalRial int64  `json:"line_total_rial"`
}

type customerPaymentView struct {
	Kind       string    `json:"kind"`
	Method     string    `json:"method"`
	AmountRial int64     `json:"amount_rial"`
	CreatedAt  time.Time `json:"created_at"`
}

// customerOrderView is an order as its customer (or a manager) sees it: prices as recorded on
// the order, no costs, no tracking token, no staff identities.
type customerOrderView struct {
	ID            int64                 `json:"id"`
	OrderNumber   int32                 `json:"order_number"`
	BusinessDate  string                `json:"business_date"`
	Status        string                `json:"status"`
	PaymentStatus string                `json:"payment_status"`
	TotalRial     int64                 `json:"total_rial"`
	PaidRial      int64                 `json:"paid_rial"`
	RefundedRial  int64                 `json:"refunded_rial"`
	CustomerLabel *string               `json:"customer_label"`
	CustomerNote  *string               `json:"customer_note"`
	Expired       bool                  `json:"expired"`
	CreatedAt     time.Time             `json:"created_at"`
	CompletedAt   *time.Time            `json:"completed_at"`
	CancelledAt   *time.Time            `json:"cancelled_at"`
	CancelReason  *string               `json:"cancel_reason"`
	Items         []customerItemView    `json:"items"`
	Payments      []customerPaymentView `json:"payments,omitempty"`
}

func toCustomerOrder(o store.Order, items []store.OrderItem, pays []store.Payment, now time.Time) customerOrderView {
	v := customerOrderView{ID: o.ID, OrderNumber: o.DisplayNumber, BusinessDate: o.BusinessDate.Time.Format(time.DateOnly),
		Status: o.Status, PaymentStatus: o.PaymentStatus.String, TotalRial: o.TotalRial, PaidRial: o.PaidRial,
		RefundedRial: o.RefundedRial, CustomerLabel: optText(o.CustomerLabel), CustomerNote: optText(o.CustomerNote),
		CreatedAt: o.CreatedAt.Time, CompletedAt: optTime(o.CompletedAt), CancelledAt: optTime(o.CancelledAt),
		CancelReason: optText(o.CancelReason), Items: []customerItemView{}}
	v.Expired = o.Status == orders.StatusPending && !now.Before(o.ExpiresAt.Time)
	for _, it := range items {
		v.Items = append(v.Items, customerItemView{Name: it.ProductName, Quantity: it.Quantity,
			UnitPriceRial: it.UnitPriceRial, LineTotalRial: it.LineTotalRial})
	}
	for _, p := range pays {
		if p.VoidedAt.Valid {
			continue // a voided row was a recording mistake, not money
		}
		v.Payments = append(v.Payments, customerPaymentView{Kind: p.Kind, Method: p.Method, AmountRial: p.AmountRial,
			CreatedAt: p.CreatedAt.Time})
	}
	return v
}

// ordersWithItems loads the lines of a page of orders in one query.
func (a *API) ordersWithItems(r *http.Request, list []store.Order) ([]customerOrderView, error) {
	out := make([]customerOrderView, 0, len(list))
	if len(list) == 0 {
		return out, nil
	}
	ids := make([]int64, len(list))
	for i, o := range list {
		ids[i] = o.ID
	}
	items, err := a.q.ListOrderItemsForOrders(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	byOrder := map[int64][]store.OrderItem{}
	for _, it := range items {
		byOrder[it.OrderID] = append(byOrder[it.OrderID], it)
	}
	now := time.Now()
	for _, o := range list {
		out = append(out, toCustomerOrder(o, byOrder[o.ID], nil, now))
	}
	return out, nil
}

// ---- the customer's own history ----

// GET /customer/orders?before_id&limit
func (a *API) customerOrders(w http.ResponseWriter, r *http.Request) {
	p := customer.FromContext(r.Context())
	before, limit, ok := page(w, r)
	if !ok {
		return
	}
	rows, err := a.q.ListCustomerOrders(r.Context(), store.ListCustomerOrdersParams{CustomerID: p.ID, BeforeID: before, RowLimit: limit})
	if err != nil {
		a.internal(w, r, err)
		return
	}
	list := make([]store.Order, len(rows))
	for i, row := range rows {
		list[i] = row.Order
	}
	out, err := a.ordersWithItems(r, list)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out,
		"next_before_id": nextBefore(list, limit, func(o store.Order) int64 { return o.ID })})
}

// GET /customer/orders/{id} — any order that is not the caller's is a uniform 404.
func (a *API) customerOrder(w http.ResponseWriter, r *http.Request) {
	p := customer.FromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	row, err := a.q.GetCustomerOrder(r.Context(), store.GetCustomerOrderParams{CustomerID: p.ID, OrderID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "not found")
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	items, err := a.q.ListOrderItems(r.Context(), row.Order.ID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	pays, err := a.q.ListOrderPayments(r.Context(), row.Order.ID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toCustomerOrder(row.Order, items, pays, time.Now()))
}

type customerEventView struct {
	ID          int64     `json:"id"`
	Type        string    `json:"type"`
	CreatedAt   time.Time `json:"created_at"`
	OrderID     *int64    `json:"order_id"`
	OrderNumber *int32    `json:"order_number"`
}

// GET /customer/activity?before_id&limit — curated events, never IPs or user agents.
func (a *API) customerActivity(w http.ResponseWriter, r *http.Request) {
	p := customer.FromContext(r.Context())
	before, limit, ok := page(w, r)
	if !ok {
		return
	}
	rows, err := a.q.ListCustomerEvents(r.Context(), store.ListCustomerEventsParams{CustomerID: p.ID, BeforeID: before, RowLimit: limit})
	if err != nil {
		a.internal(w, r, err)
		return
	}
	out := make([]customerEventView, len(rows))
	for i, e := range rows {
		out[i] = customerEventView{ID: e.ID, Type: e.EventType, CreatedAt: e.CreatedAt.Time, OrderID: optInt64(e.OrderID)}
		if e.DisplayNumber.Valid {
			n := e.DisplayNumber.Int32
			out[i].OrderNumber = &n
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out,
		"next_before_id": nextBefore(rows, limit, func(e store.ListCustomerEventsRow) int64 { return e.ID })})
}

// ---- managers: customers (ADMIN only, customers.view) ----

type adminCustomerView struct {
	ID                    int64      `json:"id"`
	FirstName             string     `json:"first_name"`
	LastName              string     `json:"last_name"`
	Phone                 string     `json:"phone"`
	PhoneVerified         bool       `json:"phone_verified"`
	IsActive              bool       `json:"is_active"`
	CreatedAt             time.Time  `json:"created_at"`
	LastLoginAt           *time.Time `json:"last_login_at"`
	Orders                int64      `json:"orders"`
	CompletedOrders       int64      `json:"completed_orders"`
	CompletedGrossRial    int64      `json:"completed_gross_rial"`
	CompletedRefundedRial int64      `json:"completed_refunded_rial"`
	LastOrderAt           *time.Time `json:"last_order_at"`
}

// searchTerm normalises ?search: a phone-like value becomes its digits; LIKE wildcards are
// escaped by stripping them (search is a plain substring match).
func searchTerm(v string) pgtype.Text {
	v = strings.TrimSpace(v)
	if v == "" {
		return pgtype.Text{}
	}
	if p := customer.NormalizePhone(v); p != "" {
		v = p
	} else if d := digitsOnly(v); d != "" && len(d) == len([]rune(strings.ReplaceAll(v, " ", ""))) {
		v = d // a partial phone typed with Persian digits
	}
	v = strings.NewReplacer("%", "", "_", "", `\`, "").Replace(v)
	if r := []rune(v); len(r) > 60 {
		v = string(r[:60])
	}
	if v == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: v, Valid: true}
}

func digitsOnly(v string) string {
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= '۰' && r <= '۹':
			b.WriteRune(r - '۰' + '0')
		case r >= '٠' && r <= '٩':
			b.WriteRune(r - '٠' + '0')
		case r == ' ':
		default:
			return ""
		}
	}
	return b.String()
}

// GET /admin/customers?search&before_id&limit
func (a *API) listCustomers(w http.ResponseWriter, r *http.Request) {
	before, limit, ok := page(w, r)
	if !ok {
		return
	}
	rows, err := a.q.AdminListCustomers(r.Context(), store.AdminListCustomersParams{
		Search: searchTerm(r.URL.Query().Get("search")), BeforeID: before, RowLimit: limit})
	if err != nil {
		a.internal(w, r, err)
		return
	}
	out := make([]adminCustomerView, len(rows))
	for i, c := range rows {
		out[i] = adminCustomerView{ID: c.ID, FirstName: c.FirstName, LastName: c.LastName, Phone: c.Phone,
			IsActive: c.IsActive, CreatedAt: c.CreatedAt.Time, LastLoginAt: optTime(c.LastLoginAt), Orders: c.Orders,
			CompletedOrders: c.CompletedOrders, CompletedGrossRial: c.CompletedGrossRial,
			CompletedRefundedRial: c.CompletedRefundedRial, LastOrderAt: optTime(c.LastOrderAt)}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out,
		"next_before_id": nextBefore(rows, limit, func(c store.AdminListCustomersRow) int64 { return c.ID })})
}

// GET /admin/customers/{id}
func (a *API) getCustomer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, err := a.q.AdminGetCustomer(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "not found")
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	t, err := a.q.AdminCustomerTotals(r.Context(), id)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"customer": adminCustomerView{ID: c.ID, FirstName: c.FirstName, LastName: c.LastName, Phone: c.Phone,
			PhoneVerified: c.PhoneVerifiedAt.Valid, IsActive: c.IsActive, CreatedAt: c.CreatedAt.Time,
			LastLoginAt: optTime(c.LastLoginAt), Orders: t.Orders, CompletedOrders: t.CompletedOrders,
			CompletedGrossRial: t.CompletedGrossRial, CompletedRefundedRial: t.CompletedRefundedRial},
		"totals": map[string]int64{
			"orders": t.Orders, "completed_orders": t.CompletedOrders, "cancelled_orders": t.CancelledOrders,
			"open_orders": t.OpenOrders, "completed_gross_rial": t.CompletedGrossRial,
			"completed_refunded_rial": t.CompletedRefundedRial,
			"completed_revenue_rial":  t.CompletedGrossRial - t.CompletedRefundedRial,
			"receivable_rial":         t.ReceivableRial,
			// From the payment ledger: refunds are subtracted, voided rows are ignored.
			"payments_received_rial": t.PaymentsReceivedRial, "refunds_recorded_rial": t.RefundsRecordedRial,
			"net_collected_rial": t.PaymentsReceivedRial - t.RefundsRecordedRial},
	})
}

// customerRange parses ?from&to; both absent means the last MaxRangeDays business dates.
func (a *API) customerRange(w http.ResponseWriter, r *http.Request) (reports.Range, bool) {
	q := r.URL.Query()
	if q.Get("from") == "" && q.Get("to") == "" {
		today := a.reports.Today().To
		return reports.Range{From: today.AddDate(0, 0, -(reports.MaxRangeDays - 1)), To: today}, true
	}
	return reportRange(w, r)
}

// GET /admin/customers/{id}/orders?from&to&before_id&limit (orders created in the range)
func (a *API) customerOrdersAdmin(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rng, ok := a.customerRange(w, r)
	if !ok {
		return
	}
	before, limit, ok := page(w, r)
	if !ok {
		return
	}
	if _, err := a.q.AdminGetCustomer(r.Context(), id); errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "not found")
		return
	} else if err != nil {
		a.internal(w, r, err)
		return
	}
	from, to := a.reports.Window(rng)
	rows, err := a.q.AdminListCustomerOrders(r.Context(), store.AdminListCustomerOrdersParams{
		CustomerID: id, FromTs: from, ToTs: to, BeforeID: before, RowLimit: limit})
	if err != nil {
		a.internal(w, r, err)
		return
	}
	list := make([]store.Order, len(rows))
	for i, row := range rows {
		list[i] = row.Order
	}
	out, err := a.ordersWithItems(r, list)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"from": rng.From.Format(time.DateOnly), "to": rng.To.Format(time.DateOnly),
		"items": out, "next_before_id": nextBefore(list, limit, func(o store.Order) int64 { return o.ID })})
}

// ---- customer reports and exports ----

func (a *API) customerReport(w http.ResponseWriter, r *http.Request) (reports.CustomerReport, bool) {
	rng, ok := reportRange(w, r)
	if !ok {
		return reports.CustomerReport{}, false
	}
	rep, err := a.reports.Customers(r.Context(), rng)
	if errors.Is(err, reports.ErrTooManyRows) {
		httpx.ValidationError(w, map[string]string{"to": "too many customers in this range; choose a shorter range"})
		return rep, false
	}
	if err != nil {
		a.internal(w, r, err)
		return rep, false
	}
	return rep, true
}

// GET /admin/reports/customers?from&to
func (a *API) reportCustomers(w http.ResponseWriter, r *http.Request) {
	if rep, ok := a.customerReport(w, r); ok {
		httpx.JSON(w, http.StatusOK, rep)
	}
}

var customerReportHeader = []string{
	"شناسه مشتری", "نام", "نام خانوادگی", "موبایل",
	"سفارش ثبت‌شده", "سفارش تکمیل‌شده", "سفارش لغوشده", "سفارش باز (اکنون)",
	"فروش ناخالص (ریال)", "بازپرداخت فروش (ریال)", "درآمد (ریال)", "بازپرداخت پیش‌پرداخت (ریال)",
	"پرداخت دریافتی (ریال)", "بازپرداخت ثبت‌شده (ریال)", "خالص دریافتی (ریال)", "مطالبات (ریال، اکنون)",
}

// customerDefinitionsFA are the Persian definitions shown in exports (same meaning as
// reports.CustomerDefinitions).
var customerDefinitionsFA = [][2]string{
	{"سفارش ثبت‌شده", "سفارش‌هایی که مشتری در بازه ثبت کرده، با هر وضعیت"},
	{"سفارش تکمیل‌شده", "سفارش‌هایی که در بازه تکمیل شده‌اند"},
	{"سفارش لغوشده", "سفارش‌هایی که در بازه لغو شده‌اند (هرگز درآمد نیستند)"},
	{"سفارش باز", "سفارش‌های در انتظار، در حال آماده‌سازی یا آماده، در لحظهٔ گزارش؛ به بازه محدود نیست و سفارش‌های ثبت‌شده پیش از بازه را هم می‌شمارد"},
	{"فروش ناخالص", "جمع مبلغ سفارش‌هایی که در بازه تکمیل شده‌اند (درآمد در زمان تکمیل شناسایی می‌شود)"},
	{"بازپرداخت فروش", "ردیف‌های بازپرداخت سفارش‌های تکمیل‌شده که در بازه ثبت شده‌اند"},
	{"درآمد", "فروش ناخالص منهای بازپرداخت فروش"},
	{"بازپرداخت پیش‌پرداخت", "بازپرداخت پیش‌پرداخت سفارش‌های لغوشده در بازه (فقط جریان نقدی، نه درآمد)"},
	{"پرداخت دریافتی", "پرداخت‌های باطل‌نشدهٔ ثبت‌شده در بازه"},
	{"بازپرداخت ثبت‌شده", "همهٔ ردیف‌های بازپرداخت باطل‌نشدهٔ ثبت‌شده در بازه"},
	{"خالص دریافتی", "پرداخت دریافتی منهای بازپرداخت ثبت‌شده"},
	{"مطالبات", "سفارش‌های تکمیل‌شدهٔ بدون بازپرداخت که کامل پرداخت نشده‌اند، در لحظهٔ گزارش؛ به بازه محدود نیست"},
}

func customerReportSheets(rep reports.CustomerReport) []export.Sheet {
	main := export.Sheet{Name: "مشتریان", Header: customerReportHeader}
	figures := func(f reports.CustomerFigures) []export.Cell {
		return []export.Cell{export.N(f.OrdersPlaced), export.N(f.CompletedOrders), export.N(f.CancelledOrders),
			export.N(f.OpenOrders), export.N(f.GrossSalesRial), export.N(f.SalesRefundsRial), export.N(f.RevenueRial),
			export.N(f.PrepaymentRefundsRial), export.N(f.PaymentsReceivedRial), export.N(f.RefundsRecordedRial),
			export.N(f.NetCollectedRial), export.N(f.ReceivableRial)}
	}
	for _, c := range rep.Customers {
		row := []export.Cell{export.N(c.CustomerID), export.T(c.FirstName), export.T(c.LastName), export.T(c.Phone)}
		main.Rows = append(main.Rows, append(row, figures(c.CustomerFigures)...))
	}
	main.Rows = append(main.Rows, append([]export.Cell{export.T("جمع"), export.T(""), export.T(""), export.T("")}, figures(rep.Totals)...))
	defs := export.Sheet{Name: "تعاریف", Header: []string{"شاخص", "تعریف"}}
	defs.Rows = append(defs.Rows,
		[]export.Cell{export.T("بازه"), export.T(rep.From + " تا " + rep.To + " (" + rep.Timezone + ")")},
		[]export.Cell{export.T("انتساب"), export.T("فقط سفارش‌هایی که هنگام ثبت به حساب مشتری متصل شده‌اند؛ سفارش‌های مهمان در این گزارش نیستند")})
	for _, d := range customerDefinitionsFA {
		defs.Rows = append(defs.Rows, []export.Cell{export.T(d[0]), export.T(d[1])})
	}
	return []export.Sheet{main, defs}
}

// download buffers an export and sends it as an attachment (no-store, ASCII file name).
func (a *API) download(w http.ResponseWriter, r *http.Request, name, format string, sheets []export.Sheet) {
	var buf bytes.Buffer
	var err error
	ctype := ""
	switch format {
	case "csv":
		ctype = "text/csv; charset=utf-8"
		err = export.WriteCSV(&buf, sheets[0])
	case "xlsx":
		ctype = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		err = export.WriteXLSX(&buf, true, sheets...)
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.%s"`, name, format))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// GET /admin/reports/customers.csv?from&to and .xlsx
func (a *API) reportCustomersExport(format string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rep, ok := a.customerReport(w, r)
		if !ok {
			return
		}
		a.download(w, r, "rokh-customers-"+rep.From+"_"+rep.To, format, customerReportSheets(rep))
	}
}

var customerLinesHeader = []string{
	"شناسه سفارش", "شماره سفارش", "تاریخ کاری", "زمان ثبت", "زمان تکمیل", "وضعیت", "وضعیت پرداخت",
	"جمع سفارش (ریال)", "پرداخت‌شده (ریال)", "بازپرداخت‌شده (ریال)",
	"کالا", "تعداد", "قیمت واحد ثبت‌شده (ریال)", "جمع سطر (ریال)",
}

// GET /admin/reports/customers/{id}/orders.csv?from&to and .xlsx (order lines, prices as recorded)
func (a *API) reportCustomerLinesExport(format string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		rng, ok := reportRange(w, r)
		if !ok {
			return
		}
		c, err := a.q.AdminGetCustomer(r.Context(), id)
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "not found")
			return
		}
		if err != nil {
			a.internal(w, r, err)
			return
		}
		rows, err := a.reports.CustomerLines(r.Context(), id, rng)
		if errors.Is(err, reports.ErrTooManyRows) {
			httpx.ValidationError(w, map[string]string{"to": "too many order lines in this range; choose a shorter range"})
			return
		}
		if err != nil {
			a.internal(w, r, err)
			return
		}
		loc := a.d.Location
		ts := func(t pgtype.Timestamptz) export.Cell {
			if !t.Valid {
				return export.T("")
			}
			return export.T(t.Time.In(loc).Format("2006-01-02 15:04"))
		}
		sheet := export.Sheet{Name: "سفارش‌ها", Header: customerLinesHeader}
		for _, l := range rows {
			sheet.Rows = append(sheet.Rows, []export.Cell{export.N(l.OrderID), export.N(int64(l.DisplayNumber)),
				export.T(l.BusinessDate.Time.Format(time.DateOnly)), ts(l.CreatedAt), ts(l.CompletedAt),
				export.T(l.Status), export.T(l.PaymentStatus), export.N(l.TotalRial), export.N(l.PaidRial),
				export.N(l.RefundedRial), export.T(l.ProductName), export.N(int64(l.Quantity)),
				export.N(l.UnitPriceRial), export.N(l.LineTotalRial)})
		}
		profile := export.Sheet{Name: "مشتری", Header: []string{"فیلد", "مقدار"}, Rows: [][]export.Cell{
			{export.T("نام"), export.T(c.FirstName + " " + c.LastName)},
			{export.T("موبایل (تأییدنشده)"), export.T(c.Phone)},
			{export.T("بازه"), export.T(rng.From.Format(time.DateOnly) + " تا " + rng.To.Format(time.DateOnly) + " (" + loc.String() + ")")},
			{export.T("توضیح"), export.T("سفارش‌های ثبت‌شده در بازه با همهٔ وضعیت‌ها؛ لغوشده‌ها درآمد نیستند")},
		}}
		a.download(w, r, fmt.Sprintf("rokh-customer-%d-%s_%s", id, rng.From.Format(time.DateOnly), rng.To.Format(time.DateOnly)),
			format, []export.Sheet{sheet, profile})
	}
}

// routeCustomers registers the customer-facing routes under /customer.
func (a *API) routeCustomers(r chi.Router) {
	r.Mount("/auth", a.customers.AuthHandler())
	r.Group(func(r chi.Router) {
		r.Use(a.customers.RequireCustomer)
		r.Get("/orders", a.customerOrders)
		r.Get("/orders/{id}", a.customerOrder)
		r.Get("/activity", a.customerActivity)
	})
}
