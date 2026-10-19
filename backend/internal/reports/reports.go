// Package reports computes the management reports defined in docs/DATABASE.md §7 under the
// revenue, refund and receivable policy of docs/WORKFLOWS.md §3.1.
//
// Definitions (all bucketed by the business date of each row's own timestamp):
//   - Gross sales: totals of orders COMPLETED in the range (revenue is recognised at completion).
//   - Sales refunds: REFUND rows of COMPLETED orders, on the refund date.
//   - Revenue = gross sales − sales refunds. COGS (actual, moving average) is not reversed by refunds.
//   - Gross profit = revenue − COGS. It is not net profit.
//   - Inventory losses: ADJUSTMENT_OUT and WASTE value; gains: ADJUSTMENT_IN with COUNT_CORRECTION or
//     RETURN_TO_STOCK (OPENING_BALANCE has no P&L effect).
//   - Estimated operating result = gross profit − losses + gains − expenses.
//   - Purchases are shown for information only: they become COGS when sold, so subtracting them
//     would count the same money twice.
//   - Payments received / refunds recorded / net collected are per method by row date. A REFUND
//     row records that money must go back to the customer; handing it back is a staff action.
//   - Prepayment refunds (REFUND rows of CANCELLED orders) are cash flow only, never revenue.
//   - Receivables are point in time: completed, unrefunded orders that are not fully paid.
//   - Estimated costs of made-to-order drinks appear only in the product and waste estimates.
package reports

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
)

// MaxRangeDays limits one report request.
const MaxRangeDays = 366

// ErrRange is returned for an invalid or too long date range.
var ErrRange = errors.New("invalid date range")

// Service computes reports.
type Service struct {
	Pool     *pgxpool.Pool
	Location *time.Location
}

// Range is an inclusive range of business dates.
type Range struct {
	From, To time.Time // UTC midnights of the calendar dates
}

// ParseRange validates from/to (YYYY-MM-DD, inclusive, at most MaxRangeDays).
func ParseRange(from, to string) (Range, error) {
	f, err1 := time.Parse(time.DateOnly, from)
	t, err2 := time.Parse(time.DateOnly, to)
	if err1 != nil || err2 != nil || t.Before(f) || t.Sub(f) >= MaxRangeDays*24*time.Hour {
		return Range{}, ErrRange
	}
	return Range{From: f, To: t}, nil
}

// timestamps returns [start of From, start of the day after To) in the business timezone.
func (s *Service) timestamps(r Range) (pgtype.Timestamptz, pgtype.Timestamptz) {
	start := time.Date(r.From.Year(), r.From.Month(), r.From.Day(), 0, 0, 0, 0, s.Location)
	end := time.Date(r.To.Year(), r.To.Month(), r.To.Day()+1, 0, 0, 0, 0, s.Location)
	return pgtype.Timestamptz{Time: start, Valid: true}, pgtype.Timestamptz{Time: end, Valid: true}
}

func dates(r Range) (pgtype.Date, pgtype.Date) {
	return pgtype.Date{Time: r.From, Valid: true}, pgtype.Date{Time: r.To, Valid: true}
}

// Methods are the tender types, in display order.
var Methods = []string{"CASH", "CARD_TERMINAL"}

// ByMethod holds one amount per tender type.
type ByMethod map[string]int64

func newByMethod() ByMethod { return ByMethod{"CASH": 0, "CARD_TERMINAL": 0} }

// Figures are the metrics for a day or for the whole range.
type Figures struct {
	GrossSalesRial        int64    `json:"gross_sales_rial"`
	SalesRefundsRial      int64    `json:"sales_refunds_rial"`
	RevenueRial           int64    `json:"revenue_rial"`
	Orders                int64    `json:"orders"`
	AverageOrderValueRial int64    `json:"average_order_value_rial"`
	COGSRial              int64    `json:"cogs_rial"`
	GrossProfitRial       int64    `json:"gross_profit_rial"`
	InventoryLossesRial   int64    `json:"inventory_losses_rial"`
	InventoryGainsRial    int64    `json:"inventory_gains_rial"`
	ExpensesRial          int64    `json:"expenses_rial"`
	OperatingResultRial   int64    `json:"operating_result_rial"`
	PurchasesRial         int64    `json:"purchases_rial"`
	PrepaymentRefundsRial int64    `json:"prepayment_refunds_rial"`
	PaymentsReceived      ByMethod `json:"payments_received"`
	RefundsRecorded       ByMethod `json:"refunds_recorded"`
	Collected             ByMethod `json:"collected"` // payments received − refunds recorded
	EstimatedWasteRial    int64    `json:"estimated_waste_rial"`
}

func newFigures() *Figures {
	return &Figures{PaymentsReceived: newByMethod(), RefundsRecorded: newByMethod(), Collected: newByMethod()}
}

