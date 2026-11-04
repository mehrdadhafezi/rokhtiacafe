package api_test

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// C5: manager customer views, customer reports and spreadsheet exports.

// get fetches a path and returns the raw body (for CSV and XLSX downloads).
func (c *client) get(path string) (*http.Response, []byte) {
	c.e.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.e.srv.URL+path, nil)
	req.Header.Set("Origin", origin)
	resp, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// orderFlow moves an order through the staff steps given ("accept", "pay:CASH:950000", "ready",
// "complete", "refund", "cancel").
func orderFlow(t *testing.T, admin *client, id float64, steps ...string) {
	t.Helper()
	for _, s := range steps {
		var resp *http.Response
		var body map[string]any
		switch {
		case strings.HasPrefix(s, "pay:"):
			parts := strings.Split(s, ":")
			var amount int
			fmt.Sscan(parts[2], &amount)
			resp, body = admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/payments", id),
				map[string]any{"method": parts[1], "amount_rial": amount}, withKey(newUUID()))
		case s == "refund":
			resp, body = admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/refund", id), map[string]any{"note": "test"})
		case s == "cancel":
			resp, body = admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/cancel", id), map[string]any{"note": "test"})
		default:
			resp, body = admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/%s", id, s), nil)
		}
		if resp.StatusCode/100 != 2 {
			t.Fatalf("order %v step %s: %d %v", id, s, resp.StatusCode, body)
		}
	}
}

func TestCustomerAdminRoutesAreAdminOnly(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	e.createUser("barista", "OPERATOR", "barista-pass-1")
	op := e.newClient().login("barista", "barista-pass-1")
	cust, id := newCustomer(t, e, "09121111111", "آرش")
	d := today()
	paths := []string{
		"/api/v1/admin/customers",
		fmt.Sprintf("/api/v1/admin/customers/%d", id),
		fmt.Sprintf("/api/v1/admin/customers/%d/orders", id),
		"/api/v1/admin/reports/customers?from=" + d + "&to=" + d,
		"/api/v1/admin/reports/customers.csv?from=" + d + "&to=" + d,
		"/api/v1/admin/reports/customers.xlsx?from=" + d + "&to=" + d,
		fmt.Sprintf("/api/v1/admin/reports/customers/%d/orders.csv?from=%s&to=%s", id, d, d),
		fmt.Sprintf("/api/v1/admin/reports/customers/%d/orders.xlsx?from=%s&to=%s", id, d, d),
	}
	for _, p := range paths {
		if resp, _ := op.get(p); resp.StatusCode != http.StatusForbidden {
			t.Errorf("OPERATOR %s = %d", p, resp.StatusCode)
		}
		if resp, _ := e.newClient().get(p); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("anonymous %s = %d", p, resp.StatusCode)
		}
		if resp, _ := cust.get(p); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("customer session %s = %d", p, resp.StatusCode)
		}
		resp, _ := admin.get(p)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("ADMIN %s = %d", p, resp.StatusCode)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s Cache-Control = %q", p, resp.Header.Get("Cache-Control"))
		}
	}
	// The staff "users" API still lists staff only.
	_, body := admin.do(http.MethodGet, "/api/v1/admin/users", nil)
	if strings.Contains(fmt.Sprint(body), "09121111111") {
		t.Error("customers appear in the staff users list")
	}
}

