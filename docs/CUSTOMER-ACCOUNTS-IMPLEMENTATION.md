# Customer accounts, order history and manager reporting — implementation contract

Status: **Development / draft**. This is not deployed or approved for production.
C1–C5 are implemented on the feature branches (C3–C5: `feat/c3-c5-customer-orders-ui-reports`);
C6 release readiness, the R1 client-IP gate and staging rehearsals remain open.
Baseline: `c468ffce27067af2714284b0a68034b0d9aa9d23`.
This document and `00008_customer_accounts.sql` are the first additive development slice; they do **not** enable customer registration by themselves.

## Business scope

- Customers of Rokh café, **not** sellers and not staff. Staff `ADMIN` / `OPERATOR` remain in the existing `users` and `sessions` tables.
- Customer registers with first name, last name, Iranian mobile phone and a password; logs in with phone + password.
- Name, phone and account activity are retained subject to a future approved data-retention/privacy policy.
- All **new authenticated** orders receive one immutable row in `customer_order_links` in the same creation transaction as order creation and stock reservation.
- Customer can see only their own order history, including ordered items, historical unit-price snapshots, current status and payment status.
- Managers (ADMIN only) can view customer summaries, per-customer order details and export them. Reporting distinguishes completed sales, actual collected payments, refunds and cancelled orders.
- Existing anonymous orders stay anonymous. Do not infer ownership from `customer_label`, device IDs, IP addresses or phone guesses; future claims require separate proof of ownership.

## Identity and session safety

- One phone per account, canonical form `09XXXXXXXXX` (11 ASCII digits); normalize Persian/Arabic digits and `+98`/leading `98` at the API boundary before checking uniqueness. Reject invalid inputs.
- Registration requires first and last name (1–60 chars each) and a strong password (use existing `auth.ValidatePassword` + Argon2id). Passwords must never be logged or exposed by public/admin APIs.
- **Phone ownership is not verified in MVP.** Record `phone_verified_at = NULL`; UI must not describe unverified phones as verified. Production activation should explicitly decide SMS/OTP and safe recovery.
- Customer cookies and sessions are wholly separate from staff: proposed cookie `__Host-rokh_customer` (Secure, HttpOnly, SameSite=Lax, Path=/). Only a SHA-256 session-token hash is persisted in `customer_sessions`. No staff session grants customer access, and vice versa.
- Session absolute lifetime <= 7 days; idle timeout <= 12 hours; revoke on password change and deactivation. Rotate session token on login; never persist or log raw token.
- Use CSRF protection and Origin/Referer validation for customer endpoints that rely on cookies, including registration, logout, profile edits and order creation.
- Independent throttling per trusted client IP and per normalized phone on registration and login; generic errors on unknown phone and wrong password; account enumeration prevention.
- Set `Cache-Control: no-store` on all customer auth, profile, history and manager customer/report endpoints. Verify CDN `Mc-Cache-Status != HIT` in every case.
- Do not expose raw IP or User-Agent in customer UI or exports. Security activity history is a curated event log, not full access logs. Define retention and deletion/anonymization policy before public launch.
- No plaintext credentials in files, deployment commands, SQL, URLs, logs or screenshots.

## API contract (implemented in C2–C5)

| Method | Path (under `/api/v1`) | Authorization | Behavior |
|---|---|---|---|
| POST | `/customer/auth/register` | public + origin + rate-limit | First/last name, phone, password; server-owned session; generic duplicate-phone response |
| POST | `/customer/auth/login` | public + origin + rate-limit | Mobile and password; rotated session cookie |
| GET | `/customer/auth/me` | customer cookie | Minimal account profile; never password hash |
| POST | `/customer/auth/logout` | customer + CSRF | Revoke session |
| GET | `/customer/orders?before_id&limit` | customer | Paginated own orders; items and payment state; no tracking token needed |
| GET | `/customer/orders/{id}` | customer | 404 for any unowned ID (never reveal existence) |
| GET | `/customer/activity?before_id&limit` | customer | Sanitized events for this customer only |
| GET | `/admin/customers?search&before_id&limit` | ADMIN | Customers list with limited PII |
| GET | `/admin/customers/{id}/orders?from&to&before_id&limit` | ADMIN | Order history including cancelled and pending; dates in Asia/Tehran |
| GET | `/admin/reports/customers?from&to` | ADMIN | Aggregates per customer; clear gross/refund/net definitions |
| GET | `/admin/reports/customers.csv?from&to` | ADMIN | UTF-8 BOM CSV, protected from spreadsheet formula injection |
| GET | `/admin/reports/customers.xlsx?from&to` | ADMIN | The same report as a real Office Open XML workbook (RTL, definitions sheet) |
| GET | `/admin/customers/{id}` | ADMIN | Profile (phone shown as unverified) and all-time totals |
| GET | `/admin/reports/customers/{id}/orders.csv?from&to` and `.xlsx` | ADMIN | Order-line detail export, with prices as recorded on order |

`GET /customer/*` must be authenticated with customer cookies only; existing public `GET /public/order` tracking-token flow remains backward compatible.

`POST /public/orders` should accept an optional authenticated customer session; if present, the service must bind the customer ID in the same locked order-creation transaction and in the idempotent replay response check. Never accept `customer_id` from client JSON. An idempotency key replay by a different account must never expose or claim an order owned by another account. Decide whether guest ordering stays permitted before enabling UI; this slice preserves it.