func (f *Figures) finish() {
	f.RevenueRial = f.GrossSalesRial - f.SalesRefundsRial
	if f.Orders > 0 {
		f.AverageOrderValueRial = (f.GrossSalesRial + f.Orders/2) / f.Orders
	}
	f.GrossProfitRial = f.RevenueRial - f.COGSRial
	f.OperatingResultRial = f.GrossProfitRial - f.InventoryLossesRial + f.InventoryGainsRial - f.ExpensesRial
	for _, m := range Methods {
		f.Collected[m] = f.PaymentsReceived[m] - f.RefundsRecorded[m]
	}
}

func (f *Figures) add(o *Figures) {
	f.GrossSalesRial += o.GrossSalesRial
	f.SalesRefundsRial += o.SalesRefundsRial
	f.Orders += o.Orders
	f.COGSRial += o.COGSRial
	f.InventoryLossesRial += o.InventoryLossesRial
	f.InventoryGainsRial += o.InventoryGainsRial
	f.ExpensesRial += o.ExpensesRial
	f.PurchasesRial += o.PurchasesRial
	f.PrepaymentRefundsRial += o.PrepaymentRefundsRial
	f.EstimatedWasteRial += o.EstimatedWasteRial
	for _, m := range Methods {
		f.PaymentsReceived[m] += o.PaymentsReceived[m]
		f.RefundsRecorded[m] += o.RefundsRecorded[m]
	}
}

// Day is one business date's figures.
type Day struct {
	Date string `json:"date"`
	*Figures
}

// Receivables is the point-in-time outstanding amount.
type Receivables struct {
	Orders     int64 `json:"orders"`
	AmountRial int64 `json:"amount_rial"`
}

// Summary is the response of GET /admin/reports/summary.
type Summary struct {
	From               string           `json:"from"`
	To                 string           `json:"to"`
	Timezone           string           `json:"timezone"`
	Totals             *Figures         `json:"totals"`
	Daily              []Day            `json:"daily"`
	ExpensesByCategory map[string]int64 `json:"expenses_by_category"`
	Receivables        Receivables      `json:"receivables"`
}

// Summary computes the report for an inclusive range of business dates. Every date in the range
// has a daily entry, so the client can sum Jalali months from Gregorian days.
func (s *Service) Summary(ctx context.Context, r Range) (Summary, error) {
	q := store.New(s.Pool)
	fromTs, toTs := s.timestamps(r)
	fromDate, toDate := dates(r)
	tz := s.Location.String()

	days := map[string]*Figures{}
	var order []string
	for d := r.From; !d.After(r.To); d = d.AddDate(0, 0, 1) {
		k := d.Format(time.DateOnly)
		days[k] = newFigures()
		order = append(order, k)
	}
	day := func(d pgtype.Date) *Figures {
		if f, ok := days[d.Time.Format(time.DateOnly)]; ok {
			return f
		}
		return newFigures() // outside the range (cannot happen with the WHERE clauses); ignored
	}

	completed, err := q.ReportCompletedOrdersByDay(ctx, store.ReportCompletedOrdersByDayParams{Tz: tz, FromTs: fromTs, ToTs: toTs})
	if err != nil {
		return Summary{}, err
	}
	for _, row := range completed {
		f := day(row.Day)
		f.Orders, f.GrossSalesRial, f.COGSRial = row.Orders, row.GrossSales, row.Cogs
	}

	pays, err := q.ReportPaymentsByDay(ctx, store.ReportPaymentsByDayParams{Tz: tz, FromTs: fromTs, ToTs: toTs})
	if err != nil {
		return Summary{}, err
	}
	for _, row := range pays {
		f := day(row.Day)
		if row.Kind == "PAYMENT" {
			f.PaymentsReceived[row.Method] += row.Amount
			continue
		}
		f.RefundsRecorded[row.Method] += row.Amount
		switch row.OrderStatus {
		case "COMPLETED":
			f.SalesRefundsRial += row.Amount
		case "CANCELLED":
			f.PrepaymentRefundsRial += row.Amount
		}
	}

	moves, err := q.ReportInventoryValueByDay(ctx, store.ReportInventoryValueByDayParams{Tz: tz, FromTs: fromTs, ToTs: toTs})
	if err != nil {
		return Summary{}, err
	}
	for _, row := range moves {
		f := day(row.Day)
		switch {
		case row.MovementType == "ADJUSTMENT_OUT" || row.MovementType == "WASTE":
			f.InventoryLossesRial += -row.ValueDelta
		case row.MovementType == "ADJUSTMENT_IN" && (row.ReasonCode == "COUNT_CORRECTION" || row.ReasonCode == "RETURN_TO_STOCK"):
			f.InventoryGainsRial += row.ValueDelta
		}
	}

	waste, err := q.ReportEstimatedWasteByDay(ctx, store.ReportEstimatedWasteByDayParams{Tz: tz, FromTs: fromTs, ToTs: toTs})
	if err != nil {
		return Summary{}, err
	}
	for _, row := range waste {
		day(row.Day).EstimatedWasteRial += row.EstimatedWaste
	}

	byCategory := map[string]int64{}
	exps, err := q.ReportExpensesByDay(ctx, store.ReportExpensesByDayParams{FromDate: fromDate, ToDate: toDate})
	if err != nil {
		return Summary{}, err
	}
	for _, row := range exps {
		day(row.Day).ExpensesRial += row.Amount
		byCategory[row.Category] += row.Amount
	}

	purchases, err := q.ReportPurchasesByDay(ctx, store.ReportPurchasesByDayParams{FromDate: fromDate, ToDate: toDate})
	if err != nil {
		return Summary{}, err
	}
	for _, row := range purchases {
		day(row.Day).PurchasesRial += row.Amount
	}

	rec, err := q.ReportReceivablesTotal(ctx)
	if err != nil {
		return Summary{}, err
	}

	out := Summary{From: r.From.Format(time.DateOnly), To: r.To.Format(time.DateOnly), Timezone: tz,
		Totals: newFigures(), ExpensesByCategory: byCategory, Receivables: Receivables{Orders: rec.Orders, AmountRial: rec.Amount}}
	for _, k := range order {
		f := days[k]
		f.finish()
		out.Totals.add(f)
		out.Daily = append(out.Daily, Day{Date: k, Figures: f})
	}
	out.Totals.finish()
	return out, nil
}

