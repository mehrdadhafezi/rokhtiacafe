-- Reconciliation queries for app-enforced invariants (docs/DATABASE.md §3). Each returns
-- only violating rows, so an empty result means the invariant holds.

-- name: CheckReservedMatchesOpenOrders :many
-- INV-3: reserved = open (PENDING/PREPARING/READY) tracked order quantities.
SELECT b.product_id, b.reserved, COALESCE(o.open_qty, 0)::int8 AS expected
FROM inventory_balances b
LEFT JOIN (
  SELECT oi.product_id, sum(oi.quantity) AS open_qty
  FROM order_items oi
  JOIN orders ord ON ord.id = oi.order_id
  WHERE ord.status IN ('PENDING', 'PREPARING', 'READY') AND oi.inventory_mode = 'TRACKED'
  GROUP BY oi.product_id
) o ON o.product_id = b.product_id
WHERE b.reserved <> COALESCE(o.open_qty, 0)
ORDER BY b.product_id;

-- name: CheckBalancesMatchLedger :many
-- INV-4: on_hand and inventory value equal the sums of the movement ledger.
SELECT b.product_id, b.on_hand, b.inventory_value_rial,
       COALESCE(m.qty, 0)::int8 AS ledger_qty, COALESCE(m.value, 0)::int8 AS ledger_value
FROM inventory_balances b
LEFT JOIN (
  SELECT product_id, sum(quantity_delta) AS qty, sum(value_delta_rial) AS value
  FROM inventory_movements
  GROUP BY product_id
) m ON m.product_id = b.product_id
WHERE b.on_hand <> COALESCE(m.qty, 0) OR b.inventory_value_rial <> COALESCE(m.value, 0)
ORDER BY b.product_id;

-- name: CheckOrphanLedgerProducts :many
-- INV-4: movements for a product without a balance row.
SELECT DISTINCT m.product_id
FROM inventory_movements m
LEFT JOIN inventory_balances b ON b.product_id = m.product_id
WHERE b.product_id IS NULL
ORDER BY m.product_id;

-- name: CheckTrackedProductsHaveBalance :many
SELECT p.id AS product_id
FROM products p
LEFT JOIN inventory_balances b ON b.product_id = p.id
WHERE p.inventory_mode = 'TRACKED' AND b.product_id IS NULL
ORDER BY p.id;

-- name: CheckOrderTotals :many
-- INV-7b: order total = sum of its line totals.
SELECT o.id AS order_id, o.total_rial, COALESCE(sum(oi.line_total_rial), 0)::int8 AS lines_total
FROM orders o
LEFT JOIN order_items oi ON oi.order_id = o.id
GROUP BY o.id, o.total_rial
HAVING o.total_rial <> COALESCE(sum(oi.line_total_rial), 0)
ORDER BY o.id;

-- name: CheckOrderPaymentTotals :many
-- INV-11: paid/refunded amounts on orders equal the non-void payment rows.
SELECT o.id AS order_id, o.paid_rial, o.refunded_rial,
       COALESCE(p.paid, 0)::int8 AS rows_paid, COALESCE(p.refunded, 0)::int8 AS rows_refunded
FROM orders o
LEFT JOIN (
  SELECT order_id,
         sum(amount_rial) FILTER (WHERE kind = 'PAYMENT' AND voided_at IS NULL) AS paid,
         sum(amount_rial) FILTER (WHERE kind = 'REFUND') AS refunded
  FROM payments
  GROUP BY order_id
) p ON p.order_id = o.id
WHERE o.paid_rial <> COALESCE(p.paid, 0) OR o.refunded_rial <> COALESCE(p.refunded, 0)
ORDER BY o.id;

-- name: CheckCompletedOrdersHaveCogs :many
-- INV-8: every line of a completed order has cogs_rial; no other order has any.
SELECT oi.id AS item_id, oi.order_id, o.status
FROM order_items oi
JOIN orders o ON o.id = oi.order_id
WHERE (o.status = 'COMPLETED') <> (oi.cogs_rial IS NOT NULL)
ORDER BY oi.id;

-- name: CheckActiveAdminExists :one
-- INV-16 (only meaningful once users exist).
SELECT
  (SELECT count(*) FROM users)::int8 AS users,
  (SELECT count(*) FROM users WHERE role = 'ADMIN' AND is_active)::int8 AS active_admins;
