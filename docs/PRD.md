# PRD — Rokh Tia Café (MVP)

Status: **Draft for architecture review** · Owner: Mehrdad Hafezi · Last updated: 2026-10-19

## 1. Problem

Rokh is a small café run inside an existing branch. Today the menu, orders, stock,
and money are tracked by hand. The owner needs one simple tool, used mostly from a
phone, that covers the day: customers order by QR, staff prepare and get paid, and the
owner sees stock and profit.

## 2. Goals

| # | Goal | Measure of success |
|---|------|--------------------|
| G1 | Customers order without staff help | A customer can scan → order → get a number in < 60 s on 4G |
| G2 | Staff handle orders from a phone | New order visible on admin screen within 10 s; 1 tap per status change |
| G3 | Stock never oversold | Zero orders accepted beyond available stock (tested under concurrency) |
| G4 | Owner trusts the numbers | Daily/monthly revenue, COGS, gross profit and expenses reconcile with records |
| G5 | Owner warned before items run out | Low-stock warning on dashboard always; Web Push when the device supports it |

## 3. Non-goals (MVP)

Online payment, customer accounts, loyalty, discounts/coupons, tax/VAT calculation,
table management, kitchen display hardware, recipes/BOM/ingredient stock, multi-branch,
multi-tenant, supplier management, payroll, full double-entry accounting, receipt
printing, delivery, English UI.

## 4. Users and roles

| Actor | Auth | Description |
|-------|------|-------------|
| Customer | None | Scans QR, orders, tracks own order via secret link |
| OPERATOR | Login | Barista/cashier: handles orders, records payments, marks items sold out |
| ADMIN | Login | Owner: everything an operator can do + catalog, prices, stock, purchases, expenses, reports, users, refunds |

Full permission matrix: [SECURITY.md §3](SECURITY.md#3-permission-matrix).

## 5. Functional scope

### 5.1 Customer (public, `/menu`)
- C1 Browse active categories and products (name, price in toman, image, sold-out state).
- C2 Cart: add/remove/change quantity; cart persists in the browser (localStorage).
- C3 Optional name/label (≤ 40 chars) and note (≤ 200 chars, e.g. "بدون شکر").
- C4 Submit order → receive a **daily order number** (e.g. «۱۷») and a private tracking link.
- C5 Track status (در انتظار تأیید / در حال آماده‌سازی / آماده تحویل / تحویل شد / لغو شد), auto-refreshing.
- C6 Cancel own order while it is still PENDING.
- C7 Clear messages when ordering is closed, busy, an item sold out, or rate-limited.

### 5.2 Operator + Admin (`/admin`)
- A1 Login/logout; change own password.
- A2 Dashboard: open orders by status, today's order count, low-stock list (ADMIN also sees today's revenue).
- A3 Orders board: accept → mark ready → complete; cancel with note; audible/visual cue for new orders while the tab is open.
- A4 Record payment (CASH / CARD_TERMINAL); split tender allowed.
- A5 Toggle product "sold out" (`is_available`).
- A6 Enable Web Push on their device (button-initiated) for low-stock alerts (ADMIN).

### 5.3 Admin only
- B1 Categories: create, rename, reorder, deactivate.
- B2 Products: create, edit, price, image, deactivate, inventory mode (TRACKED/UNTRACKED), low-stock threshold.
- B3 Purchases: record stock purchases for tracked products (qty, unit cost); void with reason.
- B4 Inventory: view on-hand / reserved / available / average cost; adjust in/out with mandatory reason; movement history.
- B5 Expenses: record by category; void with reason.
- B6 Refund a paid order; void a mistaken payment.
- B7 Reports: daily & monthly (Jalali) revenue, orders, AOV, product sales, COGS, gross profit, expenses, estimated operating result, cash vs card collected.
- B8 Users: create operators/admins, deactivate, reset password.
- B9 Audit log viewer.

## 6. Key product decisions

| Topic | Decision |
|-------|----------|
| Currency | Stored as BIGINT **rials**; shown as **tomans** (rial ÷ 10) with Persian digits |
| Made-to-order drinks | `UNTRACKED` products: no stock; COGS = 0; ingredient purchases are expenses. See [DATABASE.md §5](DATABASE.md#5-cost-model) |
| Packaged goods | `TRACKED` products: integer stock, reservations, moving weighted average cost |
| Pending orders | Expire after **20 min** without acceptance → auto-cancel, stock released |
| Price changes | Order keeps the price shown at creation; cost snapshot taken at completion |
| Paid + cancel | A PAID order cannot be cancelled until refunded |
| Live updates | Polling (customer 5 s, admin 5 s); no WebSocket/SSE in MVP |
| Ordering hours | Configured opening hours; outside them the menu is view-only |
| Calendar | Jalali dates in UI; reports by Asia/Tehran business day |

## 7. Non-functional requirements

- Persian, RTL, mobile-first, WCAG 2.1 AA contrast, 44×44 px minimum touch targets.
- Customer menu: first load ≤ 200 KB JS gzipped for the public route; LCP < 2.5 s on mid-range Android over 4G.
- Single server, single process; ~ hundreds of orders/day.
- No secrets in repo; PostgreSQL never public.
- Financial and inventory history is append-only (void/reverse, never delete).

## 8. Release criteria

All acceptance criteria in [ACCEPTANCE.md](ACCEPTANCE.md) pass; CI green; production
server inspection completed and deployment plan approved by owner.