// ProductLine is one product in the product report.
type ProductLine struct {
	ProductID         int64  `json:"product_id"`
	Name              string `json:"name"`
	InventoryMode     string `json:"inventory_mode"`
	Quantity          int64  `json:"quantity"`
	RevenueRial       int64  `json:"revenue_rial"`
	COGSRial          int64  `json:"cogs_rial"`
	GrossProfitRial   int64  `json:"gross_profit_rial"`
	EstimatedCostRial *int64 `json:"estimated_cost_rial"`   // UNTRACKED only; management estimate
	EstimatedMargin   *int64 `json:"estimated_margin_rial"` // UNTRACKED only; never in the summary
	LinesNoEstimate   int64  `json:"lines_without_estimate"`
}

// Products reports per-product sales of orders completed in the range. Revenue is line revenue
// before order-level refunds. Actual COGS applies to TRACKED products; UNTRACKED products carry
// an estimated cost and margin instead (F8), shown separately from gross profit.
func (s *Service) Products(ctx context.Context, r Range) ([]ProductLine, error) {
	fromTs, toTs := s.timestamps(r)
	rows, err := store.New(s.Pool).ReportProducts(ctx, store.ReportProductsParams{FromTs: fromTs, ToTs: toTs})
	if err != nil {
		return nil, err
	}
	out := make([]ProductLine, 0, len(rows))
	for _, row := range rows {
		l := ProductLine{ProductID: row.ProductID, Name: row.Name, InventoryMode: row.InventoryMode, Quantity: row.Quantity,
			RevenueRial: row.Revenue, COGSRial: row.Cogs, GrossProfitRial: row.Revenue - row.Cogs, LinesNoEstimate: row.LinesWithoutEstimate}
		if row.InventoryMode == "UNTRACKED" && row.LinesWithEstimate > 0 {
			est, margin := row.EstimatedCost, row.Revenue-row.EstimatedCost
			l.EstimatedCostRial, l.EstimatedMargin = &est, &margin
		}
		out = append(out, l)
	}
	return out, nil
}

// Receivable is one completed order that is not fully paid.
type Receivable struct {
	OrderID         int64     `json:"order_id"`
	OrderNumber     int32     `json:"order_number"`
	BusinessDate    string    `json:"business_date"`
	CompletedAt     time.Time `json:"completed_at"`
	TotalRial       int64     `json:"total_rial"`
	PaidRial        int64     `json:"paid_rial"`
	OutstandingRial int64     `json:"outstanding_rial"`
}

// Receivables lists completed, unrefunded orders that are not fully paid (oldest first).
func (s *Service) Receivables(ctx context.Context) ([]Receivable, error) {
	rows, err := store.New(s.Pool).ListReceivables(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Receivable, 0, len(rows))
	for _, row := range rows {
		out = append(out, Receivable{OrderID: row.ID, OrderNumber: row.DisplayNumber, BusinessDate: row.BusinessDate.Time.Format(time.DateOnly),
			CompletedAt: row.CompletedAt.Time, TotalRial: row.TotalRial, PaidRial: row.PaidRial, OutstandingRial: row.OutstandingRial})
	}
	return out, nil
}

// Today returns today's business date range.
func (s *Service) Today() Range {
	now := time.Now().In(s.Location)
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return Range{From: d, To: d}
}