func TestAdminCustomerListSearchAndDetail(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, _, _ := seedCatalog(t, admin)
	a, aID := newCustomer(t, e, "09121111111", "آرش")
	newCustomer(t, e, "09122222222", "بهار")
	newCustomer(t, e, "09123333333", "سیما")
	_, body := placeOrder(a, newUUID(), line(espresso, 2, 950000))
	orderFlow(t, admin, orderIDByNumber(t, admin, body["order_number"].(float64)), "accept", "pay:CASH:1900000", "ready", "complete")
	placeOrder(a, newUUID(), line(espresso, 1, 950000)) // open

	resp, body := admin.do(http.MethodGet, "/api/v1/admin/customers", nil)
	expect(t, resp, body, http.StatusOK, "")
	items := body["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("list = %v", body)
	}
	last := items[2].(map[string]any)
	if last["id"].(float64) != float64(aID) || last["orders"].(float64) != 2 || last["completed_orders"].(float64) != 1 ||
		last["completed_gross_rial"].(float64) != 1900000 || last["phone_verified"] != false {
		t.Errorf("A row = %v", last)
	}
	if strings.Contains(fmt.Sprint(body), "password") || strings.Contains(fmt.Sprint(body), "$argon2") {
		t.Fatal("customer list leaks password data")
	}
	for search, want := range map[string]int{"0912222": 1, "۰۹۱۲۳۳": 1, "بهار": 1, "مشتری": 3, "%": 3, "zzz": 0} {
		_, body = admin.do(http.MethodGet, "/api/v1/admin/customers?search="+strings.ReplaceAll(search, "%", "%25"), nil)
		if n := len(body["items"].([]any)); n != want {
			t.Errorf("search %q = %d results, want %d", search, n, want)
		}
	}
	_, body = admin.do(http.MethodGet, "/api/v1/admin/customers?limit=2", nil)
	next := body["next_before_id"]
	_, body = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/admin/customers?limit=2&before_id=%v", next), nil)
	if n := len(body["items"].([]any)); n != 1 {
		t.Errorf("page 2 = %d", n)
	}

	resp, body = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/admin/customers/%d", aID), nil)
	expect(t, resp, body, http.StatusOK, "")
	tot := body["totals"].(map[string]any)
	if tot["orders"].(float64) != 2 || tot["open_orders"].(float64) != 1 || tot["payments_received_rial"].(float64) != 1900000 ||
		tot["net_collected_rial"].(float64) != 1900000 || tot["completed_revenue_rial"].(float64) != 1900000 ||
		tot["receivable_rial"].(float64) != 0 {
		t.Errorf("totals = %v", tot)
	}
	resp, body = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/admin/customers/%d/orders?from=%s&to=%s", aID, today(), today()), nil)
	expect(t, resp, body, http.StatusOK, "")
	if n := len(body["items"].([]any)); n != 2 {
		t.Errorf("customer orders = %d", n)
	}
	resp, body = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/admin/customers/%d/orders?from=2020-01-01&to=2020-01-31", aID), nil)
	expect(t, resp, body, http.StatusOK, "")
	if n := len(body["items"].([]any)); n != 0 {
		t.Errorf("out-of-range orders = %d", n)
	}
	for _, q := range []string{"from=2026-02-01&to=2026-01-01", "from=2020-01-01&to=2026-01-01", "from=bad&to=2026-01-01"} {
		resp, body = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/admin/customers/%d/orders?%s", aID, q), nil)
		expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	}
	resp, body = admin.do(http.MethodGet, "/api/v1/admin/customers/999999", nil)
	expect(t, resp, body, http.StatusNotFound, "NOT_FOUND")
	resp, body = admin.do(http.MethodGet, "/api/v1/admin/customers/999999/orders", nil)
	expect(t, resp, body, http.StatusNotFound, "NOT_FOUND")
}

