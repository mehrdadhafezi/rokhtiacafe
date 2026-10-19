// Package check verifies the invariants that are enforced by application logic rather than by
// database constraints (docs/DATABASE.md §3). It only reads, so it runs as the runtime role.
package check

import (
	"context"
	"fmt"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
)

// Violation describes one broken invariant.
type Violation struct {
	Invariant string
	Detail    string
}

func (v Violation) String() string { return v.Invariant + ": " + v.Detail }

// Run executes every reconciliation query and returns all violations found.
func Run(ctx context.Context, q store.Querier) ([]Violation, error) {
	var out []Violation
	add := func(inv, format string, args ...any) {
		out = append(out, Violation{Invariant: inv, Detail: fmt.Sprintf(format, args...)})
	}

	reserved, err := q.CheckReservedMatchesOpenOrders(ctx)
	if err != nil {
		return nil, fmt.Errorf("reserved check: %w", err)
	}
	for _, r := range reserved {
		add("INV-3", "product %d reserved=%d but open tracked order quantity=%d", r.ProductID, r.Reserved, r.Expected)
	}

	ledger, err := q.CheckBalancesMatchLedger(ctx)
	if err != nil {
		return nil, fmt.Errorf("ledger check: %w", err)
	}
	for _, r := range ledger {
		add("INV-4", "product %d on_hand=%d value=%d but ledger qty=%d value=%d",
			r.ProductID, r.OnHand, r.InventoryValueRial, r.LedgerQty, r.LedgerValue)
	}

	orphans, err := q.CheckOrphanLedgerProducts(ctx)
	if err != nil {
		return nil, fmt.Errorf("orphan ledger check: %w", err)
	}
	for _, id := range orphans {
		add("INV-4", "product %d has movements but no inventory balance", id)
	}

	missing, err := q.CheckTrackedProductsHaveBalance(ctx)
	if err != nil {
		return nil, fmt.Errorf("balance presence check: %w", err)
	}
	for _, id := range missing {
		add("INV-4", "tracked product %d has no inventory balance row", id)
	}

	totals, err := q.CheckOrderTotals(ctx)
	if err != nil {
		return nil, fmt.Errorf("order total check: %w", err)
	}
	for _, r := range totals {
		add("INV-7b", "order %d total=%d but lines sum to %d", r.OrderID, r.TotalRial, r.LinesTotal)
	}

	payments, err := q.CheckOrderPaymentTotals(ctx)
	if err != nil {
		return nil, fmt.Errorf("payment total check: %w", err)
	}
	for _, r := range payments {
		add("INV-11", "order %d paid=%d refunded=%d but payment rows paid=%d refunded=%d",
			r.OrderID, r.PaidRial, r.RefundedRial, r.RowsPaid, r.RowsRefunded)
	}

	cogs, err := q.CheckCompletedOrdersHaveCogs(ctx)
	if err != nil {
		return nil, fmt.Errorf("cogs check: %w", err)
	}
	for _, r := range cogs {
		add("INV-8", "order %d (%s) line %d has cogs set inconsistently with its status", r.OrderID, r.Status, r.ItemID)
	}

	mirrors, err := q.CheckRefundsMirrorPayments(ctx)
	if err != nil {
		return nil, fmt.Errorf("refund mirror check: %w", err)
	}
	for _, r := range mirrors {
		add("INV-11", "refund %d does not mirror a non-void payment of the same order, method and amount (payment %d)", r.RefundID, r.PaymentID.Int64)
	}

	admins, err := q.CheckActiveAdminExists(ctx)
	if err != nil {
		return nil, fmt.Errorf("admin check: %w", err)
	}
	if admins.Users > 0 && admins.ActiveAdmins == 0 {
		add("INV-16", "%d users exist but none is an active ADMIN", admins.Users)
	}
	return out, nil
}
