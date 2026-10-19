-- Inventory: balances, the movement ledger and purchases (docs/DATABASE.md §2.5–2.7, §5.1).
-- Lock order (§3.1): products FOR SHARE → inventory_balances FOR UPDATE.

-- name: GetProductForShare :one
SELECT * FROM products WHERE id = $1 FOR SHARE;

-- name: UpdateBalanceStock :one
UPDATE inventory_balances
SET on_hand = @on_hand,
    inventory_value_rial = @inventory_value_rial,
    last_unit_cost_rial = COALESCE(sqlc.narg('last_unit_cost_rial'), last_unit_cost_rial)
WHERE product_id = @product_id
RETURNING *;

-- name: SetLowStockAlerted :one
UPDATE inventory_balances SET low_stock_alerted_at = now()
WHERE product_id = $1 AND low_stock_alerted_at IS NULL
RETURNING low_stock_alerted_at;

-- name: ClearLowStockAlerted :exec
UPDATE inventory_balances SET low_stock_alerted_at = NULL
WHERE product_id = $1 AND low_stock_alerted_at IS NOT NULL;

-- name: InsertMovement :one
INSERT INTO inventory_movements (product_id, movement_type, reason_code, quantity_delta, value_delta_rial,
  on_hand_after, value_after_rial, order_id, purchase_entry_id, note, actor_user_id, idempotency_key)
VALUES (@product_id, @movement_type, @reason_code, @quantity_delta, @value_delta_rial,
  @on_hand_after, @value_after_rial, @order_id, @purchase_entry_id, @note, @actor_user_id, @idempotency_key)
RETURNING *;

-- name: GetMovementByIdempotencyKey :one
SELECT * FROM inventory_movements WHERE idempotency_key = $1;

-- name: GetPurchaseMovement :one
SELECT * FROM inventory_movements WHERE purchase_entry_id = $1 AND movement_type = 'PURCHASE';

-- name: HasOutflowAfter :one
-- True when stock left the product after the given movement (blocks exact purchase voids, F4).
SELECT EXISTS (
  SELECT 1 FROM inventory_movements
  WHERE product_id = @product_id AND id > @after_id
    AND movement_type IN ('SALE', 'WASTE', 'ADJUSTMENT_OUT', 'PURCHASE_VOID')
)::bool;

-- name: ListMovements :many
SELECT * FROM inventory_movements
WHERE product_id = @product_id
  AND (sqlc.narg('before_id')::int8 IS NULL OR id < sqlc.narg('before_id'))
ORDER BY id DESC
LIMIT @row_limit;

-- name: ListInventory :many
-- Tracked products with their balance; products without a balance row show zeros.
SELECT p.id AS product_id, p.name, p.is_active, p.low_stock_threshold,
       COALESCE(b.on_hand, 0)::int4 AS on_hand, COALESCE(b.reserved, 0)::int4 AS reserved,
       COALESCE(b.inventory_value_rial, 0)::int8 AS inventory_value_rial,
       b.last_unit_cost_rial, b.low_stock_alerted_at
FROM products p
LEFT JOIN inventory_balances b ON b.product_id = p.id
WHERE p.inventory_mode = 'TRACKED'
ORDER BY p.category_id, p.sort_order, p.id;

-- name: CreatePurchase :one
INSERT INTO purchase_entries (product_id, quantity, unit_cost_rial, total_cost_rial, supplier, purchased_on,
  note, idempotency_key, created_by)
VALUES (@product_id, @quantity, @unit_cost_rial, @total_cost_rial, @supplier, @purchased_on,
  @note, @idempotency_key, @created_by)
RETURNING *;

-- name: GetPurchaseByIdempotencyKey :one
SELECT * FROM purchase_entries WHERE idempotency_key = $1;

-- name: GetPurchaseForUpdate :one
SELECT * FROM purchase_entries WHERE id = $1 FOR UPDATE;

-- name: VoidPurchase :one
UPDATE purchase_entries SET voided_at = now(), voided_by = @voided_by, void_reason = @void_reason
WHERE id = @id AND voided_at IS NULL
RETURNING *;

-- name: ListPurchases :many
SELECT * FROM purchase_entries
WHERE (sqlc.narg('product_id')::int8 IS NULL OR product_id = sqlc.narg('product_id'))
  AND (sqlc.narg('from_date')::date IS NULL OR purchased_on >= sqlc.narg('from_date'))
  AND (sqlc.narg('to_date')::date IS NULL OR purchased_on <= sqlc.narg('to_date'))
  AND (sqlc.narg('before_id')::int8 IS NULL OR id < sqlc.narg('before_id'))
ORDER BY id DESC
LIMIT @row_limit;

-- name: ListAdminPushSubscriptions :many
-- Low-stock alerts go to active ADMIN users only (D11).
SELECT ps.id FROM push_subscriptions ps
JOIN users u ON u.id = ps.user_id
WHERE u.role = 'ADMIN' AND u.is_active
ORDER BY ps.id;

-- name: InsertNotificationEvent :exec
INSERT INTO notification_events (event_type, dedup_key, subscription_id, product_id, payload)
VALUES (@event_type, @dedup_key, @subscription_id, @product_id, @payload)
ON CONFLICT (dedup_key, subscription_id) DO NOTHING;