// TestCustomerReportReconciles builds every money case for one customer and checks the report
// against the hand-computed figures and against the existing summary report for the same day.
func TestCustomerReportReconciles(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, soda, _ := seedCatalog(t, admin)
	stockSoda(t, admin, soda)
	a, aID := newCustomer(t, e, "09121111111", "آرش")
	newCustomer(t, e, "09122222222", "بهار") // no orders: not in the report
	order := func(c *client, items ...map[string]any) float64 {
		t.Helper()
		resp, body := placeOrder(c, newUUID(), items...)
		expect(t, resp, body, http.StatusCreated, "")
		return orderIDByNumber(t, admin, body["order_number"].(float64))
	}

	// 1. multi-line, completed, fully paid: 2×450 000 + 950 000 = 1 850 000
	orderFlow(t, admin, order(a, line(soda, 2, 450000), line(espresso, 1, 950000)), "accept", "pay:CARD_TERMINAL:1850000", "ready", "complete")
	// 2. completed, unpaid: receivable 950 000
	orderFlow(t, admin, order(a, line(espresso, 1, 950000)), "accept", "ready", "complete")
	// 3. completed, paid in cash, then refunded: 1 900 000
	orderFlow(t, admin, order(a, line(espresso, 2, 950000)), "accept", "pay:CASH:1900000", "ready", "complete", "refund")
	// 4. prepaid 950 000, then cancelled: the prepayment is refunded, never revenue
	orderFlow(t, admin, order(a, line(espresso, 1, 950000)), "accept", "pay:CASH:950000", "cancel")
	// 5. still pending
	order(a, line(espresso, 1, 950000))
	// A guest order that is not completed does not change sales figures.
	order(e.newClient(), line(espresso, 1, 950000))

	d := today()
	resp, body := admin.do(http.MethodGet, "/api/v1/admin/reports/customers?from="+d+"&to="+d, nil)
	expect(t, resp, body, http.StatusOK, "")
	rows := body["customers"].([]any)
	if len(rows) != 1 {
		t.Fatalf("report rows = %v", rows)
	}
	r := rows[0].(map[string]any)
	want := map[string]float64{
		"customer_id": float64(aID), "orders_placed": 5, "completed_orders": 3, "cancelled_orders": 1, "open_orders": 1,
		"gross_sales_rial": 1850000 + 950000 + 1900000, "sales_refunds_rial": 1900000,
		"revenue_rial": 1850000 + 950000, "prepayment_refunds_rial": 950000,
		"payments_received_rial": 1850000 + 1900000 + 950000, "refunds_recorded_rial": 1900000 + 950000,
		"net_collected_rial": 1850000, "receivable_rial": 950000,
	}
	for k, v := range want {
		if r[k].(float64) != v {
			t.Errorf("%s = %v, want %v", k, r[k], v)
		}
	}
	totals := body["totals"].(map[string]any)
	for k := range want {
		if k != "customer_id" && totals[k] != r[k] {
			t.Errorf("totals %s = %v, row %v", k, totals[k], r[k])
		}
	}
	if body["definitions"].(map[string]any)["revenue_rial"] == nil || body["timezone"] != "Asia/Tehran" {
		t.Errorf("report lacks definitions/timezone: %v", body)
	}

	// Every order with money belongs to the customer, so the summary report must agree.
	_, sum := admin.do(http.MethodGet, "/api/v1/admin/reports/summary?from="+d+"&to="+d, nil)
	st := sum["totals"].(map[string]any)
	received := st["payments_received"].(map[string]any)
	refunds := st["refunds_recorded"].(map[string]any)
	for name, pair := range map[string][2]float64{
		"gross sales":        {st["gross_sales_rial"].(float64), totals["gross_sales_rial"].(float64)},
		"sales refunds":      {st["sales_refunds_rial"].(float64), totals["sales_refunds_rial"].(float64)},
		"revenue":            {st["revenue_rial"].(float64), totals["revenue_rial"].(float64)},
		"prepayment refunds": {st["prepayment_refunds_rial"].(float64), totals["prepayment_refunds_rial"].(float64)},
		"payments received":  {received["CASH"].(float64) + received["CARD_TERMINAL"].(float64), totals["payments_received_rial"].(float64)},
		"refunds recorded":   {refunds["CASH"].(float64) + refunds["CARD_TERMINAL"].(float64), totals["refunds_recorded_rial"].(float64)},
		"receivables":        {sum["receivables"].(map[string]any)["amount_rial"].(float64), totals["receivable_rial"].(float64)},
		"completed orders":   {st["orders"].(float64), totals["completed_orders"].(float64)},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s: summary %v, customer report %v", name, pair[0], pair[1])
		}
	}

	// A range without activity has no in-range figures; the receivable is point in time (now), so
	// the customer who still owes money is listed and the totals keep matching the summary report.
	_, body = admin.do(http.MethodGet, "/api/v1/admin/reports/customers?from=2020-01-01&to=2020-01-31", nil)
	old := body["totals"].(map[string]any)
	if len(body["customers"].([]any)) != 1 || old["gross_sales_rial"].(float64) != 0 || old["orders_placed"].(float64) != 0 ||
		old["payments_received_rial"].(float64) != 0 || old["receivable_rial"].(float64) != 950000 {
		t.Errorf("old range = %v", body)
	}
	for _, q := range []string{"", "?from=" + d, "?from=2020-01-01&to=2026-01-01", "?from=2026-02-02&to=2026-02-01"} {
		resp, body = admin.do(http.MethodGet, "/api/v1/admin/reports/customers"+q, nil)
		expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	}
}

