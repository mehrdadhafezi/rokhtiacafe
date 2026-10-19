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
  Same key + same body → original response replayed (HTTP 200 or 201 as originally). Same key + different body → `409 IDEMPOTENCY_KEY_REUSED`.
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
| 409 | `PAYMENT_STATE_CONFLICT` | e.g. paying a PAID order, cancelling a PAID order, refunding unpaid |
| 409 | `CONFLICT` | Unique violations (e.g. duplicate username/category name), last-admin rule |
| 413 | `PAYLOAD_TOO_LARGE` | Body/image too large |
| 415 | `UNSUPPORTED_MEDIA_TYPE` | Image not JPEG/PNG/WebP |
| 422 | `VALIDATION_ERROR` | Field errors in `details.fields: { "items[0].quantity": "max" }` |
| 422 | `PRODUCT_UNAVAILABLE` | Inactive/sold-out product in order (`details.product_ids`) |
| 429 | `RATE_LIMITED` | `Retry-After` header set |
| 503 | `ORDERING_CLOSED` | Outside ordering hours |
| 503 | `ORDERING_BUSY` | Global pending-order cap reached |
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
```json
{ "customer_label": "سارا", "customer_note": "بدون شکر",
  "items": [ { "product_id": 12, "quantity": 2 }, { "product_id": 30, "quantity": 1 } ] }
```
Validation: 1–15 items, quantity 1–20, unique `product_id`, label ≤ 40, note ≤ 200,
control characters stripped. Any client-sent price fields are rejected (`422`, unknown field).

`201 Created`
```json
{ "order_number": 17, "tracking_token": "q4Vt…22+chars", "status": "PENDING",
  "payment_status": "UNPAID", "total_rial": 2350000, "expires_at": "…", "created_at": "…",
  "items": [ { "product_id": 12, "name": "اسپرسو", "quantity": 2, "unit_price_rial": 950000, "line_total_rial": 1900000 } ] }
```
Errors: `409 INSUFFICIENT_STOCK`, `422 PRODUCT_UNAVAILABLE`, `503 ORDERING_CLOSED|ORDERING_BUSY`, `429`.

### `GET /public/orders/{tracking_token}`
Same shape as above plus `cancel_reason`, `ready_at`, `completed_at`. `404` for unknown token
(constant-time lookup via unique index; no enumeration hints).

### `POST /public/orders/{tracking_token}/cancel`
Only from PENDING. `200` with order; `409 ILLEGAL_TRANSITION` otherwise. Idempotent: cancelling an
order already cancelled by the customer returns `200`.

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
| GET | `/admin/dashboard` | O | `{ open_counts: {PENDING, PREPARING, READY}, today_orders, low_stock: [{product_id, name, available, threshold}], today_revenue_rial? }` — revenue only for ADMIN |

### 5.2 Orders
| Method | Path | Role | Body / Notes |
|--------|------|------|------|
| GET | `/admin/orders` | O | `?status=PENDING,PREPARING&date=YYYY-MM-DD&before_id&limit` |
| GET | `/admin/orders/{id}` | O | Includes items, payments, timestamps, actors |
| POST | `/admin/orders/{id}/accept` | O | PENDING → PREPARING |
| POST | `/admin/orders/{id}/ready` | O | PREPARING → READY |
| POST | `/admin/orders/{id}/complete` | O | READY → COMPLETED (deducts stock, sets COGS) |
| POST | `/admin/orders/{id}/cancel` | O | `{ note }`; from PENDING/PREPARING/READY; `409 PAYMENT_STATE_CONFLICT` if any money is held (net paid > 0) |

Transition responses: `200` with the full order. Repeating a transition whose target equals the
current status returns `200` with no side effects (safe double-tap). Any other mismatch →
`409 ILLEGAL_TRANSITION`.

### 5.3 Payments
| Method | Path | Role | Body / Notes |
|--------|------|------|------|
| POST | `/admin/orders/{id}/payments` 🔁 | O | `{ method, amount_rial, note? }`; order not CANCELLED, not PAID/REFUNDED; Σ payments must not exceed total |
| POST | `/admin/orders/{id}/refunds` 🔁 | A | `{ method, note }`; net paid > 0; refunds the full net paid amount (partial refunds out of MVP) |
| POST | `/admin/payments/{id}/void` | A | `{ reason }`; marks a PAYMENT or REFUND void and recomputes status |

### 5.4 Catalog
| Method | Path | Role | Body / Notes |
|--------|------|------|------|
| GET | `/admin/categories` | O | All, incl. inactive |
| POST | `/admin/categories` | A | `{ name, sort_order? }` |
| PATCH | `/admin/categories/{id}` | A | `{ name?, sort_order?, is_active? }` |
| GET | `/admin/products` | O | `?category_id&include_inactive` ; cost fields only for ADMIN |
| POST | `/admin/products` | A | `{ category_id, name, description?, price_rial, inventory_mode, low_stock_threshold?, sort_order? }` |
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
| POST | `/admin/inventory/{product_id}/adjustments` 🔁 | A | `{ direction: "IN"\|"OUT", quantity, unit_cost_rial (IN only), reason }` |
| GET | `/admin/purchases` | A | `?product_id&from&to` |
| POST | `/admin/purchases` 🔁 | A | `{ product_id, quantity, unit_cost_rial, purchased_on, supplier?, note? }` |
| POST | `/admin/purchases/{id}/void` | A | `{ reason }`; `409 INSUFFICIENT_STOCK` if stock already consumed |

### 5.6 Expenses
| Method | Path | Role | Body |
|--------|------|------|------|
| GET | `/admin/expenses` | A | `?from&to&category` |
| POST | `/admin/expenses` 🔁 | A | `{ category, amount_rial, expense_date, description }` |
| POST | `/admin/expenses/{id}/void` | A | `{ reason }` |

### 5.7 Reports (ADMIN)
`GET /admin/reports/summary?from=2026-10-01&to=2026-10-31` (max 366 days)
```json
{ "from": "2026-10-01", "to": "2026-10-31",
  "totals": { "gross_sales_rial": 0, "refunds_rial": 0, "revenue_rial": 0, "orders": 0,
              "average_order_value_rial": 0, "cogs_rial": 0, "gross_profit_rial": 0,
              "inventory_losses_rial": 0, "expenses_rial": 0, "operating_result_rial": 0,
              "purchases_rial": 0, "collected": { "CASH": 0, "CARD_TERMINAL": 0 },
              "unpaid_completed_orders": 0 },
  "daily": [ { "date": "2026-10-01", "revenue_rial": 0, "orders": 0, "cogs_rial": 0,
               "gross_profit_rial": 0, "expenses_rial": 0 } ],
  "expenses_by_category": { "INGREDIENTS": 0 } }
```
Monthly (Jalali) views are produced by the client by requesting the Jalali month's
Gregorian date range and summing `daily`.

`GET /admin/reports/products?from&to` → `[{ product_id, name, quantity, revenue_rial, cogs_rial, gross_profit_rial, inventory_mode }]`.

Definitions: [DATABASE.md §7](DATABASE.md#7-reporting-queries-definitions).

### 5.8 Push
| Method | Path | Role | Body |
|--------|------|------|------|
| GET | `/admin/push/vapid-public-key` | A | `{ key }` |
| POST | `/admin/push/subscriptions` | A | `{ endpoint, keys: { p256dh, auth } }` (upsert by endpoint, bound to current user) |
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
