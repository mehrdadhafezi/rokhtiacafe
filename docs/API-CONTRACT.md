# API Contract — `/api/v1`

## 1. Surfaces

| Prefix | Auth | Notes |
|--------|------|-------|
| `/api/v1/public/*` | none | Rate-limited per IP; no cookies read |
| `/api/v1/auth/*` | partial | Login is rate-limited; others need session |
| `/api/v1/admin/*` | session cookie + `X-CSRF-Token` on unsafe methods | Role checked by central policy |
| `/healthz`, `/readyz` | none | Not under `/api/v1` |
| `/media/products/{file}` | none | Product images; `Content-Type` fixed by server, `nosniff` |

## 2. Conventions

- JSON only (`Content-Type: application/json`), except image upload (`multipart/form-data`).
  Unknown JSON fields → `422`. Request body limit 64 KB (images 3 MB).
- Money: integers in **rials**, field names end in `_rial`. The client converts to tomans for display.
- Timestamps: RFC 3339 UTC. Date params: `YYYY-MM-DD` interpreted in Asia/Tehran.
- IDs: integers. Customer-facing order access uses `tracking_token` only.
- Pagination: `?limit=` (default 50, max 100) and `?before_id=`; response `{ "items": [...], "next_before_id": 123|null }`.
- **Idempotency**: endpoints marked 🔁 require header `Idempotency-Key: <uuid v4>`.
  Same key + same body → `200` with the resource's **current** state (even if the original request is still in flight: concurrent duplicates are serialized, F3). Same key + different body → `409 IDEMPOTENCY_KEY_REUSED`.
  Replays are evaluated before ordering hours, caps, price and stock checks, so a retry of a successful request never fails on those.
- **State-idempotent** endpoints (transitions, cancel, refund, void): repeating a call whose target state is already reached returns `200` with no new side effects.
- **Errors** (stable English codes; the frontend maps codes to Persian text):
  ```json
  { "error": { "code": "INSUFFICIENT_STOCK", "message": "insufficient stock", "details": { "product_ids": [12] } } }
  ```

| HTTP | code | When |
|------|------|------|
| 400 | `BAD_REQUEST` | Malformed JSON / missing header |
| 401 | `UNAUTHENTICATED` | No/expired session, bad credentials |
| 403 | `FORBIDDEN` | Role not allowed |
| 403 | `CSRF_FAILED` | Missing/invalid CSRF token or Origin |
| 404 | `NOT_FOUND` | Unknown id/token |
| 409 | `ILLEGAL_TRANSITION` | Status change not allowed from current status (`details.current_status`) |
| 409 | `INSUFFICIENT_STOCK` | Not enough available stock (`details.product_ids`) |
| 409 | `IDEMPOTENCY_KEY_REUSED` | Key reused with different body |
| 409 | `ORDER_EXPIRED` | Accept attempted after `expires_at`; the order is now CANCELLED/EXPIRED (F2) |
| 409 | `PRICE_CHANGED` | An expected price differs from the current price (`details.items: [{product_id, current_unit_price_rial}]`) (F13) |
| 409 | `PURCHASE_ALREADY_CONSUMED` | Purchase void rejected: stock left after the purchase (F4) |
| 409 | `PAYMENT_STATE_CONFLICT` | e.g. paying a PENDING/CANCELLED/fully paid order, overpaying, refunding a not fully paid order, voiding after a refund |
| 409 | `CONFLICT` | Unique violations (e.g. duplicate username/category name), last-admin rule |
| 413 | `PAYLOAD_TOO_LARGE` | Body/image too large |
| 415 | `UNSUPPORTED_MEDIA_TYPE` | Image not JPEG/PNG/WebP |
| 422 | `VALIDATION_ERROR` | Field errors in `details.fields: { "items[0].quantity": "max" }` |
| 422 | `PRODUCT_UNAVAILABLE` | Inactive/sold-out product in order (`details.product_ids`) |
| 429 | `RATE_LIMITED` | `Retry-After` header set |
| 429 | `TOO_MANY_OPEN_ORDERS` | Open PENDING orders from this client network reached `ORDER_MAX_PENDING_PER_CLIENT` |
| 503 | `ORDERING_CLOSED` | Outside ordering hours or ordering switched off by staff |
| 503 | `ORDERING_BUSY` | Global pending-order cap reached |
| 503 | `BUSY_RETRY` | Lock timeout; safe to retry with the same idempotency key |
| 500 | `INTERNAL` | Never includes internal detail; logged with request id |