func TestCustomerExportsAreSafeSpreadsheets(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, _, _ := seedCatalog(t, admin)
	// Hostile names: formulas, a command, a tab and control characters.
	c := e.newClient()
	resp, body := c.do(http.MethodPost, customerPath+"register", map[string]string{
		"first_name": "=HYPERLINK(\"http://x\",\"y\")", "last_name": "@SUM(1+1)", "phone": "09121111111", "password": "secure-cafe-2026"})
	expect(t, resp, body, http.StatusCreated, "")
	c.csrf = body["csrf_token"].(string)
	id := int64(body["customer"].(map[string]any)["id"].(float64))
	_, body = placeOrder(c, newUUID(), line(espresso, 2, 950000))
	orderFlow(t, admin, orderIDByNumber(t, admin, body["order_number"].(float64)), "accept", "pay:CASH:1900000", "ready", "complete")
	d := today()

	// CSV: UTF-8 BOM, attachment, neutralised formulas, numbers kept as numbers.
	resp, raw := admin.get("/api/v1/admin/reports/customers.csv?from=" + d + "&to=" + d)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), `attachment; filename="rokh-customers-`+d+"_"+d+`.csv"`) {
		t.Fatalf("csv headers: %d %v", resp.StatusCode, resp.Header)
	}
	if !bytes.HasPrefix(raw, []byte("\xef\xbb\xbf")) {
		t.Fatal("csv lacks the UTF-8 BOM")
	}
	recs, err := csv.NewReader(bytes.NewReader(raw[3:])).ReadAll()
	if err != nil || len(recs) != 3 { // header, customer, total
		t.Fatalf("csv = %v %v", recs, err)
	}
	row := recs[1]
	if row[1] != `'=HYPERLINK("http://x","y")` || row[2] != "'@SUM(1+1)" {
		t.Errorf("csv formula injection not neutralised: %q", row[1:3])
	}
	if row[8] != "1900000" || recs[0][8] != "فروش ناخالص (ریال)" {
		t.Errorf("csv figures/header = %q / %q", row[8], recs[0][8])
	}
	for _, leak := range []string{"argon2", "127.0.0.1"} {
		if bytes.Contains(raw, []byte(leak)) {
			t.Errorf("csv contains %q", leak)
		}
	}

	// XLSX: a real workbook, inline strings only, risky text quote-prefixed, no formulas.
	resp, raw = admin.get("/api/v1/admin/reports/customers.xlsx?from=" + d + "&to=" + d)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("xlsx headers: %d %v", resp.StatusCode, resp.Header)
	}
	files := unzip(t, raw)
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels",
		"xl/styles.xml", "xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml"} {
		if _, ok := files[name]; !ok {
			t.Errorf("xlsx lacks %s", name)
		}
	}
	sheet := files["xl/worksheets/sheet1.xml"]
	if strings.Contains(sheet, "<f>") || strings.Contains(sheet, "<f ") {
		t.Error("xlsx contains a formula")
	}
	if !strings.Contains(sheet, `t="inlineStr" s="3"><is><t xml:space="preserve">=HYPERLINK(&quot;http://x&quot;,&quot;y&quot;)</t>`) {
		t.Errorf("risky text is not a quote-prefixed inline string: %s", sheet)
	}
	if !strings.Contains(sheet, `<v>1900000</v>`) || !strings.Contains(sheet, `rightToLeft="1"`) {
		t.Error("xlsx lacks numeric cells or the RTL view")
	}
	if !strings.Contains(files["xl/styles.xml"], `quotePrefix="1"`) {
		t.Error("styles lack quotePrefix")
	}

	// Per-customer order lines: one row per line with the recorded price.
	resp, raw = admin.get(fmt.Sprintf("/api/v1/admin/reports/customers/%d/orders.csv?from=%s&to=%s", id, d, d))
	if resp.StatusCode != 200 {
		t.Fatalf("lines csv = %d %s", resp.StatusCode, raw)
	}
	recs, _ = csv.NewReader(bytes.NewReader(raw[3:])).ReadAll()
	if len(recs) != 2 || recs[1][10] != "اسپرسو" || recs[1][11] != "2" || recs[1][12] != "950000" || recs[1][13] != "1900000" {
		t.Errorf("lines = %v", recs)
	}
	resp, raw = admin.get(fmt.Sprintf("/api/v1/admin/reports/customers/%d/orders.xlsx?from=%s&to=%s", id, d, d))
	if resp.StatusCode != 200 || !strings.Contains(unzip(t, raw)["xl/worksheets/sheet2.xml"], "09121111111") {
		t.Errorf("lines xlsx = %d", resp.StatusCode)
	}
	if resp, _ = admin.get("/api/v1/admin/reports/customers/999999/orders.csv?from=" + d + "&to=" + d); resp.StatusCode != 404 {
		t.Errorf("unknown customer export = %d", resp.StatusCode)
	}
	if resp, _ = admin.get(fmt.Sprintf("/api/v1/admin/reports/customers/%d/orders.csv", id)); resp.StatusCode != 422 {
		t.Errorf("export without a range = %d", resp.StatusCode)
	}
}

