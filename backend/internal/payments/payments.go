// Package payments records cash and card-terminal payments, full refunds and voids
// (docs/WORKFLOWS.md §3, docs/DATABASE.md §2.10).
//
// Every operation locks the parent order row first (F1) and keeps orders.paid_rial and
// orders.refunded_rial equal to the sums of the non-void PAYMENT and REFUND rows (INV-11) in the
// same transaction. The database CHECKs on those columns (no overpayment, full refunds only, no
// money on PENDING or CANCELLED orders) back these rules up.
package payments

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/audit"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/textutil"
)

const (
	KindPayment = "PAYMENT"
	KindRefund  = "REFUND"

	MethodCash         = "CASH"
	MethodCardTerminal = "CARD_TERMINAL"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrIdempotencyReused = errors.New("idempotency key reused with a different request")
)

// StateError means the order's payment state does not allow the operation (409).
type StateError struct{ Reason string }

func (e StateError) Error() string { return e.Reason }

// ValidationError lists invalid fields.
type ValidationError map[string]string

func (v ValidationError) Error() string { return "invalid payment input" }

// Service holds payment operations.
type Service struct {
	Pool *pgxpool.Pool
}

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func lockOrder(ctx context.Context, q *store.Queries, id int64) (store.Order, error) {
	o, err := q.GetOrderForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

// ---- record ----

// RecordInput is one payment (split tender = several payments).
type RecordInput struct {
	Method     string
	AmountRial int64
	Note       string
}

// Record adds a payment to an accepted order (PREPARING, READY or COMPLETED) without exceeding the
// remaining amount. A replay with the same Idempotency-Key and input returns the original payment.
func (s *Service) Record(ctx context.Context, actor audit.Actor, orderID int64, key pgtype.UUID, in RecordInput) (p store.Payment, o store.Order, replay bool, err error) {
	bad := ValidationError{}
	if in.Method != MethodCash && in.Method != MethodCardTerminal {
		bad["method"] = "CASH or CARD_TERMINAL"
	}
	if in.AmountRial <= 0 {
		bad["amount_rial"] = "must be positive"
	}
	in.Note = textutil.Multiline(in.Note)
	if textutil.Len(in.Note) > 200 {
		bad["note"] = "at most 200 characters"
	}
	if len(bad) > 0 {
		return p, o, false, bad
	}

	err = store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		var err error
		if o, err = lockOrder(ctx, q, orderID); err != nil {
			return err
		}
		// Under the order lock, so a concurrent duplicate on the same order is a clean replay.
		if existing, err := q.GetPaymentByIdempotencyKey(ctx, key); err == nil {
			if existing.OrderID != orderID || existing.Kind != KindPayment || existing.Method != in.Method ||
				existing.AmountRial != in.AmountRial || existing.Note.String != in.Note {
				return ErrIdempotencyReused
			}
			p, replay = existing, true
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		switch o.Status {
		case "PREPARING", "READY", "COMPLETED":
		default:
			return StateError{fmt.Sprintf("payments are recorded only for accepted orders (status %s)", o.Status)}
		}
		if o.RefundedRial > 0 {
			return StateError{"the order has been refunded"}
		}
		if remaining := o.TotalRial - o.PaidRial; in.AmountRial > remaining {
			return StateError{fmt.Sprintf("amount exceeds the remaining %d rials", remaining)}
		}
		p, err = q.InsertPayment(ctx, store.InsertPaymentParams{
			OrderID: orderID, Kind: KindPayment, Method: in.Method, AmountRial: in.AmountRial,
			Note: text(in.Note), IdempotencyKey: key, CreatedBy: actor.UserID,
		})
		if err != nil {
			return err
		}
		if o, err = q.AddOrderPaid(ctx, store.AddOrderPaidParams{ID: orderID, Delta: in.AmountRial}); err != nil {
			return err
		}
		return audit.Record(ctx, q, actor, audit.Entry{Action: "payment.record", EntityType: "order",
			EntityID: strconv.FormatInt(orderID, 10), Details: map[string]any{
				"payment_id": p.ID, "method": p.Method, "amount_rial": p.AmountRial, "paid_rial": o.PaidRial}})
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// The key was used concurrently for a payment on another order.
		return p, o, false, ErrIdempotencyReused
	}
	return p, o, replay, err
}

// ---- refunds ----

// RefundAll returns every non-void payment of the locked order, one REFUND row per PAYMENT with
// the same method and amount, and marks the order fully refunded. The caller holds the order lock
// and decides whether the refund is allowed. Used by full refunds and by cancellation with a
// prepayment.
func RefundAll(ctx context.Context, q *store.Queries, o store.Order, actor audit.Actor) ([]store.Payment, store.Order, error) {
	active, err := q.ListActiveOrderPaymentsForUpdate(ctx, o.ID)
	if err != nil {
		return nil, o, err
	}
	var sum int64
	refunds := make([]store.Payment, 0, len(active))
	for _, p := range active {
		r, err := q.InsertPayment(ctx, store.InsertPaymentParams{
			OrderID: o.ID, Kind: KindRefund, Method: p.Method, AmountRial: p.AmountRial,
			RefundOfPaymentID: pgtype.Int8{Int64: p.ID, Valid: true}, CreatedBy: actor.UserID,
		})
		if err != nil {
			return nil, o, err
		}
		sum += r.AmountRial
		refunds = append(refunds, r)
	}
	if sum != o.PaidRial {
		return nil, o, fmt.Errorf("payments: order %d paid_rial %d does not match payment rows %d", o.ID, o.PaidRial, sum)
	}
	o, err = q.SetOrderFullyRefunded(ctx, o.ID)
	return refunds, o, err
}

// Refund returns a completed, fully paid order's money in full (ADMIN). Revenue is reduced on the
// refund date; COGS is unchanged (docs/WORKFLOWS.md §3.1). Repeating it is a no-op.
func (s *Service) Refund(ctx context.Context, actor audit.Actor, orderID int64, note string) (store.Order, []store.Payment, error) {
	note = textutil.Multiline(note)
	if textutil.Len(note) > 200 {
		return store.Order{}, nil, ValidationError{"note": "at most 200 characters"}
	}
	var o store.Order
	var refunds []store.Payment
	err := store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		var err error
		if o, err = lockOrder(ctx, q, orderID); err != nil {
			return err
		}
		if o.Status == "COMPLETED" && o.RefundedRial > 0 {
			return nil // already refunded: state-idempotent
		}
		if o.Status != "COMPLETED" {
			return StateError{fmt.Sprintf("only completed orders are refunded (status %s); cancel open orders instead", o.Status)}
		}
		if o.PaidRial != o.TotalRial {
			return StateError{fmt.Sprintf("the order is not fully paid (%d of %d rials)", o.PaidRial, o.TotalRial)}
		}
		if refunds, o, err = RefundAll(ctx, q, o, actor); err != nil {
			return err
		}
		return audit.Record(ctx, q, actor, audit.Entry{Action: "payment.refund", EntityType: "order",
			EntityID: strconv.FormatInt(orderID, 10), Details: map[string]any{"refunded_rial": o.RefundedRial, "note": note}})
	})
	return o, refunds, err
}