All responses carry `X-Request-Id`.

## 3. Public

### `GET /public/menu`
`Cache-Control: no-cache` + `ETag`.
```json
{
  "ordering": { "open": true, "opens_at": "08:00", "closes_at": "23:00" },
  "categories": [
    { "id": 1, "name": "قهوه", "products": [
      { "id": 12, "name": "اسپرسو", "description": "…", "price_rial": 950000,
        "image_url": "/media/products/7f3c….jpg", "available": true }
    ]}
  ]
}
```
Only active categories/products. `available = is_available && (UNTRACKED || on_hand − reserved > 0)`.
Exact stock is never exposed.

### `POST /public/orders` 🔁
Headers: `Idempotency-Key` (required), `X-Client-Id` (random UUID kept in localStorage; used only for rate limiting).
```json
{ "customer_label": "سارا", "customer_note": "بدون شکر",
  "items": [ { "product_id": 12, "quantity": 2, "expected_unit_price_rial": 950000 },
             { "product_id": 30, "quantity": 1, "expected_unit_price_rial": 450000 } ] }
```
Validation: 1–15 items, quantity 1–20, unique `product_id`, label ≤ 40, note ≤ 200,
control characters stripped. `expected_unit_price_rial` is **required** and is only compared
with the current price (`409 PRICE_CHANGED` on mismatch); totals are always computed from DB
prices (F13). The client shows the changed prices and asks the customer to confirm, then
resubmits with a **new** idempotency key.

`201 Created` (replay: `200`, same shape, current state)
```json
{ "order_number": 17, "tracking_token": "<43 chars, base64url>", "status": "PENDING",
  "payment_status": "UNPAID", "total_rial": 2350000, "expires_at": "…", "created_at": "…",
  "items": [ { "product_id": 12, "name": "اسپرسو", "quantity": 2, "unit_price_rial": 950000, "line_total_rial": 1900000 } ] }
```
Errors: `409 INSUFFICIENT_STOCK|PRICE_CHANGED|IDEMPOTENCY_KEY_REUSED`, `422 PRODUCT_UNAVAILABLE`,
`503 ORDERING_CLOSED|ORDERING_BUSY|BUSY_RETRY`, `429 RATE_LIMITED|TOO_MANY_OPEN_ORDERS`.

### Tracking token transport (F11)
The token is sent in the `X-Tracking-Token` header, **never in a URL path or query** (keeps it out
of access logs, proxies and `Referer`). The customer page is `/menu/order#<token>`; the fragment is
not sent to the server. The server stores only `SHA-256(token)`; tokens stop working 24 h after
order creation (`404`).

### `GET /public/order`
Header `X-Tracking-Token`. Same shape as above plus `cancel_reason`, `ready_at`, `completed_at`,
`expired` (true when PENDING and past `expires_at`). `404` for unknown/old token.

### `POST /public/order/cancel`
Header `X-Tracking-Token`. Only from PENDING (orders in PENDING never hold money). `200` with order;
`409 ILLEGAL_TRANSITION` otherwise. Already cancelled by the customer or expired → `200`.

## 4. Auth

| Method | Path | Body | Response |
|--------|------|------|----------|
| POST | `/auth/login` | `{username, password}` | `200 {user, csrf_token}` + `Set-Cookie` |
| POST | `/auth/logout` | – | `204`, cookie cleared, session row deleted |
| GET | `/auth/me` | – | `200 {user, csrf_token}` or `401` |
| POST | `/auth/password` | `{current_password, new_password}` | `204`; all other sessions of the user revoked |

`user = { id, username, display_name, role }`. Login failures always return
`401 UNAUTHENTICATED` with the same message and similar timing.

## 5. Admin

Role column: **O** = OPERATOR and ADMIN, **A** = ADMIN only.

### 5.1 Dashboard
| Method | Path | Role | Notes |
|--------|------|------|-------|
| GET | `/admin/dashboard` | O | `{ open_counts: {PENDING, PREPARING, READY}, today_orders, low_stock: [{product_id, name, available, threshold}], ordering_open, today_revenue_rial? }` — revenue only for ADMIN |
| PUT | `/admin/ordering` | A | `{ open: bool }` — emergency kill switch for public ordering (F11). In memory, audited; resets to `ORDERING_HOURS` behaviour on restart (owner decision O4) |