func unzip(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("not a zip/xlsx: %v", err)
	}
	out := map[string]string{}
	for _, f := range z.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

// F-01 regression: the manager's all-time customer totals come from the payment ledger. Before the
// fix, "paid_rial" summed orders.paid_rial, which a refund never reduces, and was shown as the net
// amount paid: a refunded order and a refunded cancelled prepayment still counted as collected.
func TestAdminCustomerTotalsUseThePaymentLedger(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, _, _ := seedCatalog(t, admin)
	a, aID := newCustomer(t, e, "09121111111", "آرش")
	order := func(qty int) float64 {
		t.Helper()
		resp, body := placeOrder(a, newUUID(), line(espresso, qty, 950000))
		expect(t, resp, body, http.StatusCreated, "")
		return orderIDByNumber(t, admin, body["order_number"].(float64))
	}

	// completed, paid 1 900 000, then fully refunded
	orderFlow(t, admin, order(2), "accept", "pay:CASH:1900000", "ready", "complete", "refund")
	// prepaid 950 000, then cancelled (the prepayment is refunded)
	orderFlow(t, admin, order(1), "accept", "pay:CARD_TERMINAL:950000", "cancel")
	// completed with a partial payment of 500 000: 450 000 still owed
	orderFlow(t, admin, order(1), "accept", "pay:CASH:500000", "ready", "complete")
	// completed and unpaid: 950 000 owed
	orderFlow(t, admin, order(1), "accept", "ready", "complete")
	// a cash payment entered by mistake and voided, then paid by card and completed
	voided := order(1)
	orderFlow(t, admin, voided, "accept")
	resp, body := admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/payments", voided),
		map[string]any{"method": "CASH", "amount_rial": 950000}, withKey(newUUID()))
	expect(t, resp, body, http.StatusCreated, "")
	resp, body = admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/payments/%v/void", body["payment"].(map[string]any)["id"]),
		map[string]any{"reason": "wrong order"})
	expect(t, resp, body, http.StatusOK, "")
	orderFlow(t, admin, voided, "pay:CARD_TERMINAL:950000", "ready", "complete")

	resp, body = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/admin/customers/%d", aID), nil)
	expect(t, resp, body, http.StatusOK, "")
	tot := body["totals"].(map[string]any)
	if _, ok := tot["paid_rial"]; ok {
		t.Error(`the ambiguous "paid_rial" total is still exposed`)
	}
	want := map[string]float64{
		"orders": 5, "completed_orders": 4, "cancelled_orders": 1, "open_orders": 0,
		"completed_gross_rial":    1900000 + 950000 + 950000 + 950000,
		"completed_refunded_rial": 1900000,
		"completed_revenue_rial":  950000 + 950000 + 950000,
		"payments_received_rial":  1900000 + 950000 + 500000 + 950000, // the voided 950 000 is not money
		"refunds_recorded_rial":   1900000 + 950000,
		"net_collected_rial":      500000 + 950000,
		"receivable_rial":         450000 + 950000,
	}
	for k, v := range want {
		if tot[k] != v {
			t.Errorf("%s = %v, want %v", k, tot[k], v)
		}
	}

	// Everything happened today, so the all-time figures equal the report for today.
	d := today()
	_, rep := admin.do(http.MethodGet, "/api/v1/admin/reports/customers?from="+d+"&to="+d, nil)
	row := rep["customers"].([]any)[0].(map[string]any)
	for detail, report := range map[string]string{
		"payments_received_rial": "payments_received_rial", "refunds_recorded_rial": "refunds_recorded_rial",
		"net_collected_rial": "net_collected_rial", "receivable_rial": "receivable_rial",
		"completed_gross_rial": "gross_sales_rial", "completed_revenue_rial": "revenue_rial",
	} {
		if tot[detail] != row[report] {
			t.Errorf("detail %s = %v, report %s = %v", detail, tot[detail], report, row[report])
		}
	}
}

