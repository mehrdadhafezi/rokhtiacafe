-- Customer accounts: order attribution, private history and manager reporting
-- (docs/CUSTOMER-ACCOUNTS-IMPLEMENTATION.md). Ownership comes ONLY from customer_order_links;
-- names, phones, labels, IPs and device ids are never used to attribute an order.

-- ---- order creation (inside the LOCK_ORDER_CREATE transaction) ----

-- name: LockCustomerForOrder :one
-- Taken after the creation lock and before stock, so a deactivation cannot race an order.
SELECT is_active FROM customers WHERE id = $1 FOR SHARE;

-- name: InsertCustomerOrderLink :exec
INSERT INTO customer_order_links (order_id, customer_id) VALUES (@order_id, @customer_id);

-- name: InsertCustomerOrderEvent :exec
INSERT INTO customer_events (customer_id, event_type, order_id, ip)
VALUES (@customer_id, 'ORDER_CREATED', @order_id, sqlc.narg('ip')::inet);

-- name: GetOrderCustomer :one
SELECT customer_id FROM customer_order_links WHERE order_id = $1;

-- ---- the customer's own history (filtered in SQL by the authenticated customer) ----

-- name: ListCustomerOrders :many
SELECT sqlc.embed(o)
FROM customer_order_links l
JOIN orders o ON o.id = l.order_id
WHERE l.customer_id = @customer_id
  AND (sqlc.narg('before_id')::int8 IS NULL OR l.order_id < sqlc.narg('before_id'))
ORDER BY l.order_id DESC
LIMIT @row_limit;

-- name: GetCustomerOrder :one
SELECT sqlc.embed(o)
FROM customer_order_links l
JOIN orders o ON o.id = l.order_id
WHERE l.customer_id = @customer_id AND l.order_id = @order_id;

-- name: ListCustomerEvents :many
-- Curated activity: event type, time and the customer's own order number. Never the IP.
SELECT e.id, e.event_type, e.created_at, e.order_id, o.display_number, o.business_date
FROM customer_events e
LEFT JOIN orders o ON o.id = e.order_id
WHERE e.customer_id = @customer_id
  AND (sqlc.narg('before_id')::int8 IS NULL OR e.id < sqlc.narg('before_id'))
ORDER BY e.id DESC
LIMIT @row_limit;

-- ---- manager views (ADMIN only, customers.view) ----

-- name: AdminListCustomers :many
-- All-time figures per customer. completed_gross_rial is the total of COMPLETED orders before
-- refunds; refunded_rial is what was refunded on them; receivables follow ReportReceivablesTotal.
SELECT c.id, c.first_name, c.last_name, c.phone, c.is_active, c.created_at, c.last_login_at,
       COALESCE(s.orders, 0)::int8 AS orders,
       COALESCE(s.completed_orders, 0)::int8 AS completed_orders,
       COALESCE(s.completed_gross_rial, 0)::int8 AS completed_gross_rial,
       COALESCE(s.completed_refunded_rial, 0)::int8 AS completed_refunded_rial,
       s.last_order_at::timestamptz AS last_order_at
FROM customers c
LEFT JOIN LATERAL (
  SELECT count(*) AS orders,
         count(*) FILTER (WHERE o.status = 'COMPLETED') AS completed_orders,
         sum(o.total_rial) FILTER (WHERE o.status = 'COMPLETED') AS completed_gross_rial,
         sum(o.refunded_rial) FILTER (WHERE o.status = 'COMPLETED') AS completed_refunded_rial,
         max(o.created_at) AS last_order_at
  FROM customer_order_links l
  JOIN orders o ON o.id = l.order_id
  WHERE l.customer_id = c.id
) s ON true
WHERE (sqlc.narg('search')::text IS NULL
       OR c.phone LIKE '%' || sqlc.narg('search')::text || '%'
       OR (c.first_name || ' ' || c.last_name) ILIKE '%' || sqlc.narg('search')::text || '%')
  AND (sqlc.narg('before_id')::int8 IS NULL OR c.id < sqlc.narg('before_id'))
ORDER BY c.id DESC
LIMIT @row_limit;

-- name: AdminGetCustomer :one
SELECT c.id, c.first_name, c.last_name, c.phone, c.is_active, c.phone_verified_at, c.created_at, c.last_login_at
FROM customers c WHERE c.id = $1;

-- name: AdminCustomerTotals :one
-- All-time, point-in-time figures for one customer (same definitions as the summary report).
SELECT count(*)::int8 AS orders,
       count(*) FILTER (WHERE o.status = 'COMPLETED')::int8 AS completed_orders,
       count(*) FILTER (WHERE o.status = 'CANCELLED')::int8 AS cancelled_orders,
       count(*) FILTER (WHERE o.status IN ('PENDING', 'PREPARING', 'READY'))::int8 AS open_orders,
       COALESCE(sum(o.total_rial) FILTER (WHERE o.status = 'COMPLETED'), 0)::int8 AS completed_gross_rial,
       COALESCE(sum(o.refunded_rial) FILTER (WHERE o.status = 'COMPLETED'), 0)::int8 AS completed_refunded_rial,
       COALESCE(sum(o.paid_rial), 0)::int8 AS paid_rial,
       COALESCE(sum(o.total_rial - o.paid_rial) FILTER (
         WHERE o.status = 'COMPLETED' AND o.refunded_rial = 0 AND o.paid_rial < o.total_rial), 0)::int8 AS receivable_rial
