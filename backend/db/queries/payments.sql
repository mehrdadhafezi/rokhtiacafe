-- Payments and refunds (docs/DATABASE.md §2.10, docs/WORKFLOWS.md §3).
-- Every payment change runs under the parent order's row lock (F1); lock order: order → payments.

-- name: GetPayment :one
SELECT * FROM payments WHERE id = $1;

-- name: GetPaymentForUpdate :one
SELECT * FROM payments WHERE id = $1 FOR UPDATE;

-- name: GetPaymentByIdempotencyKey :one
SELECT * FROM payments WHERE idempotency_key = $1;

-- name: ListOrderPayments :many
SELECT * FROM payments WHERE order_id = $1 ORDER BY id;

-- name: ListActiveOrderPaymentsForUpdate :many
-- Non-void PAYMENT rows that a full refund returns, one REFUND each.
SELECT * FROM payments
WHERE order_id = $1 AND kind = 'PAYMENT' AND voided_at IS NULL
ORDER BY id
FOR UPDATE;

-- name: InsertPayment :one
INSERT INTO payments (order_id, kind, method, amount_rial, refund_of_payment_id, note, idempotency_key, created_by)
VALUES (@order_id, @kind, @method, @amount_rial, @refund_of_payment_id, @note, @idempotency_key, @created_by)
RETURNING *;

-- name: VoidPayment :one
UPDATE payments SET voided_at = now(), voided_by = @voided_by, void_reason = @void_reason
WHERE id = @id AND kind = 'PAYMENT' AND voided_at IS NULL
RETURNING *;

-- name: AddOrderPaid :one
UPDATE orders SET paid_rial = paid_rial + @delta WHERE id = @id RETURNING *;

-- name: SetOrderFullyRefunded :one
UPDATE orders SET refunded_rial = paid_rial WHERE id = $1 RETURNING *;

-- name: CheckRefundsMirrorPayments :many
-- F9: every REFUND returns exactly one non-void PAYMENT of the same order, method and amount.
SELECT r.id AS refund_id, r.refund_of_payment_id AS payment_id
FROM payments r
JOIN payments p ON p.id = r.refund_of_payment_id
WHERE r.kind = 'REFUND'
  AND (p.kind <> 'PAYMENT' OR p.voided_at IS NOT NULL OR p.order_id <> r.order_id
       OR p.method <> r.method OR p.amount_rial <> r.amount_rial)
ORDER BY r.id;