// F-03 regression: open orders are point in time, so a customer whose only order was placed before
// the selected range and is still open is listed (before the fix the row was dropped although the
// figure is not range-bound, and the totals' open-order count disagreed with "now").
func TestCustomerReportListsOpenOrdersPlacedBeforeTheRange(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, _, _ := seedCatalog(t, admin)
	a, aID := newCustomer(t, e, "09121111111", "آرش")
	b, bID := newCustomer(t, e, "09122222222", "بهار")
	newCustomer(t, e, "09123333333", "سیما")                          // never ordered: never listed
	resp, body := placeOrder(a, newUUID(), line(espresso, 1, 950000)) // stays PENDING
	expect(t, resp, body, http.StatusCreated, "")
	resp, body = placeOrder(b, newUUID(), line(espresso, 1, 950000))
	expect(t, resp, body, http.StatusCreated, "")
	orderFlow(t, admin, orderIDByNumber(t, admin, body["order_number"].(float64)), "accept", "pay:CASH:300000") // PREPARING, part paid

	// A range that starts after both orders were placed.
	tehran, _ := time.LoadLocation("Asia/Tehran")
	from := time.Now().In(tehran).AddDate(0, 0, 1).Format(time.DateOnly)
	to := time.Now().In(tehran).AddDate(0, 0, 7).Format(time.DateOnly)
	resp, body = admin.do(http.MethodGet, "/api/v1/admin/reports/customers?from="+from+"&to="+to, nil)
	expect(t, resp, body, http.StatusOK, "")
	rows := body["customers"].([]any)
	if len(rows) != 2 {
		t.Fatalf("customers with open orders placed before the range: got %d rows, want 2: %v", len(rows), rows)
	}
	seen := map[float64]bool{}
	for _, x := range rows {
		r := x.(map[string]any)
		seen[r["customer_id"].(float64)] = true
		if r["open_orders"].(float64) != 1 {
			t.Errorf("open_orders = %v: %v", r["open_orders"], r)
		}
		for _, k := range []string{"orders_placed", "completed_orders", "gross_sales_rial", "payments_received_rial",
			"refunds_recorded_rial", "net_collected_rial", "receivable_rial"} {
			if r[k].(float64) != 0 {
				t.Errorf("in-range figure %s = %v outside the activity window", k, r[k])
			}
		}
	}
	if !seen[float64(aID)] || !seen[float64(bID)] {
		t.Errorf("rows = %v", rows)
	}
	if body["totals"].(map[string]any)["open_orders"].(float64) != 2 {
		t.Errorf("totals = %v", body["totals"])
	}
	// The export lists the same customers.
	_, raw := admin.get("/api/v1/admin/reports/customers.csv?from=" + from + "&to=" + to)
	recs, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, rec := range recs {
		if len(rec) > 3 && (rec[3] == "09121111111" || rec[3] == "09122222222") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("CSV lists %d of the 2 customers with open orders: %v", n, recs)
	}
}