// ---- voids ----

// Void marks a mistaken PAYMENT row void and lowers paid_rial (ADMIN). Refund rows cannot be voided
// (F10), and payments of refunded or cancelled orders stay as they are. Repeating it is a no-op.
func (s *Service) Void(ctx context.Context, actor audit.Actor, paymentID int64, reason string) (store.Payment, store.Order, error) {
	reason = textutil.Line(reason)
	if n := textutil.Len(reason); n < 1 || n > 200 {
		return store.Payment{}, store.Order{}, ValidationError{"reason": "required, at most 200 characters"}
	}
	first, err := store.New(s.Pool).GetPayment(ctx, paymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return first, store.Order{}, ErrNotFound
	}
	if err != nil {
		return first, store.Order{}, err
	}
	var p store.Payment
	var o store.Order
	err = store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		var err error
		if o, err = lockOrder(ctx, q, first.OrderID); err != nil { // order first, then the payment
			return err
		}
		if p, err = q.GetPaymentForUpdate(ctx, paymentID); err != nil {
			return err
		}
		switch {
		case p.Kind != KindPayment:
			return StateError{"refunds cannot be voided"}
		case p.VoidedAt.Valid:
			return nil // state-idempotent
		case o.Status == "CANCELLED":
			return StateError{"payments of a cancelled order cannot be voided"}
		case o.RefundedRial > 0:
			return StateError{"the order has been refunded"}
		}
		if p, err = q.VoidPayment(ctx, store.VoidPaymentParams{ID: paymentID, VoidReason: text(reason),
			VoidedBy: pgtype.Int8{Int64: actor.UserID, Valid: actor.Kind == audit.KindUser}}); err != nil {
			return err
		}
		if o, err = q.AddOrderPaid(ctx, store.AddOrderPaidParams{ID: o.ID, Delta: -p.AmountRial}); err != nil {
			return err
		}
		return audit.Record(ctx, q, actor, audit.Entry{Action: "payment.void", EntityType: "order",
			EntityID: strconv.FormatInt(o.ID, 10), Details: map[string]any{
				"payment_id": p.ID, "amount_rial": p.AmountRial, "method": p.Method, "reason": reason}})
	})
	return p, o, err
}