### 5.2 Orders
| Method | Path | Role | Body / Notes |
|--------|------|------|------|
| GET | `/admin/orders` | O | `?status=PENDING,PREPARING&date=YYYY-MM-DD&before_id&limit` |
| GET | `/admin/orders/{id}` | O | Includes items, payments, timestamps, actors |
| POST | `/admin/orders/{id}/accept` | O | PENDING → PREPARING; after `expires_at` → order is expired and `409 ORDER_EXPIRED` |
| POST | `/admin/orders/{id}/ready` | O | PREPARING → READY |
| POST | `/admin/orders/{id}/complete` | O | READY → COMPLETED (deducts stock, sets COGS) |
| POST | `/admin/orders/{id}/cancel` | O | `{ note, waste?: [{product_id, quantity}] }`; from PENDING/PREPARING/READY; `waste` only from PREPARING/READY (F7). Prepayments are refunded automatically (one REFUND per PAYMENT, same method); response includes `refunds: [{method, amount_rial}]` to hand back |

Transition responses: `200` with the full order. Repeating a transition whose target equals the
current status returns `200` with no side effects (safe double-tap). Any other mismatch →
`409 ILLEGAL_TRANSITION`.

### 5.3 Payments
| Method | Path | Role | Body / Notes |
|--------|------|------|------|
| POST | `/admin/orders/{id}/payments` 🔁 | O | `{ method, amount_rial, note? }`; status PREPARING/READY/COMPLETED; no refund on the order; `amount ≤ total − paid` |
| POST | `/admin/orders/{id}/refund` | A | `{ note }`; COMPLETED and PAID; creates one REFUND per PAYMENT (same method/amount); state-idempotent. Pre-completion refunds happen only via cancel |
| POST | `/admin/payments/{id}/void` | A | `{ reason }`; PAYMENT rows only; order not CANCELLED and not refunded; state-idempotent |

### 5.4 Catalog
| Method | Path | Role | Body / Notes |
|--------|------|------|------|
| GET | `/admin/categories` | O | All, incl. inactive |
| POST | `/admin/categories` | A | `{ name, sort_order? }` |
| PATCH | `/admin/categories/{id}` | A | `{ name?, sort_order?, is_active? }` |
| GET | `/admin/products` | O | `?category_id&include_inactive` ; cost fields only for ADMIN |
| POST | `/admin/products` | A | `{ category_id, name, description?, price_rial, inventory_mode, low_stock_threshold? (TRACKED), estimated_unit_cost_rial? (UNTRACKED, F8), sort_order? }` |
| GET | `/admin/products/{id}` | O | |
| PATCH | `/admin/products/{id}` | A | Any of the create fields + `is_active`; price change audited with old/new |
| PATCH | `/admin/products/{id}/availability` | O | `{ is_available }` — sold-out toggle |
| PUT | `/admin/products/{id}/image` | A | multipart `file`; JPEG/PNG/WebP ≤ 3 MB, ≤ 4000×4000 px; re-encoded to JPEG 800 px max |
| DELETE | `/admin/products/{id}/image` | A | |

### 5.5 Inventory & purchases
| Method | Path | Role | Body / Notes |
|--------|------|------|------|
| GET | `/admin/inventory` | O | `[{product_id, name, on_hand, reserved, available, threshold, low}]` + `avg_unit_cost_rial`, `inventory_value_rial` for ADMIN |
| GET | `/admin/inventory/{product_id}/movements` | A | Paginated ledger |
| POST | `/admin/inventory/{product_id}/adjustments` 🔁 | A | `{ direction: "IN"\|"OUT", quantity, unit_cost_rial (IN only), reason_code, note }` — IN: `OPENING_BALANCE`/`COUNT_CORRECTION`/`RETURN_TO_STOCK`; OUT: `COUNT_CORRECTION`/`DAMAGED`/`EXPIRED` (F10) |
| GET | `/admin/purchases` | A | `?product_id&from&to` |
| POST | `/admin/purchases` 🔁 | A | `{ product_id, quantity, unit_cost_rial, purchased_on, supplier?, note? }` |
| POST | `/admin/purchases/{id}/void` | A | `{ reason }`; exact reversal; `409 PURCHASE_ALREADY_CONSUMED` if any outflow happened after it, `409 INSUFFICIENT_STOCK` if reserved (F4) |

