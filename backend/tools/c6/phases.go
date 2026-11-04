package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
	_ "time/tzdata" // static Linux binaries in containers without zoneinfo
)

// The HTTP phases of the rehearsal. Each phase reads and extends a state file (0600, disposable
// directory) so the next binary can continue where the previous one stopped:
//
//	seed-legacy          previous (pre-C1) binary on the pre-00008 database: staff, catalog, stock,
//	                     guest orders in every money state, an expense; baseline report figures.
//	verify-new           C1–C5 binary on the restored + migrated database: legacy data and tracking
//	                     unchanged, the same report figures, then customer accounts end to end.
//	verify-old           previous binary on the migrated database (binary rollback): legacy flows,
//	                     tracking, payments and stock work; customer routes absent; customer
//	                     sessions are never staff sessions.
//	verify-roll-forward  C1–C5 binary again: accounts, history, ownership and reports intact.
//
// Admin credentials come from C6_ADMIN_USER / C6_ADMIN_PASSWORD; nothing secret is printed.

type order struct {
	ID     int64  `json:"id"`
	Number int64  `json:"number"`
	Token  string `json:"token"`
	Total  int64  `json:"total"`
	Status string `json:"status"`
	// Replay data for the cross-binary idempotency check.
	Key      string `json:"key,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	Body     string `json:"body,omitempty"`
}

type account struct {
	Phone    string  `json:"phone"`
	Password string  `json:"password"`
	ID       int64   `json:"id"`
	Cookie   string  `json:"cookie"`
	Orders   []order `json:"orders"`
}

type state struct {
	OperatorUser     string           `json:"operator_user"`
	OperatorPassword string           `json:"operator_password"`
	Products         map[string]int64 `json:"products"`
	Legacy           []order          `json:"legacy"`
	NewEra           []order          `json:"new_era"`
	OldEra           []order          `json:"old_era"`
	A                account          `json:"a"`
	B                account          `json:"b"`
	Summary          map[string]int64 `json:"summary"` // figures of the last phase, for the next
}

var prices = map[string]int64{"espresso": 950000, "cake": 1200000, "soda": 450000}

type run struct {
	ctx    context.Context
	base   string
	st     *state
	failed int
	phase  string
}

func (r *run) check(ok bool, format string, v ...any) bool {
	status := "PASS"
	if !ok {
		status = "FAIL"
		r.failed++
	}
	fmt.Printf("%s  [%s] %s\n", status, r.phase, fmt.Sprintf(format, v...))
	return ok
}

// must stops the phase: later steps would only produce noise.
func (r *run) must(ok bool, format string, v ...any) {
	if !r.check(ok, format, v...) {
		panic(errStop)
	}
}

var errStop = errors.New("phase stopped")

func cmdPhase(ctx context.Context, args []string) (err error) {
	if len(args) < 1 {
		return errors.New("usage: c6 phase NAME -url BASE -state FILE")
	}
	name := args[0]
	fs := flag.NewFlagSet("phase", flag.ContinueOnError)
	base := fs.String("url", "", "base URL of the running binary")
	file := fs.String("state", "", "state file")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	st := &state{Products: map[string]int64{}}
	if b, err := os.ReadFile(*file); err == nil {
		if err := json.Unmarshal(b, st); err != nil {
			return err
		}
	}
	r := &run{ctx: ctx, base: *base, st: st, phase: name}
	defer func() {
		if p := recover(); p != nil {
			if p != errStop {
				panic(p)
			}
		}
		if b, e := json.MarshalIndent(st, "", " "); e == nil {
			_ = os.WriteFile(*file, b, 0o600)
		}
		if r.failed > 0 && err == nil {
			err = fmt.Errorf("phase %s: %d check(s) failed", name, r.failed)
		}
	}()
	r.must(waitReady(*base), "GET /readyz answers 200 (binary up, frontend embedded)")
	switch name {
	case "seed-legacy":
		r.seedLegacy()
	case "verify-new":
		r.verifyNew()
	case "verify-old":
		r.verifyOld()
	case "verify-roll-forward":
		r.verifyRollForward()
	default:
		return fmt.Errorf("unknown phase %q", name)
	}
	return nil
}

func waitReady(base string) bool {
	c := newClient(base)
	for i := 0; i < 60; i++ {
		if c.do(http.MethodGet, "/readyz", nil).status == http.StatusOK {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// ---- helpers -------------------------------------------------------------------------------------

func (r *run) staff(user, password string) *client {
	c := newClient(r.base)
	resp := c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": user, "password": password})
	r.must(resp.status == http.StatusOK, "staff login %s (%d)", user, resp.status)
	c.csrf, _ = resp.body["csrf_token"].(string)
	return c
}

func (r *run) admin() *client {
	return r.staff(os.Getenv("C6_ADMIN_USER"), os.Getenv("C6_ADMIN_PASSWORD"))
}

func today() string {
	tehran, _ := time.LoadLocation("Asia/Tehran")
	return time.Now().In(tehran).Format(time.DateOnly)
}

func monthAgo() string {
	tehran, _ := time.LoadLocation("Asia/Tehran")
	return time.Now().In(tehran).AddDate(0, 0, -30).Format(time.DateOnly)
}

// summary returns the established finance figures of the last 31 business days.
func (r *run) summary(admin *client) map[string]int64 {
	resp := admin.do(http.MethodGet, "/api/v1/admin/reports/summary?from="+monthAgo()+"&to="+today(), nil)
	r.must(resp.status == http.StatusOK, "summary report (%d)", resp.status)
	t := obj(resp.body["totals"])
	both := func(m any) int64 { return num(obj(m)["CASH"]) + num(obj(m)["CARD_TERMINAL"]) }
	return map[string]int64{
		"gross_sales_rial": num(t["gross_sales_rial"]), "sales_refunds_rial": num(t["sales_refunds_rial"]),
		"revenue_rial": num(t["revenue_rial"]), "orders": num(t["orders"]), "cogs_rial": num(t["cogs_rial"]),
		"prepayment_refunds_rial": num(t["prepayment_refunds_rial"]), "expenses_rial": num(t["expenses_rial"]),
		"payments_received_rial": both(t["payments_received"]), "refunds_recorded_rial": both(t["refunds_recorded"]),
		"receivables_rial": num(obj(resp.body["receivables"])["amount_rial"]),
	}
}

func (r *run) sameFigures(what string, got, want map[string]int64) {
	diff := []string{}
	for k, v := range want {
		if got[k] != v {
			diff = append(diff, fmt.Sprintf("%s %d≠%d", k, got[k], v))
		}
	}
	r.check(len(diff) == 0, "%s (%s)", what, strings.Join(diff, ", "))
}

// place creates an order the way the app does; csrf is the customer token or "" for a guest.
func (r *run) place(c *client, admin *client, lines map[string]int) order {
	type line struct {
		ProductID int64 `json:"product_id"`
		Quantity  int   `json:"quantity"`
		Price     int64 `json:"expected_unit_price_rial"`
	}
	var body struct {
		Items []line `json:"items"`
	}
	var total int64
	for _, name := range []string{"espresso", "cake", "soda"} {
		if q := lines[name]; q > 0 {
			body.Items = append(body.Items, line{r.st.Products[name], q, prices[name]})
			total += int64(q) * prices[name]
		}
	}
	key := uuid()
	raw, _ := json.Marshal(body)
	resp := c.do(http.MethodPost, "/api/v1/public/orders", body, "Idempotency-Key", key)
	r.must(resp.status == http.StatusCreated, "order created (%d %s)", resp.status, resp.code())
	o := order{Number: num(resp.body["order_number"]), Total: total, Status: "PENDING", Key: key, ClientID: c.clientID, Body: string(raw)}
	o.Token, _ = resp.body["tracking_token"].(string)
	r.must(o.Token != "", "order has a tracking token")
	list := admin.do(http.MethodGet, "/api/v1/admin/orders?status=PENDING", nil)
	for _, it := range items(list) {
		if num(it["order_number"]) == o.Number && num(it["id"]) > o.ID {
			o.ID = num(it["id"])
		}
	}
	r.must(o.ID > 0, "order %d found in the staff list", o.Number)
	return o
}

// flow runs staff steps: accept, ready, complete, refund, cancel, pay:METHOD:AMOUNT, void-last.
func (r *run) flow(admin *client, o *order, steps ...string) {
	var lastPayment int64
	for _, s := range steps {
		var resp response
		switch {
		case strings.HasPrefix(s, "pay:"):
			p := strings.Split(s, ":")
			var amount int64
			fmt.Sscan(p[2], &amount)
			resp = admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%d/payments", o.ID),
				map[string]any{"method": p[1], "amount_rial": amount}, "Idempotency-Key", uuid())
			lastPayment = num(obj(resp.body["payment"])["id"])
		case s == "void-last":
			resp = admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/payments/%d/void", lastPayment), map[string]any{"reason": "C6 rehearsal: wrong order"})
		case s == "refund" || s == "cancel":
			resp = admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%d/%s", o.ID, s), map[string]any{"note": "C6 rehearsal"})
		default:
			resp = admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/orders/%d/%s", o.ID, s), nil)
		}
		r.must(resp.status/100 == 2, "order %d: %s (%d %s)", o.Number, s, resp.status, resp.code())
		switch s {
		case "accept":
			o.Status = "PREPARING"
		case "ready":
			o.Status = "READY"
		case "complete":
			o.Status = "COMPLETED"
		case "cancel":
			o.Status = "CANCELLED"
		}
	}
}

func (r *run) track(orders []order, what string) {
	anon := newClient(r.base)
	bad := []string{}
	for _, o := range orders {
		resp := anon.do(http.MethodGet, "/api/v1/public/order", nil, "X-Tracking-Token", o.Token)
		got, _ := resp.body["status"].(string)
		if resp.status != http.StatusOK || got != o.Status {
			bad = append(bad, fmt.Sprintf("#%d %d %s≠%s", o.Number, resp.status, got, o.Status))
		}
	}
	r.check(len(bad) == 0, "%d %s orders track with their token and status (%s)", len(orders), what, strings.Join(bad, ", "))
}

func (r *run) stock(admin *client, product string) (onHand, reserved int64) {
	resp := admin.do(http.MethodGet, "/api/v1/admin/inventory", nil)
	r.must(resp.status == http.StatusOK, "inventory list (%d)", resp.status)
	for _, it := range items(resp) {
		if num(it["product_id"]) == r.st.Products[product] {
			return num(it["on_hand"]), num(it["reserved"])
		}
	}
	r.must(false, "inventory row for %s", product)
	return
}

// ---- seed-legacy (previous binary, pre-00008) -----------------------------------------------------

func (r *run) seedLegacy() {
	admin := r.admin()
	mk := func(path string, body any) map[string]any {
		resp := admin.do(http.MethodPost, path, body, "Idempotency-Key", uuid())
		r.must(resp.status == http.StatusCreated, "POST %s (%d %s)", path, resp.status, resp.code())
		return resp.body
	}
	drinks := num(mk("/api/v1/admin/categories", map[string]any{"name": "نوشیدنی", "sort_order": 1})["id"])
	snacks := num(mk("/api/v1/admin/categories", map[string]any{"name": "کیک و اسنک", "sort_order": 2})["id"])
	r.st.Products["espresso"] = num(mk("/api/v1/admin/products", map[string]any{"category_id": drinks, "name": "اسپرسو",
		"price_rial": prices["espresso"], "inventory_mode": "UNTRACKED", "estimated_unit_cost_rial": 300000})["id"])
	r.st.Products["soda"] = num(mk("/api/v1/admin/products", map[string]any{"category_id": drinks, "name": "نوشابه",
		"price_rial": prices["soda"], "inventory_mode": "TRACKED", "low_stock_threshold": 3})["id"])
	r.st.Products["cake"] = num(mk("/api/v1/admin/products", map[string]any{"category_id": snacks, "name": "کیک شکلاتی",
		"price_rial": prices["cake"], "inventory_mode": "TRACKED"})["id"])
	mk("/api/v1/admin/purchases", map[string]any{"product_id": r.st.Products["cake"], "quantity": 10, "unit_cost_rial": 400000, "purchased_on": today()})
	mk("/api/v1/admin/purchases", map[string]any{"product_id": r.st.Products["soda"], "quantity": 24, "unit_cost_rial": 150000, "purchased_on": today()})
	mk(fmt.Sprintf("/api/v1/admin/inventory/%d/adjustments", r.st.Products["soda"]),
		map[string]any{"direction": "OUT", "quantity": 2, "reason_code": "DAMAGED", "note": "C6 rehearsal"})
	mk("/api/v1/admin/expenses", map[string]any{"category": "UTILITIES", "amount_rial": 300000, "expense_date": today(), "description": "اجاره (C6)"})
	r.st.OperatorUser, r.st.OperatorPassword = "c6operator", randomSecret()
	mk("/api/v1/admin/users", map[string]any{"username": r.st.OperatorUser, "display_name": "C6 Operator", "role": "OPERATOR", "password": r.st.OperatorPassword})

	guest := func(lines map[string]int, steps ...string) order {
		o := r.place(newClient(r.base), admin, lines)
		r.flow(admin, &o, steps...)
		return o
	}
	full := func(lines map[string]int) int64 {
		var t int64
		for k, q := range lines {
			t += int64(q) * prices[k]
		}
		return t
	}
	l1 := map[string]int{"espresso": 2, "cake": 1}
	l4 := map[string]int{"cake": 2}
	r.st.Legacy = []order{
		guest(l1, "accept", fmt.Sprintf("pay:CASH:%d", full(l1)), "ready", "complete"),                                              // paid in cash
		guest(map[string]int{"soda": 3}, "accept", "pay:CARD_TERMINAL:500000", "ready", "complete"),                                 // partial: 850 000 owed
		guest(map[string]int{"espresso": 1}, "accept", "ready", "complete"),                                                         // unpaid
		guest(l4, "accept", fmt.Sprintf("pay:CASH:%d", full(l4)), "ready", "complete", "refund"),                                    // refunded
		guest(map[string]int{"espresso": 1}, "accept", "pay:CARD_TERMINAL:950000", "cancel"),                                        // prepaid, cancelled
		guest(map[string]int{"soda": 1}, "accept", "pay:CASH:450000", "void-last", "pay:CARD_TERMINAL:450000", "ready", "complete"), // voided payment
		guest(map[string]int{"cake": 1}),                          // pending (stock reserved)
		guest(map[string]int{"espresso": 1, "soda": 1}, "accept"), // preparing
	}
	_, reserved := r.stock(admin, "cake")
	r.check(reserved == 1, "pending legacy order reserves cake stock (reserved %d)", reserved)
	r.track(r.st.Legacy, "legacy")
	r.st.Summary = r.summary(admin)
	r.check(r.st.Summary["gross_sales_rial"] > 0 && r.st.Summary["receivables_rial"] == 850000+950000,
		"baseline figures recorded (gross %d, receivables %d)", r.st.Summary["gross_sales_rial"], r.st.Summary["receivables_rial"])
}

// ---- verify-new (C1–C5 binary on the restored, migrated database) -------------------------------

func (r *run) register(first string) (*client, account) {
	c := newClient(r.base)
	acc := account{Phone: "0912" + randomDigits(7), Password: randomSecret()}
	resp := c.do(http.MethodPost, "/api/v1/customer/auth/register",
		map[string]string{"first_name": first, "last_name": "آزمون", "phone": acc.Phone, "password": acc.Password})
	r.must(resp.status == http.StatusCreated, "customer registration (%d %s)", resp.status, resp.code())
	c.csrf, _ = resp.body["csrf_token"].(string)
	acc.ID = num(obj(resp.body["customer"])["id"])
	return c, acc
}

func (r *run) customerLogin(acc account) *client {
	c := newClient(r.base)
	resp := c.do(http.MethodPost, "/api/v1/customer/auth/login", map[string]string{"phone": acc.Phone, "password": acc.Password})
	r.must(resp.status == http.StatusOK, "customer login (%d %s)", resp.status, resp.code())
	c.csrf, _ = resp.body["csrf_token"].(string)
	return c
}

func (r *run) historyIDs(c *client) []int64 {
	resp := c.do(http.MethodGet, "/api/v1/customer/orders?limit=100", nil)
	r.must(resp.status == http.StatusOK, "customer history (%d)", resp.status)
	r.check(resp.header.Get("Cache-Control") == "no-store", "customer history is Cache-Control: no-store")
	var ids []int64
	for _, it := range items(resp) {
		ids = append(ids, num(it["id"]))
	}
	return ids
}

func (r *run) verifyNew() {
	r.track(r.st.Legacy, "legacy")
	// The guest request hash is unchanged: replaying a legacy order request returns that order.
	var pending *order
	for i := range r.st.Legacy {
		if r.st.Legacy[i].Status == "PENDING" {
			pending = &r.st.Legacy[i]
		}
	}
	r.must(pending != nil, "a pending legacy order to replay")
	replayer := newClient(r.base)
	replayer.clientID = pending.ClientID
	var body any
	_ = json.Unmarshal([]byte(pending.Body), &body)
	resp := replayer.do(http.MethodPost, "/api/v1/public/orders", body, "Idempotency-Key", pending.Key)
	r.check(resp.status/100 == 2 && num(resp.body["order_number"]) == pending.Number,
		"legacy idempotent replay returns the same order across binaries (%d, #%d)", resp.status, num(resp.body["order_number"]))

	admin := r.admin()
	r.sameFigures("report figures identical after backup, restore and migration 00008", r.summary(admin), r.st.Summary)
	resp = admin.do(http.MethodGet, "/api/v1/admin/customers", nil)
	r.check(resp.status == http.StatusOK && len(items(resp)) == 0, "no customers after migration (no backfill)")
	resp = admin.do(http.MethodGet, "/api/v1/admin/reports/customers?from="+monthAgo()+"&to="+today(), nil)
	r.check(resp.status == http.StatusOK && len(resp.body["customers"].([]any)) == 0, "legacy guest orders are not attributed to anyone")

	a, accA := r.register("آرش")
	b, accB := r.register("بهار")
	r.check(accA.ID > 0 && accB.ID > 0 && accA.ID != accB.ID, "two customer accounts")
	before, reservedBefore := r.stock(admin, "cake")
	oa := r.place(a, admin, map[string]int{"espresso": 1, "cake": 1})
	_, reservedAfter := r.stock(admin, "cake")
	r.check(reservedAfter == reservedBefore+1 && before > 0, "customer order reserves stock exactly once (%d→%d)", reservedBefore, reservedAfter)
	ids := r.historyIDs(a)
	r.check(len(ids) == 1 && ids[0] == oa.ID, "customer A sees exactly the order A placed")
	r.check(len(r.historyIDs(b)) == 0, "customer B's history is empty")
	resp = b.do(http.MethodGet, fmt.Sprintf("/api/v1/customer/orders/%d", oa.ID), nil)
	r.check(resp.status == http.StatusNotFound, "customer B gets 404 for A's order (%d)", resp.status)
	var oaBody any
	_ = json.Unmarshal([]byte(oa.Body), &oaBody)
	b.clientID = oa.ClientID
	resp = b.do(http.MethodPost, "/api/v1/public/orders", oaBody, "Idempotency-Key", oa.Key)
	r.check(resp.status == http.StatusConflict && resp.body["order_number"] == nil, "B cannot claim A's order with A's idempotency key (%d %s)", resp.status, resp.code())
	noCSRF := *a
	noCSRF.csrf = ""
	resp = noCSRF.do(http.MethodPost, "/api/v1/public/orders", oaBody, "Idempotency-Key", uuid())
	r.check(resp.status == http.StatusForbidden, "customer order without the customer CSRF token is refused (%d %s)", resp.status, resp.code())
	g := r.place(newClient(r.base), admin, map[string]int{"espresso": 1})
	r.check(len(r.historyIDs(a)) == 1, "a guest order is not attributed to the signed-in customer")

	r.flow(admin, &oa, "accept", fmt.Sprintf("pay:CASH:%d", oa.Total), "ready", "complete")
	resp = admin.do(http.MethodGet, "/api/v1/admin/reports/customers?from="+monthAgo()+"&to="+today(), nil)
	rows, _ := resp.body["customers"].([]any)
	r.check(len(rows) == 1 && num(obj(rows[0])["customer_id"]) == accA.ID && num(obj(rows[0])["gross_sales_rial"]) == oa.Total &&
		num(obj(rows[0])["net_collected_rial"]) == oa.Total, "customer report: A only, gross and net collected = A's order")
	want := map[string]int64{}
	for k, v := range r.st.Summary {
		want[k] = v
	}
	want["gross_sales_rial"] += oa.Total
	want["revenue_rial"] += oa.Total
	want["orders"]++
	want["payments_received_rial"] += oa.Total
	delete(want, "cogs_rial")
	got := r.summary(admin)
	r.sameFigures("summary moved by exactly A's order", got, want)
	r.st.Summary = got

	for _, f := range []string{"csv", "xlsx"} {
		resp = admin.do(http.MethodGet, "/api/v1/admin/reports/customers."+f+"?from="+monthAgo()+"&to="+today(), nil)
		ok := resp.status == http.StatusOK && resp.header.Get("Cache-Control") == "no-store" &&
			strings.HasPrefix(resp.header.Get("Content-Disposition"), "attachment")
		if f == "csv" {
			ok = ok && bytes.HasPrefix(resp.raw, []byte{0xEF, 0xBB, 0xBF}) && bytes.Contains(resp.raw, []byte(accA.Phone))
		} else {
			ok = ok && bytes.HasPrefix(resp.raw, []byte("PK"))
		}
		r.check(ok, "customer report %s download (no-store attachment, valid format)", f)
	}
	op := r.staff(r.st.OperatorUser, r.st.OperatorPassword)
	for _, p := range []string{"/api/v1/admin/customers", fmt.Sprintf("/api/v1/admin/customers/%d", accA.ID),
		"/api/v1/admin/reports/customers?from=" + today() + "&to=" + today(), "/api/v1/admin/reports/customers.csv?from=" + today() + "&to=" + today()} {
		resp = op.do(http.MethodGet, p, nil)
		r.check(resp.status == http.StatusForbidden, "OPERATOR gets 403 for %s (%d)", strings.SplitN(p, "?", 2)[0], resp.status)
	}
	r.check(op.do(http.MethodGet, "/api/v1/admin/orders", nil).status == http.StatusOK, "OPERATOR still works the order queue")
	r.check(a.do(http.MethodGet, "/api/v1/admin/orders", nil).status == http.StatusUnauthorized, "a customer session is not a staff session")
	r.check(admin.do(http.MethodGet, "/api/v1/customer/orders", nil).status == http.StatusUnauthorized, "a staff session is not a customer session")

	// Password change revokes the account's other sessions.
	second := r.customerLogin(accA)
	newPassword := randomSecret()
	resp = a.do(http.MethodPost, "/api/v1/customer/auth/password", map[string]string{"current_password": accA.Password, "new_password": newPassword})
	r.check(resp.status == http.StatusOK, "password change (%d %s)", resp.status, resp.code())
	a.csrf, _ = resp.body["csrf_token"].(string)
	accA.Password = newPassword
	r.check(second.do(http.MethodGet, "/api/v1/customer/auth/me", nil).status == http.StatusUnauthorized, "password change ended A's other session")
	r.check(a.do(http.MethodGet, "/api/v1/customer/auth/me", nil).status == http.StatusOK, "the session that changed the password continues")
	resp = b.do(http.MethodPost, "/api/v1/customer/auth/logout", nil)
	r.check(resp.status/100 == 2 && b.do(http.MethodGet, "/api/v1/customer/auth/me", nil).status == http.StatusUnauthorized, "logout ends B's session")

	accA.Cookie = a.cookie("rokh_customer")
	accA.Orders = []order{oa}
	r.check(accA.Cookie != "", "customer cookie kept for the rollback phases")
	r.st.A, r.st.B = accA, accB
	r.st.NewEra = []order{oa, g}
	r.track(r.st.NewEra, "new-era")
}

// ---- verify-old (previous binary on the migrated database) --------------------------------------

func (r *run) verifyOld() {
	r.track(r.st.Legacy, "legacy")
	r.track(r.st.NewEra, "new-era (customer and guest)")
	anon := newClient(r.base)
	resp := anon.do(http.MethodGet, "/api/v1/customer/auth/me", nil)
	r.check(resp.status == http.StatusNotFound, "customer API is absent on the previous binary (%d): expected temporary feature rollback", resp.status)
	carried := newClient(r.base)
	carried.setCookie("rokh_customer", r.st.A.Cookie)
	r.check(carried.do(http.MethodGet, "/api/v1/auth/me", nil).status == http.StatusUnauthorized, "a customer cookie is not a staff session on the previous binary")
	r.check(carried.do(http.MethodGet, "/api/v1/admin/orders", nil).status == http.StatusUnauthorized, "a customer cookie cannot reach staff data on the previous binary")

	admin := r.admin()
	start := r.summary(admin)
	want := map[string]int64{}
	for k, v := range r.st.Summary {
		want[k] = v
	}
	r.sameFigures("previous binary reports the same figures on the migrated database", start, want)
	op := r.staff(r.st.OperatorUser, r.st.OperatorPassword)
	r.check(op.do(http.MethodGet, "/api/v1/admin/orders", nil).status == http.StatusOK, "OPERATOR login and order queue on the previous binary")

	onHand, reserved := r.stock(admin, "soda")
	o := r.place(newClient(r.base), admin, map[string]int{"soda": 2, "espresso": 1})
	_, reservedAfter := r.stock(admin, "soda")
	r.check(reservedAfter == reserved+2, "previous binary reserves stock (%d→%d)", reserved, reservedAfter)
	r.flow(admin, &o, "accept", "pay:CASH:500000", "void-last", fmt.Sprintf("pay:CARD_TERMINAL:%d", o.Total), "ready", "complete")
	onHandAfter, reservedEnd := r.stock(admin, "soda")
	r.check(onHandAfter == onHand-2 && reservedEnd == reserved, "completion consumes the reservation (on hand %d→%d, reserved %d→%d)", onHand, onHandAfter, reserved, reservedEnd)
	r.track([]order{o}, "rollback-era guest")
	want["gross_sales_rial"] += o.Total
	want["revenue_rial"] += o.Total
	want["orders"]++
	want["payments_received_rial"] += o.Total
	delete(want, "cogs_rial")
	got := r.summary(admin)
	r.sameFigures("summary moved by exactly the rollback-era order (voided payment ignored)", got, want)
	r.st.Summary = got
	r.st.OldEra = []order{o}
}

// ---- verify-roll-forward (C1–C5 binary again) ----------------------------------------------------

func (r *run) verifyRollForward() {
	r.track(r.st.Legacy, "legacy")
	r.track(r.st.NewEra, "new-era")
	r.track(r.st.OldEra, "rollback-era")
	carried := newClient(r.base)
	carried.setCookie("rokh_customer", r.st.A.Cookie)
	resp := carried.do(http.MethodGet, "/api/v1/customer/auth/me", nil)
	r.check(resp.status == http.StatusOK && num(obj(resp.body["customer"])["id"]) == r.st.A.ID, "A's session survived the rollback window (%d)", resp.status)
	a := r.customerLogin(r.st.A)
	ids := r.historyIDs(a)
	r.check(len(ids) == 1 && ids[0] == r.st.A.Orders[0].ID, "A's history: the same single order (rollback-era guest order not attributed)")
	resp = a.do(http.MethodGet, fmt.Sprintf("/api/v1/customer/orders/%d", r.st.A.Orders[0].ID), nil)
	r.check(resp.status == http.StatusOK, "A opens the order detail (%d)", resp.status)
	resp = a.do(http.MethodGet, fmt.Sprintf("/api/v1/customer/orders/%d", r.st.OldEra[0].ID), nil)
	r.check(resp.status == http.StatusNotFound, "A cannot open the rollback-era guest order (%d)", resp.status)
	b := r.customerLogin(r.st.B)
	r.check(len(r.historyIDs(b)) == 0, "B's history still empty")
	r.check(b.do(http.MethodGet, fmt.Sprintf("/api/v1/customer/orders/%d", r.st.A.Orders[0].ID), nil).status == http.StatusNotFound, "B still gets 404 for A's order")

	admin := r.admin()
	o := r.place(a, admin, map[string]int{"espresso": 1})
	r.check(len(r.historyIDs(a)) == 2, "a new order is linked to A after roll-forward")
	r.sameFigures("roll-forward reports the same figures as the previous binary", r.summary(admin), r.st.Summary)
	resp = admin.do(http.MethodGet, "/api/v1/admin/reports/customers?from="+monthAgo()+"&to="+today(), nil)
	rows, _ := resp.body["customers"].([]any)
	r.check(len(rows) == 1 && num(obj(rows[0])["gross_sales_rial"]) == r.st.A.Orders[0].Total && num(obj(rows[0])["open_orders"]) == 1,
		"customer report after roll-forward: A's completed order and one open order")
	// Global figures minus the customer's equal the guest (unattributed) activity exactly.
	var guestGross int64
	for _, g := range append(append([]order{}, r.st.Legacy...), r.st.OldEra...) {
		if g.Status == "COMPLETED" {
			guestGross += g.Total
		}
	}
	for _, g := range r.st.NewEra[1:] {
		if g.Status == "COMPLETED" {
			guestGross += g.Total
		}
	}
	r.check(r.st.Summary["gross_sales_rial"]-num(obj(resp.body["totals"])["gross_sales_rial"]) == guestGross,
		"summary gross − customer gross = guest gross (%d)", guestGross)
	resp = admin.do(http.MethodGet, fmt.Sprintf("/api/v1/admin/customers/%d", r.st.A.ID), nil)
	t := obj(resp.body["totals"])
	r.check(num(t["orders"]) == 2 && num(t["net_collected_rial"]) == r.st.A.Orders[0].Total, "customer detail totals after roll-forward")
	resp = admin.do(http.MethodGet, "/api/v1/admin/reports/customers.xlsx?from="+monthAgo()+"&to="+today(), nil)
	r.check(resp.status == http.StatusOK && bytes.HasPrefix(resp.raw, []byte("PK")), "XLSX export after roll-forward")
	op := r.staff(r.st.OperatorUser, r.st.OperatorPassword)
	r.check(op.do(http.MethodGet, "/api/v1/admin/customers", nil).status == http.StatusForbidden, "OPERATOR still 403 for customer data")
	r.st.A.Orders = append(r.st.A.Orders, o)
}
