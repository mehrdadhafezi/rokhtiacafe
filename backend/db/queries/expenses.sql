-- Operational expenses (docs/DATABASE.md §2.11). Voided rows stay and are excluded from reports.

-- name: CreateExpense :one
INSERT INTO expenses (category, amount_rial, expense_date, description, idempotency_key, created_by)
VALUES (@category, @amount_rial, @expense_date, @description, @idempotency_key, @created_by)
RETURNING *;

-- name: GetExpenseByIdempotencyKey :one
SELECT * FROM expenses WHERE idempotency_key = $1;

-- name: GetExpenseForUpdate :one
SELECT * FROM expenses WHERE id = $1 FOR UPDATE;

-- name: VoidExpense :one
UPDATE expenses SET voided_at = now(), voided_by = @voided_by, void_reason = @void_reason
WHERE id = @id AND voided_at IS NULL
RETURNING *;

-- name: ListExpenses :many
SELECT * FROM expenses
WHERE (sqlc.narg('from_date')::date IS NULL OR expense_date >= sqlc.narg('from_date'))
  AND (sqlc.narg('to_date')::date IS NULL OR expense_date <= sqlc.narg('to_date'))
  AND (sqlc.narg('category')::text IS NULL OR category = sqlc.narg('category'))
  AND (sqlc.narg('before_id')::int8 IS NULL OR id < sqlc.narg('before_id'))
ORDER BY id DESC
LIMIT @row_limit;