// Audit follow-up: with completed guest orders, voided payments and refunds on both sides, the
// customer report and the summary report differ by exactly the guest (unattributed) activity.
func TestCustomerReportDiffersFromSummaryByGuestActivityOnly(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, _, _ := seedCatalog(t, admin)
	a, _ := newCustomer(t, e, "09121111111", "آرش")
	guest := e.newClient()
	order := func(c *client, qty int) float64 {
		t.Helper()
		resp, body := placeOrder(c, newUUID(), line(espresso, qty, 950000))
		expect(t, resp, body, http.StatusCreated, "")
		return orderIDByNumber(t, admin, body["order_number"].(float64))
	}
	voidThenPay := func(id float64) {
		t.Helper()
		resp, body := admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%v/payments", id),
			map[string]any{"method": "CASH", "amount_rial": 950000}, withKey(newUUID()))
		expect(t, resp, body, http.StatusCreated, "")
		resp, body = admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/payments/%v/void", body["payment"].(map[string]any)["id"]),
			map[string]any{"reason": "wrong order"})
		expect(t, resp, body, http.StatusOK, "")
		orderFlow(t, admin, id, "pay:CARD_TERMINAL:950000")
	}

	// Customer: completed and paid (with a voided payment first); completed, paid and refunded; unpaid.
	c1 := order(a, 1)
	orderFlow(t, admin, c1, "accept")
	voidThenPay(c1)
	orderFlow(t, admin, c1, "ready", "complete")
	orderFlow(t, admin, order(a, 2), "accept", "pay:CASH:1900000", "ready", "complete", "refund")
	orderFlow(t, admin, order(a, 1), "accept", "ready", "complete")
	// Guest: completed and paid (with a voided payment first); prepaid then cancelled; completed, part paid.
	g1 := order(guest, 1)
	orderFlow(t, admin, g1, "accept")
	voidThenPay(g1)
	orderFlow(t, admin, g1, "ready", "complete")
	orderFlow(t, admin, order(guest, 2), "accept", "pay:CASH:1900000", "cancel")
	orderFlow(t, admin, order(guest, 1), "accept", "pay:CASH:400000", "ready", "complete")

	d := today()
	_, body := admin.do(http.MethodGet, "/api/v1/admin/reports/customers?from="+d+"&to="+d, nil)
	ct := body["totals"].(map[string]any)
	_, sum := admin.do(http.MethodGet, "/api/v1/admin/reports/summary?from="+d+"&to="+d, nil)
	st := sum["totals"].(map[string]any)
	byMethod := func(m any) float64 {
		x := m.(map[string]any)
		return x["CASH"].(float64) + x["CARD_TERMINAL"].(float64)
	}
	// Guest figures, computed by hand.
	guestOnly := map[string]float64{
		"gross sales":        950000 + 950000,
		"sales refunds":      0,
		"prepayment refunds": 1900000,
		"payments received":  950000 + 1900000 + 400000,
		"refunds recorded":   1900000,
		"receivables":        950000 - 400000,
		"completed orders":   2,
	}
	got := map[string][2]float64{
		"gross sales":        {st["gross_sales_rial"].(float64), ct["gross_sales_rial"].(float64)},
		"sales refunds":      {st["sales_refunds_rial"].(float64), ct["sales_refunds_rial"].(float64)},
		"prepayment refunds": {st["prepayment_refunds_rial"].(float64), ct["prepayment_refunds_rial"].(float64)},
		"payments received":  {byMethod(st["payments_received"]), ct["payments_received_rial"].(float64)},
		"refunds recorded":   {byMethod(st["refunds_recorded"]), ct["refunds_recorded_rial"].(float64)},
		"receivables":        {sum["receivables"].(map[string]any)["amount_rial"].(float64), ct["receivable_rial"].(float64)},
		"completed orders":   {st["orders"].(float64), ct["completed_orders"].(float64)},
	}
	for name, pair := range got {
		if diff := pair[0] - pair[1]; diff != guestOnly[name] {
			t.Errorf("%s: summary %v − customers %v = %v, want the guest share %v", name, pair[0], pair[1], diff, guestOnly[name])
		}
	}
	// And the customer side on its own.
	for k, v := range map[string]float64{"gross_sales_rial": 950000 + 1900000 + 950000, "sales_refunds_rial": 1900000,
		"payments_received_rial": 950000 + 1900000, "refunds_recorded_rial": 1900000, "net_collected_rial": 950000,
		"receivable_rial": 950000} {
		if ct[k] != v {
			t.Errorf("customer %s = %v, want %v", k, ct[k], v)
		}
	}
}
