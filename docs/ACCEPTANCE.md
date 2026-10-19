# Acceptance Criteria — MVP

Legend: **[IT]** = automated integration test against real PostgreSQL required ·
**[UT]** = unit test · **[M]** = manual check on a real phone.

## Ordering (public)
- **AC-01** [M] Scanning the QR opens `/menu` in Persian RTL with no login; categories and products show names, toman prices with Persian digits, images, and sold-out state.
- **AC-02** [IT] Creating an order with valid items returns `201`, a daily order number, a tracking token, and server-computed totals; client-sent prices are rejected.
- **AC-03** [IT] Repeating the same request with the same `Idempotency-Key` returns the same order (same number/token) and reserves stock only once.
- **AC-04** [IT] Same key with a different body → `409 IDEMPOTENCY_KEY_REUSED`.
- **AC-05** [IT] **Concurrency:** with `available = 5`, 20 parallel orders of qty 1 → exactly 5 succeed, 15 get `INSUFFICIENT_STOCK`; `reserved = 5`; no constraint violations.
- **AC-06** [IT] Ordering an inactive or sold-out product → `422 PRODUCT_UNAVAILABLE`; nothing reserved.
- **AC-07** [IT] Outside ordering hours → `503 ORDERING_CLOSED`; at the pending cap → `503 ORDERING_BUSY`.
- **AC-08** [IT] Rate limit returns `429` with `Retry-After`.
- **AC-09** [M] Tracking page updates within ~5 s after staff change the status.
- **AC-10** [IT] Customer can cancel only while PENDING; reservations released.
- **AC-11** [IT] Unknown tracking token → `404`; order ids never appear in public responses.

## Order workflow (admin)
- **AC-12** [IT] Legal path PENDING→PREPARING→READY→COMPLETED succeeds; every illegal transition returns `409 ILLEGAL_TRANSITION` with current status.
- **AC-13** [IT] Repeating a transition to the current status returns `200` with no side effects.
- **AC-14** [IT] **Completion double-tap:** two concurrent `complete` calls → one deduction, one SALE movement per line, `reserved` and `on_hand` each reduced exactly once.
- **AC-15** [IT] Cancelling from PENDING/PREPARING/READY releases exactly the reserved quantities.
- **AC-16** [IT] A PENDING order older than the TTL is cancelled by the job with `EXPIRED`, stock released; PREPARING orders never expire.
- **AC-17** [IT] Price change after order creation does not change the order's items or total.

## Inventory & cost
- **AC-18** [UT+IT] WAC: purchase 10 @ 1,000 + 10 @ 2,000 → value 30,000; sell 5 → COGS 7,500; sell remaining 15 → COGS 22,500, value 0.
- **AC-19** [IT] `on_hand` and `inventory_value_rial` always equal the sums of the movement ledger (reconciliation check after a randomized sequence of 200 operations).
- **AC-20** [IT] Adjustment out / purchase void cannot reduce `available` below 0; reason is mandatory.
- **AC-21** [IT] DB rejects direct `DELETE` on financial/inventory/audit tables and `UPDATE` of movements/audit rows.
- **AC-22** [IT] UNTRACKED products never reserve stock and get `cogs_rial = 0` on completion.

## Payments
- **AC-23** [IT] Split payment (cash + card) reaching the total sets `PAID`; overpayment is rejected.
- **AC-24** [IT] Cancelling an order with net paid > 0 → `409 PAYMENT_STATE_CONFLICT`.
- **AC-25** [IT] Refund (ADMIN) of a PAID order → `REFUNDED`; OPERATOR refund → `403`.
- **AC-26** [IT] Voiding a payment recomputes `payment_status`; the voided row remains with reason and actor.
- **AC-27** [IT] Same payment `Idempotency-Key` twice → one payment row.

## Reports
- **AC-28** [IT] For a seeded fixture (known orders, refunds, purchases, adjustments, expenses), `/reports/summary` matches hand-computed revenue, COGS, gross profit, inventory losses, expenses, operating result, and cash/card collected.
- **AC-29** [IT] Purchases are shown but not subtracted from the operating result.
- **AC-30** [IT] Day boundaries follow Asia/Tehran (order completed 23:50 local counts on that day).
- **AC-31** [M] Report screen shows "سود ناخالص" and "نتیجه عملیاتی تخمینی" — never "سود خالص" — and the limitations footer.

## Security
- **AC-32** [IT] Every admin route has a policy entry (test enumerates chi routes).
- **AC-33** [IT] OPERATOR gets `403` on catalog edits, purchases, expenses, reports, users, refunds, cost fields absent from responses.
- **AC-34** [IT] Unsafe admin request without valid CSRF token or with foreign `Origin` → `403 CSRF_FAILED`.
- **AC-35** [IT] Deactivated user's existing session is rejected; password change revokes other sessions.
- **AC-36** [IT] The last active ADMIN cannot be deactivated or demoted.
- **AC-37** [IT] Upload of a non-image or oversize file is rejected; accepted images are re-encoded JPEG.
- **AC-38** [CI] gitleaks passes; no `.env` tracked.

## Push
- **AC-39** [IT] Crossing the threshold creates one event per ADMIN subscription; further sales in the same episode create none; a purchase above threshold re-arms.
- **AC-40** [IT] Push endpoint returning 410 deletes the subscription; 500 schedules a retry with backoff (fake push server).
- **AC-41** [M] Android Chrome and an installed iOS 16.4+ PWA receive the test notification; tapping opens the low-stock list.
- **AC-42** [M] Notification permission is requested only after tapping the enable button.

## UX & quality
- **AC-43** [M] All screens usable at 360 px width; touch targets ≥ 44 px; loading, empty and error states present on every list.
- **AC-44** [CI] Lighthouse (mobile) on `/menu`: Accessibility ≥ 95, Performance ≥ 85.
- **AC-45** [M] Admin PWA installable on Android and iOS.

## Operations
- **AC-46** [M] Fresh Windows clone → `docker compose up -d` + documented commands → app running locally in ≤ 15 min.
- **AC-47** [CI] Migrations apply from empty DB and `goose status` is clean; sqlc output is up to date (`sqlc diff`).