### 5.6 Expenses
| Method | Path | Role | Body |
|--------|------|------|------|
| GET | `/admin/expenses` | A | `?from&to&category` |
| POST | `/admin/expenses` 🔁 | A | `{ category, amount_rial, expense_date, description }` |
| POST | `/admin/expenses/{id}/void` | A | `{ reason }` |

### 5.7 Reports (ADMIN)
`GET /admin/reports/summary?from=2026-10-01&to=2026-10-31` (business dates in Asia/Tehran, inclusive, max 366 days)
```json
{ "from": "2026-10-01", "to": "2026-10-31", "timezone": "Asia/Tehran",
  "totals": { "gross_sales_rial": 0, "sales_refunds_rial": 0, "revenue_rial": 0, "orders": 0,
              "average_order_value_rial": 0, "cogs_rial": 0, "gross_profit_rial": 0,
              "inventory_losses_rial": 0, "inventory_gains_rial": 0,
              "expenses_rial": 0, "operating_result_rial": 0,
              "purchases_rial": 0, "prepayment_refunds_rial": 0,
              "payments_received": { "CASH": 0, "CARD_TERMINAL": 0 },
              "refunds_recorded":  { "CASH": 0, "CARD_TERMINAL": 0 },
              "collected":         { "CASH": 0, "CARD_TERMINAL": 0 },   // received − refunds recorded
              "estimated_waste_rial": 0 },                              // informational (F7/F8), never in the operating result
  "daily": [ { "date": "2026-10-01", "…": "same fields as totals" } ],  // every date in the range, empty days included
  "expenses_by_category": { "INGREDIENTS": 0 },
  "receivables": { "orders": 0, "amount_rial": 0 } }                    // point in time (now), F9
```
A REFUND row **records** that money is owed back to the customer; the system does not move money.
Handing cash back or reversing the card-terminal transaction is a staff action, which is why refund
and cancel responses list the amounts per method and the report separates `refunds_recorded` from
`payments_received`.

Monthly (Jalali) views are produced by the client by requesting the Jalali month's
Gregorian date range and summing `daily`.

`GET /admin/reports/products?from&to` → `{ from, to, items: [{ product_id, name, inventory_mode, quantity, revenue_rial, cogs_rial, gross_profit_rial, estimated_cost_rial|null, estimated_margin_rial|null, lines_without_estimate }] }`.
Revenue here is line revenue of orders completed in the range, before order-level refunds.
For UNTRACKED products `cogs_rial` is 0 and the estimated fields carry the management estimate (F8);
the UI labels them «تخمینی» and never adds them to the summary.

`GET /admin/reports/receivables` → `{ items: [{ order_id, order_number, business_date, completed_at, total_rial, paid_rial, outstanding_rial }], total_outstanding_rial }` — completed orders with `total_rial − paid_rial > 0` and no refund, oldest first.

Definitions: [DATABASE.md §7](DATABASE.md#7-reporting-queries-definitions).

### 5.8 Push
| Method | Path | Role | Body |
|--------|------|------|------|
| GET | `/admin/push/vapid-public-key` | A | `{ key }` |
| POST | `/admin/push/subscriptions` | A | `{ endpoint, keys: { p256dh, auth } }` (upsert by endpoint, bound to current user). Endpoint must be `https`, ≤ 2048 chars, and on the push-service allowlist (SECURITY §12) → else `422` (F11) |
| DELETE | `/admin/push/subscriptions` | A | `{ endpoint }` |
| POST | `/admin/push/test` | A | Sends a TEST notification to the caller's subscriptions |

### 5.9 Users & audit (ADMIN)
| Method | Path | Body |
|--------|------|------|
| GET | `/admin/users` | |
| POST | `/admin/users` | `{ username, display_name, role, password }` |
| PATCH | `/admin/users/{id}` | `{ display_name?, role?, is_active? }` — deactivation revokes sessions; last-admin rule |
| POST | `/admin/users/{id}/password` | `{ new_password }` — revokes that user's sessions |
| GET | `/admin/audit-logs` | `?entity_type&entity_id&before_id&limit` |

## 6. Versioning
Breaking changes require `/api/v2`. Additive fields are non-breaking; the client ignores unknown fields.
