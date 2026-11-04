package reports

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
)

// Customer reports attribute orders ONLY through customer_order_links (never by name, phone,
// label or IP). Guest orders are therefore not part of any customer row, and the totals of this
// report equal the summary report only when every order in the range belongs to a customer.

// MaxCustomerRows bounds one customer report or export; a wider result must be narrowed.
const MaxCustomerRows = 5000

// MaxCustomerLines bounds one per-customer order-line export.
const MaxCustomerLines = 20000

// ErrTooManyRows means the range holds more rows than one report may return.
var ErrTooManyRows = errors.New("too many rows for one report")

// CustomerDefinitions explain every customer-report figure (shown in the UI and the exports).
var CustomerDefinitions = map[string]string{
	"orders_placed":           "orders the customer created in the range, any status",
	"completed_orders":        "orders completed in the range",
	"cancelled_orders":        "orders cancelled in the range (never revenue)",
	"open_orders":             "orders pending, preparing or ready now (point in time: counts orders created before the range too)",
	"gross_sales_rial":        "totals of orders completed in the range (revenue is recognised at completion)",
	"sales_refunds_rial":      "refund rows of completed orders recorded in the range",
	"revenue_rial":            "gross sales minus sales refunds",
	"prepayment_refunds_rial": "refunds of cancelled orders' prepayments in the range (cash flow only)",
	"payments_received_rial":  "non-void payments recorded in the range",
	"refunds_recorded_rial":   "all non-void refund rows recorded in the range",
	"net_collected_rial":      "payments received minus refunds recorded",
	"receivable_rial":         "completed, unrefunded orders not fully paid (point in time: not limited to the range)",
}

// CustomerFigures are one customer's (or the total's) figures for a range.
type CustomerFigures struct {
	OrdersPlaced          int64 `json:"orders_placed"`
	CompletedOrders       int64 `json:"completed_orders"`
	CancelledOrders       int64 `json:"cancelled_orders"`
	OpenOrders            int64 `json:"open_orders"`
	GrossSalesRial        int64 `json:"gross_sales_rial"`
	SalesRefundsRial      int64 `json:"sales_refunds_rial"`
	RevenueRial           int64 `json:"revenue_rial"`
	PrepaymentRefundsRial int64 `json:"prepayment_refunds_rial"`
	PaymentsReceivedRial  int64 `json:"payments_received_rial"`
	RefundsRecordedRial   int64 `json:"refunds_recorded_rial"`
	NetCollectedRial      int64 `json:"net_collected_rial"`
	ReceivableRial        int64 `json:"receivable_rial"`
}

func (f *CustomerFigures) add(o CustomerFigures) {
	f.OrdersPlaced += o.OrdersPlaced
	f.CompletedOrders += o.CompletedOrders
	f.CancelledOrders += o.CancelledOrders
	f.OpenOrders += o.OpenOrders
	f.GrossSalesRial += o.GrossSalesRial
	f.SalesRefundsRial += o.SalesRefundsRial
	f.RevenueRial += o.RevenueRial
	f.PrepaymentRefundsRial += o.PrepaymentRefundsRial
	f.PaymentsReceivedRial += o.PaymentsReceivedRial
	f.RefundsRecordedRial += o.RefundsRecordedRial
	f.NetCollectedRial += o.NetCollectedRial
	f.ReceivableRial += o.ReceivableRial
}

// CustomerRow is one customer in the report.
type CustomerRow struct {
	CustomerID int64  `json:"customer_id"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	Phone      string `json:"phone"`
	CustomerFigures
}

// CustomerReport is the response of GET /admin/reports/customers.
type CustomerReport struct {
	From        string            `json:"from"`
	To          string            `json:"to"`
	Timezone    string            `json:"timezone"`
	Customers   []CustomerRow     `json:"customers"`
	Totals      CustomerFigures   `json:"totals"`
	Definitions map[string]string `json:"definitions"`
}

// Customers computes per-customer figures for an inclusive range of business dates.
func (s *Service) Customers(ctx context.Context, r Range) (CustomerReport, error) {
	from, to := s.timestamps(r)
	out := CustomerReport{From: r.From.Format("2006-01-02"), To: r.To.Format("2006-01-02"), Timezone: s.Location.String(),
		Customers: []CustomerRow{}, Definitions: CustomerDefinitions}
	err := store.InSnapshot(ctx, s.Pool, func(q *store.Queries) error {
		rows, err := q.ReportCustomers(ctx, store.ReportCustomersParams{FromTs: from, ToTs: to, RowLimit: MaxCustomerRows + 1})
		if err != nil {
			return err
		}
		if len(rows) > MaxCustomerRows {
			return ErrTooManyRows
		}
		for _, x := range rows {
			f := CustomerFigures{OrdersPlaced: x.OrdersPlaced, CompletedOrders: x.CompletedOrders,
				CancelledOrders: x.CancelledOrders, OpenOrders: x.OpenOrders, GrossSalesRial: x.GrossSalesRial,
				SalesRefundsRial: x.SalesRefundsRial, PrepaymentRefundsRial: x.PrepaymentRefundsRial,
				PaymentsReceivedRial: x.PaymentsReceivedRial, RefundsRecordedRial: x.RefundsRecordedRial,
				ReceivableRial: x.ReceivableRial}
			f.RevenueRial = f.GrossSalesRial - f.SalesRefundsRial
			f.NetCollectedRial = f.PaymentsReceivedRial - f.RefundsRecordedRial
			out.Customers = append(out.Customers, CustomerRow{CustomerID: x.ID, FirstName: x.FirstName,
				LastName: x.LastName, Phone: x.Phone, CustomerFigures: f})
			out.Totals.add(f)
		}
		return nil
	})
	return out, err
}

// CustomerLines returns the order lines of one customer's orders created in the range (for export).
func (s *Service) CustomerLines(ctx context.Context, customerID int64, r Range) ([]store.ReportCustomerOrderLinesRow, error) {
	from, to := s.timestamps(r)
	rows, err := store.New(s.Pool).ReportCustomerOrderLines(ctx, store.ReportCustomerOrderLinesParams{
		CustomerID: customerID, FromTs: from, ToTs: to, RowLimit: MaxCustomerLines + 1})
	if err != nil {
		return nil, err
	}
	if len(rows) > MaxCustomerLines {
		return nil, ErrTooManyRows
	}
	return rows, nil
}

// Window returns [from, to) timestamps of a range in the business timezone.
func (s *Service) Window(r Range) (pgtype.Timestamptz, pgtype.Timestamptz) { return s.timestamps(r) }