FROM customer_order_links l
JOIN orders o ON o.id = l.order_id
WHERE l.customer_id = $1;

-- name: AdminListCustomerOrders :many
-- One customer's orders created in [@from_ts, @to_ts), newest first, every status.
SELECT sqlc.embed(o)
FROM customer_order_links l
JOIN orders o ON o.id = l.order_id
WHERE l.customer_id = @customer_id
  AND o.created_at >= @from_ts AND o.created_at < @to_ts
  AND (sqlc.narg('before_id')::int8 IS NULL OR o.id < sqlc.narg('before_id'))
ORDER BY o.id DESC
LIMIT @row_limit;

-- name: ReportCustomers :many
-- Per-customer figures for [@from_ts, @to_ts), with the definitions of internal/reports:
-- gross sales = totals of orders COMPLETED in the range; sales refunds = non-void REFUND rows of
-- COMPLETED orders created in the range; prepayment refunds = REFUND rows of CANCELLED orders;
-- payments received / refunds recorded = non-void PAYMENT / REFUND rows created in the range.
-- Receivables and open orders are point in time (now). Only customers with activity in the
-- range or an outstanding amount are returned.
WITH ord AS (
  SELECT l.customer_id,
         count(*) FILTER (WHERE o.created_at >= @from_ts AND o.created_at < @to_ts) AS orders_placed,
         count(*) FILTER (WHERE o.status = 'COMPLETED' AND o.completed_at >= @from_ts AND o.completed_at < @to_ts) AS completed_orders,
         sum(o.total_rial) FILTER (WHERE o.status = 'COMPLETED' AND o.completed_at >= @from_ts AND o.completed_at < @to_ts) AS gross_sales,
         count(*) FILTER (WHERE o.status = 'CANCELLED' AND o.cancelled_at >= @from_ts AND o.cancelled_at < @to_ts) AS cancelled_orders,
         count(*) FILTER (WHERE o.status IN ('PENDING', 'PREPARING', 'READY')) AS open_orders,
         sum(o.total_rial - o.paid_rial) FILTER (
           WHERE o.status = 'COMPLETED' AND o.refunded_rial = 0 AND o.paid_rial < o.total_rial) AS receivable
  FROM customer_order_links l
  JOIN orders o ON o.id = l.order_id
  GROUP BY l.customer_id
),
pay AS (
  SELECT l.customer_id,
         sum(p.amount_rial) FILTER (WHERE p.kind = 'PAYMENT') AS received,
         sum(p.amount_rial) FILTER (WHERE p.kind = 'REFUND') AS refunds_recorded,
         sum(p.amount_rial) FILTER (WHERE p.kind = 'REFUND' AND o.status = 'COMPLETED') AS sales_refunds,
         sum(p.amount_rial) FILTER (WHERE p.kind = 'REFUND' AND o.status = 'CANCELLED') AS prepayment_refunds
  FROM customer_order_links l
  JOIN orders o ON o.id = l.order_id
  JOIN payments p ON p.order_id = o.id
  WHERE p.voided_at IS NULL AND p.created_at >= @from_ts AND p.created_at < @to_ts
  GROUP BY l.customer_id
)
SELECT c.id, c.first_name, c.last_name, c.phone,
       COALESCE(ord.orders_placed, 0)::int8 AS orders_placed,
       COALESCE(ord.completed_orders, 0)::int8 AS completed_orders,
       COALESCE(ord.cancelled_orders, 0)::int8 AS cancelled_orders,
       COALESCE(ord.open_orders, 0)::int8 AS open_orders,
       COALESCE(ord.gross_sales, 0)::int8 AS gross_sales_rial,
       COALESCE(pay.sales_refunds, 0)::int8 AS sales_refunds_rial,
       COALESCE(pay.prepayment_refunds, 0)::int8 AS prepayment_refunds_rial,
       COALESCE(pay.received, 0)::int8 AS payments_received_rial,
       COALESCE(pay.refunds_recorded, 0)::int8 AS refunds_recorded_rial,
       COALESCE(ord.receivable, 0)::int8 AS receivable_rial
FROM customers c
LEFT JOIN ord ON ord.customer_id = c.id
LEFT JOIN pay ON pay.customer_id = c.id
WHERE COALESCE(ord.orders_placed, 0) + COALESCE(ord.completed_orders, 0) + COALESCE(ord.cancelled_orders, 0) > 0
   OR pay.customer_id IS NOT NULL
   OR COALESCE(ord.receivable, 0) > 0
ORDER BY gross_sales_rial DESC, c.id
LIMIT @row_limit;

-- name: ReportCustomerOrderLines :many
-- Order-line detail of one customer's orders created in [@from_ts, @to_ts) for export: prices as
-- recorded on the order. No costs, tokens or IPs.
SELECT o.id AS order_id, o.display_number, o.business_date, o.created_at, o.completed_at, o.status,
       COALESCE(o.payment_status, '')::text AS payment_status, o.total_rial, o.paid_rial, o.refunded_rial,
       oi.product_name, oi.quantity, oi.unit_price_rial, oi.line_total_rial
FROM customer_order_links l
JOIN orders o ON o.id = l.order_id
JOIN order_items oi ON oi.order_id = o.id
WHERE l.customer_id = @customer_id
  AND o.created_at >= @from_ts AND o.created_at < @to_ts
ORDER BY o.id DESC, oi.product_id
LIMIT @row_limit;
