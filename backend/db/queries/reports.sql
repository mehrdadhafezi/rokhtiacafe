-- Financial reports (docs/DATABASE.md §7, docs/WORKFLOWS.md §3.1). Every metric is bucketed by the
-- business date of its own timestamp in the business timezone (@tz). Ranges are half-open
-- [@from_ts, @to_ts) for timestamps and inclusive [@from_date, @to_date] for date columns.

-- name: ReportCompletedOrdersByDay :many
-- Gross sales, order count and COGS of orders completed in the range (revenue recognised at completion).
SELECT (o.completed_at AT TIME ZONE @tz::text)::date AS day,
       count(*)::int8 AS orders,
       sum(o.total_rial)::int8 AS gross_sales,
       COALESCE(sum(c.cogs), 0)::int8 AS cogs
FROM orders o
LEFT JOIN (SELECT order_id, sum(cogs_rial) AS cogs FROM order_items GROUP BY order_id) c ON c.order_id = o.id
WHERE o.status = 'COMPLETED' AND o.completed_at >= @from_ts AND o.completed_at < @to_ts
GROUP BY 1
ORDER BY 1;

-- name: ReportPaymentsByDay :many
-- Non-void payment and refund rows by day, kind, method and the order's current status.
SELECT (p.created_at AT TIME ZONE @tz::text)::date AS day, p.kind, p.method, o.status AS order_status,
       sum(p.amount_rial)::int8 AS amount
FROM payments p
JOIN orders o ON o.id = p.order_id
WHERE p.voided_at IS NULL AND p.created_at >= @from_ts AND p.created_at < @to_ts
GROUP BY 1, 2, 3, 4
ORDER BY 1;

-- name: ReportInventoryValueByDay :many
-- Value moved by manual adjustments and order waste (losses are negative deltas).
SELECT (m.created_at AT TIME ZONE @tz::text)::date AS day, m.movement_type, COALESCE(m.reason_code, '')::text AS reason_code,
       sum(m.value_delta_rial)::int8 AS value_delta
FROM inventory_movements m
WHERE m.movement_type IN ('ADJUSTMENT_IN', 'ADJUSTMENT_OUT', 'WASTE')
  AND m.created_at >= @from_ts AND m.created_at < @to_ts
GROUP BY 1, 2, 3
ORDER BY 1;

-- name: ReportEstimatedWasteByDay :many
-- Estimated cost of wasted made-to-order items (information only, never in the operating result).
SELECT (o.cancelled_at AT TIME ZONE @tz::text)::date AS day, sum(oi.estimated_cost_rial)::int8 AS estimated_waste
FROM order_items oi
JOIN orders o ON o.id = oi.order_id
WHERE o.status = 'CANCELLED' AND oi.wasted_quantity > 0 AND oi.estimated_cost_rial IS NOT NULL
  AND o.cancelled_at >= @from_ts AND o.cancelled_at < @to_ts
GROUP BY 1
ORDER BY 1;

-- name: ReportExpensesByDay :many
SELECT expense_date AS day, category, sum(amount_rial)::int8 AS amount
FROM expenses
WHERE voided_at IS NULL AND expense_date >= @from_date AND expense_date <= @to_date
GROUP BY 1, 2
ORDER BY 1;

-- name: ReportPurchasesByDay :many
SELECT purchased_on AS day, sum(total_cost_rial)::int8 AS amount
FROM purchase_entries
WHERE voided_at IS NULL AND purchased_on >= @from_date AND purchased_on <= @to_date
GROUP BY 1
ORDER BY 1;

-- name: ReportReceivablesTotal :one
-- Point in time (now): completed, not refunded, not fully paid.
SELECT count(*)::int8 AS orders, COALESCE(sum(total_rial - paid_rial), 0)::int8 AS amount
FROM orders
WHERE status = 'COMPLETED' AND refunded_rial = 0 AND paid_rial < total_rial;

-- name: ListReceivables :many
SELECT id, display_number, business_date, completed_at, total_rial, paid_rial, (total_rial - paid_rial)::int8 AS outstanding_rial
FROM orders
WHERE status = 'COMPLETED' AND refunded_rial = 0 AND paid_rial < total_rial
ORDER BY completed_at, id;

-- name: ReportProducts :many
-- Per-product sales of orders completed in the range. Revenue here is line revenue before
-- order-level refunds; actual COGS for TRACKED lines, estimated cost for UNTRACKED lines.
SELECT oi.product_id, p.name, oi.inventory_mode,
       sum(oi.quantity)::int8 AS quantity,
       sum(oi.line_total_rial)::int8 AS revenue,
       COALESCE(sum(oi.cogs_rial), 0)::int8 AS cogs,
       COALESCE(sum(oi.estimated_cost_rial), 0)::int8 AS estimated_cost,
       COALESCE(sum(oi.line_total_rial) FILTER (WHERE oi.estimated_cost_rial IS NOT NULL), 0)::int8 AS estimated_revenue,
       count(oi.estimated_cost_rial)::int8 AS lines_with_estimate,
       count(*) FILTER (WHERE oi.inventory_mode = 'UNTRACKED' AND oi.estimated_cost_rial IS NULL)::int8 AS lines_without_estimate
FROM order_items oi
JOIN orders o ON o.id = oi.order_id
JOIN products p ON p.id = oi.product_id
WHERE o.status = 'COMPLETED' AND o.completed_at >= @from_ts AND o.completed_at < @to_ts
GROUP BY oi.product_id, p.name, oi.inventory_mode
ORDER BY revenue DESC, oi.product_id;

-- name: DashboardOpenCounts :many
SELECT status, count(*)::int8 AS n FROM orders
WHERE status IN ('PENDING', 'PREPARING', 'READY')
GROUP BY status;

-- name: DashboardOrdersSince :one
SELECT count(*)::int8 FROM orders WHERE created_at >= $1;

-- name: DashboardLowStock :many
SELECT p.id AS product_id, p.name, (b.on_hand - b.reserved)::int4 AS available, p.low_stock_threshold::int4 AS threshold
FROM products p
JOIN inventory_balances b ON b.product_id = p.id
WHERE p.inventory_mode = 'TRACKED' AND p.is_active AND p.low_stock_threshold IS NOT NULL
  AND b.on_hand - b.reserved <= p.low_stock_threshold
ORDER BY (b.on_hand - b.reserved), p.id;