## Pages (implemented in C4–C5)

- `/menu/register`, `/menu/login`: mobile-first Persian RTL, accessible labels, clear privacy notice and no claim of SMS verification.
- `/menu/account`, `/menu/my-orders`, `/menu/my-orders/:id`: protect personal data; avoid caching private content.
- `/admin/customers`, `/admin/customers/:id`: ADMIN-only; order history and export.
- Add `Customers` navigation and `Customer Reports` under existing admin/report permissions; do not leak admin bundle into customer pages.

## Reporting correctness

- Use `customer_order_links` joined by `orders.id = customer_order_links.order_id` as the only ownership link. No `customer_label` matching.
- Completed sales: `orders.status = 'COMPLETED'` grouped by `completed_at` in Asia/Tehran; `total_rial` is **gross** sale.
- Refunds: `refunded_rial` and payment ledger semantics must follow existing reports. A completed order can later be refunded, so distinguish historical gross sale from current refund state and date of refund.
- Collected payments: derive from non-void payments and recorded refunds, not `total_rial`; never mislabel an unpaid completed order as money received.
- Show PENDING, PREPARING, READY, CANCELLED separately from completed sales. Never combine cancelled order totals with revenue.
- For exports, user-supplied text fields beginning with `=`, `+`, `-`, `@`, tab, CR or LF must be escaped so spreadsheets do not interpret them as formulas; limit export size and paginate large windows.
- Admin-only server-side policy must protect both JSON and CSV endpoints; no client-only permission logic.

## Development acceptance

1. Migration applies on PostgreSQL 16 in an isolated database created from a verified backup, and no previous rows/amounts change.
2. Migration is forward-compatible with previous binary during rollout; no customer routes enabled until new binary passes CI.
3. Duplicate phone registration is safe under concurrent requests; invalid mobile and weak password rejected.
4. Customer A cannot read customer B's profile, activity or any order ID. Staff session is not a customer session.
5. Order-create inserts a `customer_order_links` row only when the caller is authenticated, inside the same transaction as stock reservation; guest ordering still works.
6. Idempotency replay across accounts cannot transfer or reveal order ownership; all existing status transitions and financial protections pass.
7. Personal activity logs retain event types/time but no secrets, tokens or raw authorization data.
8. Admin CSV has authorization, bounds, formula-injection tests and matches database figures for completed/refunded/unpaid/cancelled orders.
9. RTL/mobile tests, logout/re-login, session expiry and password-change revocation pass.
10. Upgrade/rollback rehearsal, new encrypted off-site backup, security client-IP issue R1 and full production release gates remain mandatory.

## Work breakdown

- **Slice C1 — database foundation:** additive migration `00008_customer_accounts.sql` (this branch).
- **Slice C2 — secure customer auth:** register/login/logout/me, session and CSRF middleware, tests.
- **Slice C3 — customer-bound ordering and history:** atomic ownership, idempotency, history endpoints, tests.
- **Slice C4 — customer frontend:** login/register/account/orders, responsive RTL.
- **Slice C5 — manager view & export:** reporting API/UI, CSV/XLSX export, financial reconciliation and access tests.
- **Slice C6 — release readiness:** isolated restore/migration, staging E2E, privacy approval, OTP/recovery decision, R1 remediation, rollout/rollback.

No `migrate up`, production restart, live table modification, or deployment is authorized by this branch alone.

## Implementation notes (C3–C5)

- **Authorization:** every manager route uses the new policy action `customers.view` (ADMIN only,
  registered through `guarded.handle`); OPERATOR receives 403 for customer data and downloads.
- **Order attribution:** `POST /public/orders` with a customer cookie requires a live session, the app
  Origin and the customer CSRF token. A stale, revoked, forged or deactivated session answers
  `401 CUSTOMER_SESSION_EXPIRED` and never becomes a guest order. `orders.Create` locks the customer
  row `FOR SHARE` after `LOCK_ORDER_CREATE` and inserts `customer_order_links` and
  `customer_events.ORDER_CREATED` in the creation transaction.
- **Idempotency:** the request hash includes the customer id with `omitempty`, so guest hashes are
  byte-identical to the previous binary; a replay by another customer or a guest is
  `409 IDEMPOTENCY_KEY_REUSED` without details, and replays re-check the stored owner.
- **Reports:** figures follow `internal/reports` (gross at completion, sales refunds and prepayment
  refunds by refund date, payments received / refunds recorded by row date, receivables point in
  time). Ranges are inclusive Asia/Tehran business dates, at most 366 days and 5000 rows
  (20000 lines for one customer's export).
- **Exports:** CSV is UTF-8 with BOM; XLSX is written without third-party dependencies. Text that a
  spreadsheet could read as a formula (`= + - @`, full-width forms, leading tab/CR/LF) is prefixed
  with `'` in CSV and stored as a quote-prefixed inline string in XLSX; no hashes, tokens or IPs.
- **C2 audit fixes:** login and registration revoke the session the browser presented; a password
  change revokes every session and issues a fresh one; phone input accepts spaces, hyphens and
  `0098`; invalid phones are charged to the per-IP limit only.
- **Backward compatibility:** no new migration; `00008` is unchanged. The previous binary ignores the
  customer tables; guest orders, tracking tokens and their hashes are unchanged.
