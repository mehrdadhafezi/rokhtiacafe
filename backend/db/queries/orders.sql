-- Orders (docs/DATABASE.md §2.8–2.9, docs/WORKFLOWS.md §1).
-- Every order mutation starts with GetOrderForUpdate; status UPDATEs keep a WHERE guard on the
-- expected status as defence in depth.

-- name: DBNow :one
SELECT now()::timestamptz;

-- name: GetOrderByIdempotencyKey :one
SELECT * FROM orders WHERE idempotency_key = $1;

-- name: OrderExistsByIdempotencyKey :one
SELECT EXISTS (SELECT 1 FROM orders WHERE idempotency_key = $1)::bool;

-- name: GetOrderByTrackingHash :one
SELECT * FROM orders WHERE tracking_token_hash = $1;

-- name: GetOrder :one
SELECT * FROM orders WHERE id = $1;

-- name: GetOrderForUpdate :one
SELECT * FROM orders WHERE id = $1 FOR UPDATE;

-- name: CountPendingOrders :one
SELECT count(*)::int8 FROM orders WHERE status = 'PENDING';

-- name: CountPendingOrdersForClient :one
SELECT count(*)::int8 FROM orders WHERE status = 'PENDING' AND client_key = $1;

-- name: NextDisplayNumber :one
-- Called only under LOCK_ORDER_CREATE, so the number is unique and gap-free per business date.
SELECT (COALESCE(MAX(display_number), 0) + 1)::int4 FROM orders WHERE business_date = $1;

-- name: GetProductsForOrder :many
SELECT p.id, p.name, p.price_rial, p.inventory_mode, p.is_active, p.is_available,
       p.estimated_unit_cost_rial, c.is_active AS category_active
FROM products p
JOIN categories c ON c.id = p.category_id
WHERE p.id = ANY(@ids::int8[])
ORDER BY p.id
FOR SHARE OF p;

-- name: CreateOrder :one
INSERT INTO orders (business_date, display_number, tracking_token_hash, idempotency_key, request_hash,
  client_key, customer_label, customer_note, total_rial, expires_at)
VALUES (@business_date, @display_number, @tracking_token_hash, @idempotency_key, @request_hash,
  @client_key, @customer_label, @customer_note, @total_rial, now() + @pending_ttl::interval)
RETURNING *;

-- name: CreateOrderItem :one
INSERT INTO order_items (order_id, product_id, product_name, inventory_mode, unit_price_rial, quantity, line_total_rial)
VALUES (@order_id, @product_id, @product_name, @inventory_mode, @unit_price_rial, @quantity, @line_total_rial)
RETURNING *;

-- name: ListOrderItems :many
SELECT * FROM order_items WHERE order_id = $1 ORDER BY product_id;

-- name: ListOrderItemsForOrders :many
SELECT * FROM order_items WHERE order_id = ANY(@order_ids::int8[]) ORDER BY order_id, product_id;

-- name: ReserveStock :one
-- Atomic reservation: succeeds only while enough is available (no oversell).
UPDATE inventory_balances SET reserved = reserved + @qty
WHERE product_id = @product_id AND on_hand - reserved >= @qty
RETURNING *;

-- name: ReleaseReservation :one
UPDATE inventory_balances SET reserved = reserved - @qty
WHERE product_id = @product_id AND reserved >= @qty
RETURNING *;

-- name: ConsumeReservedStock :one
-- SALE / WASTE: reserved units leave physical stock at average cost.
UPDATE inventory_balances
SET on_hand = on_hand - @qty, reserved = reserved - @qty, inventory_value_rial = inventory_value_rial - @cost
WHERE product_id = @product_id AND reserved >= @qty AND on_hand >= @qty
RETURNING *;

-- name: AvailableStock :many
SELECT product_id, (on_hand - reserved)::int4 AS available
FROM inventory_balances WHERE product_id = ANY(@ids::int8[]);

-- name: AcceptOrder :one
UPDATE orders SET status = 'PREPARING', accepted_at = now(), accepted_by = @by
WHERE id = @id AND status = 'PENDING' AND expires_at > now()
RETURNING *;

-- name: MarkOrderReady :one
UPDATE orders SET status = 'READY', ready_at = now()
WHERE id = @id AND status = 'PREPARING'
RETURNING *;

-- name: CompleteOrder :one
UPDATE orders SET status = 'COMPLETED', completed_at = now(), completed_by = @by
WHERE id = @id AND status = 'READY'
RETURNING *;

-- name: CancelOrder :one
UPDATE orders SET status = 'CANCELLED', cancelled_at = now(), cancel_reason = @reason,
  cancel_note = @note, cancelled_by = @by
WHERE id = @id AND status IN ('PENDING', 'PREPARING', 'READY')
RETURNING *;

-- name: SetOrderItemCost :exec
UPDATE order_items SET cogs_rial = @cogs_rial, estimated_cost_rial = @estimated_cost_rial WHERE id = @id;

-- name: SetOrderItemWaste :exec
UPDATE order_items SET wasted_quantity = @wasted_quantity, estimated_cost_rial = @estimated_cost_rial WHERE id = @id;

-- name: GetProductEstimate :one
SELECT estimated_unit_cost_rial FROM products WHERE id = $1;

-- name: NextExpiredPendingOrder :one
-- One order per transaction; the lock taken here is the one held while it is cancelled (F2).
SELECT id FROM orders
WHERE status = 'PENDING' AND expires_at <= now()
ORDER BY expires_at, id
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- name: ListOrders :many
SELECT * FROM orders
WHERE (sqlc.narg('statuses')::text[] IS NULL OR status = ANY(sqlc.narg('statuses')::text[]))
  AND (sqlc.narg('business_date')::date IS NULL OR business_date = sqlc.narg('business_date'))
  AND (sqlc.narg('before_id')::int8 IS NULL OR id < sqlc.narg('before_id'))
ORDER BY id DESC
LIMIT @row_limit;
