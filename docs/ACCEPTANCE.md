# Acceptance Criteria — MVP

Legend: **[IT]** = automated integration test against real PostgreSQL required ·
**[UT]** = unit test · **[M]** = manual check on a real phone · **[CI]** = pipeline check.
Concurrency tests run each scenario at least 50 times with goroutines released by a shared
barrier, and assert final DB state (not only HTTP codes).

## Ordering (public)
- **AC-01** [M] Scanning the QR opens `/menu` in Persian RTL with no login and no install prompt; categories and products show names, toman prices with Persian digits, images, and sold-out state.
- **AC-02** [IT] Creating an order with valid items and matching `expected_unit_price_rial` returns `201`, a daily order number, a tracking token, and totals computed from DB prices.
- **AC-03** [IT] Repeating the same request with the same `Idempotency-Key` returns `200` with the same order (same number, same token) and reserves stock only once.
- **AC-04** [IT] Same key with a different body → `409 IDEMPOTENCY_KEY_REUSED`.
- **AC-05** [IT] **Concurrency:** with `available = 5`, 20 parallel orders of qty 1 (distinct keys) → exactly 5 succeed, 15 get `INSUFFICIENT_STOCK`; `reserved = 5`.
- **AC-06** [IT] Ordering an inactive or sold-out product → `422 PRODUCT_UNAVAILABLE`; nothing reserved.
- **AC-07** [IT] Outside ordering hours or with the kill switch off → `503 ORDERING_CLOSED`; at the global cap → `503 ORDERING_BUSY`; at the per-client cap → `429 TOO_MANY_OPEN_ORDERS`.
- **AC-08** [IT] Rate limit returns `429` with `Retry-After`.
- **AC-09** [M] Tracking page updates within ~5 s after staff change the status.
- **AC-10** [IT] Customer can cancel only while PENDING; reservations released; repeat → `200`.
- **AC-11** [IT] Unknown or > 24 h old tracking token → `404`; order ids never appear in public responses; token never appears in a URL path/query.

## Order workflow (admin)
- **AC-12** [IT] Legal path PENDING→PREPARING→READY→COMPLETED succeeds; every illegal transition returns `409 ILLEGAL_TRANSITION` with current status.
- **AC-13** [IT] Repeating a transition to the current status returns `200` with no side effects.
- **AC-14** [IT] **Completion double-tap:** two concurrent `complete` calls → one deduction, one SALE movement per line, `reserved` and `on_hand` each reduced exactly once.
- **AC-15** [IT] Cancelling from PENDING/PREPARING/READY without waste releases exactly the reserved quantities.
- **AC-16** [IT] A PENDING order past its deadline is cancelled by the job with `EXPIRED`, stock released; PREPARING orders never expire.
- **AC-17** [IT] Price change after order creation does not change the order's items or total.

## Inventory & cost
- **AC-18** [UT+IT] WAC: purchase 10 @ 1,000 + 10 @ 2,000 → value 30,000; sell 5 → COGS 7,500; sell remaining 15 → COGS 22,500, value 0.
- **AC-19** [IT] `on_hand` and `inventory_value_rial` always equal the sums of the movement ledger, and `reserved` equals open tracked order quantities (`rokh check` passes after a randomized sequence of 200 operations).
- **AC-20** [IT] Adjustment out / purchase void cannot reduce `available` below 0; `reason_code` and note are mandatory for adjustments.
- **AC-21** [IT] As `rokh_app`: `DELETE` on financial/inventory/audit tables fails; `UPDATE` of movements/audit rows fails; `ALTER TABLE … DISABLE TRIGGER` fails; updating order snapshot columns fails.
- **AC-22** [IT] UNTRACKED products never reserve stock and get `cogs_rial = 0` on completion.

## Payments
- **AC-23** [IT] Split payment (cash + card) reaching the total yields `PAID`; overpayment → `409 PAYMENT_STATE_CONFLICT` and DB CHECK also rejects it.
- **AC-24** [IT] Payment on a PENDING or CANCELLED order → `409 PAYMENT_STATE_CONFLICT`.
- **AC-25** [IT] Refund (ADMIN) of a COMPLETED, PAID order → one REFUND per PAYMENT with same method/amount, status `REFUNDED`; OPERATOR → `403`; partially paid completed order → `409`.
- **AC-26** [IT] Voiding a payment recomputes `paid_rial` and `payment_status`; the voided row remains with reason and actor; voiding a REFUND row is impossible.
- **AC-27** [IT] Same payment `Idempotency-Key` twice → one payment row.

## Reports
- **AC-28** [IT] For a seeded fixture (orders, refunds, prepayment refunds, purchases, adjustments, waste, expenses), `/reports/summary` matches hand-computed revenue, COGS, gross profit, inventory losses/gains, expenses, operating result, cash/card collected, and receivables.
- **AC-29** [IT] Purchases are shown but not subtracted from the operating result.
- **AC-30** [IT] Day boundaries follow Asia/Tehran (order completed 23:50 local counts on that day).
- **AC-31** [M] Report screen shows "سود ناخالص" and "نتیجه عملیاتی تخمینی" — never "سود خالص" — and the limitations footer.

## Security
- **AC-32** [IT] Every admin route has a policy entry (test enumerates chi routes).
- **AC-33** [IT] OPERATOR gets `403` on catalog edits, purchases, expenses, reports, users, completed-order refunds, payment voids, kill switch; cost fields are absent from OPERATOR responses.
- **AC-34** [IT] Unsafe admin request without valid CSRF token or with foreign `Origin` → `403 CSRF_FAILED`.
- **AC-35** [IT] Deactivated user's existing session is rejected; password change revokes other sessions.
- **AC-36** [IT] The last active ADMIN cannot be deactivated or demoted.
- **AC-37** [IT] Upload of a non-image or oversize file is rejected; accepted images are re-encoded JPEG.
- **AC-38** [CI] gitleaks passes; no `.env` tracked.

## Push
- **AC-39** [IT] Crossing the threshold creates one event per ADMIN subscription; further sales in the same episode create none; a purchase above threshold re-arms.
- **AC-40** [IT] Fake push server: 410 deletes the subscription; 500 schedules a retry with backoff; 5 failures → FAILED.
- **AC-41** [M] Android Chrome and an installed iOS 16.4+ staff PWA receive the test notification; tapping opens the low-stock list.
- **AC-42** [M] Notification permission is requested only after tapping the enable button.

## UX & quality
- **AC-43** [M] All screens usable at 360 px width; touch targets ≥ 44 px; loading, empty and error states present on every list.
- **AC-44** [CI] Lighthouse (mobile) on `/menu`: Accessibility ≥ 95, Performance ≥ 85.
- **AC-45** [M] Staff app installable from `/admin/` on Android and iOS.

## Operations
- **AC-46** [M] Fresh Windows clone → `docker compose up -d` + documented commands → app running locally in ≤ 15 min.
- **AC-47** [CI] Migrations apply from an empty DB as `rokh_owner`; the app runs as `rokh_app`; `goose status` is clean; sqlc output is up to date (`sqlc diff`).

---

## Regression scenarios from the architecture review

Each accepted finding has at least one scenario. **P0 findings (F1, F2, F3, F4, F9) must pass
before T6/T7 are merged.**

### F1 — order serialization (P0)
- **R-F1-1** [IT] Two concurrent payments that each fit but together exceed the total → exactly one succeeds; `paid_rial ≤ total_rial`.
- **R-F1-2** [IT] Concurrent `cancel` and `record payment` on a PREPARING order (50 runs) → final state is either (CANCELLED, `paid = refunded`) or (PREPARING, payment recorded); never CANCELLED with unrefunded money.
- **R-F1-3** [IT] Concurrent `complete` and `cancel` on a READY order → exactly one wins; stock reflects only the winner (SALE **or** release, never both).
- **R-F1-4** [IT] Concurrent `refund` and `void` on a PAID completed order → one wins; `refunded_rial ∈ {0, paid_rial}`; no REFUND references a voided PAYMENT.
- **R-F1-5** [IT] A cancelled order with a partial prepayment (e.g. 50 % cash) ends with `paid_rial = refunded_rial`, one REFUND mirroring the cash PAYMENT, `payment_status = REFUNDED`.
- **R-F1-6** [IT] Direct SQL as `rokh_app` setting `status = 'CANCELLED'` while `paid_rial > refunded_rial` fails the CHECK.

### F2 — expiry boundaries & acceptance deadline (P0)
- **R-F2-1** [IT] Accept at `expires_at + 1 s` before the job runs → `409 ORDER_EXPIRED`; order is CANCELLED/EXPIRED; reservations released; a second accept → `409 ILLEGAL_TRANSITION`.
- **R-F2-2** [IT] Accept and expiry job racing on an order at its deadline (50 runs) → order is either PREPARING with `accepted_at < expires_at`, or CANCELLED/EXPIRED; reservations released exactly once in the latter case.
- **R-F2-3** [IT] Two expiry workers running concurrently over 30 expired orders → each order cancelled once; `reserved` decreases exactly by their quantities.
- **R-F2-4** [IT] Direct SQL setting `accepted_at ≥ expires_at`, or `cancel_reason = 'EXPIRED'` with `accepted_at` set, is rejected by CHECK.
- **R-F2-5** [IT] Customer cancel of an already-expired order → `200`, no further stock change.

### F3 — idempotent creation under concurrency (P0)
- **R-F3-1** [IT] 10 simultaneous requests with the **same** key and body → one order, nine `200` replays with the same number and token; `reserved` incremented once.
- **R-F3-2** [IT] Final unit: `available = 1`, same-key duplicate sent while the first is in flight → both get the order (`201` + `200`), none gets `INSUFFICIENT_STOCK`.
- **R-F3-3** [IT] Order created at 22:59:59, retried with the same key at 23:00:05 (after closing) → `200` replay, not `ORDERING_CLOSED`.
- **R-F3-4** [IT] Global cap 40 with 39 open: 10 concurrent new orders → exactly 1 succeeds, 9 `ORDERING_BUSY`; open PENDING never exceeds 40. Same for the per-client cap.
- **R-F3-5** [IT] Replay after the original order has advanced to PREPARING returns its current state.
- **R-F3-6** [IT] Daily numbers stay unique and gap-free under 30 concurrent creations.

### F4 — purchase void (P0)
- **R-F4-1** [IT] Purchase 10 @ 1,000 (stock was 0), void immediately → `on_hand = 0`, `value = 0`, one PURCHASE_VOID with `value_delta = −10,000`.
- **R-F4-2** [IT] Purchase A 10 @ 1,000, purchase B 10 @ 3,000, void B → `value = 10,000` exactly (no clamping); avg returns to 1,000.
- **R-F4-3** [IT] Purchase, sell 1, then void → `409 PURCHASE_ALREADY_CONSUMED`; balances and historical `cogs_rial` unchanged.
- **R-F4-4** [IT] Voiding a purchase while pending orders have reserved stock so that `available < q` → `409 INSUFFICIENT_STOCK`; nothing changes.
- **R-F4-5** [IT] Second void of the same purchase → state-idempotent `200`, no second movement (unique index).

### F9 — refund, receivable, cancellation, revenue policy (P0)
- **R-F9-1** [IT] Completed unpaid order: revenue = total, receivable = total, cash = 0.
- **R-F9-2** [IT] Completed then paid next day: revenue on completion day; cash on payment day; receivable 0.
- **R-F9-3** [IT] Completed, paid, refunded in a later period: completion period revenue = total; refund period revenue = −total; COGS only in completion period; receivable 0.
- **R-F9-4** [IT] Prepaid order cancelled: revenue 0; cash +X then −X; prepayment refunds = X; receivable 0.
- **R-F9-5** [IT] Identity over a randomized fixture: Σ net revenue (completed orders) = Σ net cash from completed orders + receivables.
- **R-F9-6** [IT] Voided payment on a completed order increases receivables by its amount and is excluded from cash collected.

### F5 — push outbox (P1)
- **R-F5-1** [IT] Worker crash after claim (simulated by not finalizing) → after lease expiry another worker re-claims and sends; event ends SENT; attempts = 2.
- **R-F5-2** [IT] A stale worker finalizing after its lease was re-claimed updates 0 rows.
- **R-F5-3** [IT] No DB transaction is open during the HTTP send (fake server blocks 5 s while a test asserts `pg_stat_activity` shows no idle-in-transaction session from the worker).
- **R-F5-4** [IT] At most 10 claimed per tick and 4 concurrent sends.

### F6 — constraints and privileges (P1)
- **R-F6-1** [IT] Inserting a voided payment/expense/purchase with `void_reason` NULL or blank fails.
- **R-F6-2** [IT] An adjustment with NULL `reason_code` or blank note fails; a SALE with a `reason_code` fails.
- **R-F6-3** [IT] AC-21 privilege checks pass as `rokh_app`; `rokh check` detects a deliberately corrupted `reserved` value in a test DB.

### F7 — cancellation after preparation (P1)
- **R-F7-1** [IT] READY order with tracked qty 3, cancel with waste 2 → `reserved −3`, `on_hand −2`, one WASTE movement at average cost, inventory losses += that value.
- **R-F7-2** [IT] Waste on a PENDING order → `422`; waste > line quantity → `422`.
- **R-F7-3** [IT] UNTRACKED waste sets `wasted_quantity` and `estimated_cost_rial`; appears in estimated waste only.

### F8 — estimated costs (P1)
- **R-F8-1** [IT] UNTRACKED product with estimate 300,000 sold ×2 → `estimated_cost_rial = 600,000`, `cogs_rial = 0`; product report shows estimated margin; summary operating result unchanged by the estimate.
- **R-F8-2** [IT] Changing the estimate later does not change completed lines.

### F10 — explicit corrections (P1)
- **R-F10-1** [IT] No endpoint exists for voiding a REFUND or un-completing an order (route table test).
- **R-F10-2** [IT] `RETURN_TO_STOCK` adjustment after a refund appears as an inventory gain and the order's COGS is unchanged.

### F11 — security hardening (P1)
- **R-F11-1** [IT] Push subscription with endpoint `https://127.0.0.1/…`, `https://169.254.169.254/…`, `http://fcm.googleapis.com/…`, or a non-allowlisted host → `422`; a host that resolves to a private IP is refused by the dialer.
- **R-F11-2** [IT] DB contains no plaintext tracking token; replay re-derives the same token; access with a 25 h old token → `404`.
- **R-F11-3** [IT] Two admins (the only two) concurrently deactivate each other → exactly one succeeds.
- **R-F11-4** [IT] Shared café Wi-Fi: 15 open orders from one IP with 15 distinct client ids succeed; the 16th open order from that IP → `429 TOO_MANY_OPEN_ORDERS` until one is accepted; a 7th order within 10 min from one client id → `429 RATE_LIMITED`.

### F12 — staff PWA (P1)
- **R-F12-1** [CI] Built `index.html` (customer) contains no `<link rel="manifest">` and registers no service worker; `admin/index.html` does; SW scope is `/admin/`.
- **R-F12-2** [M] Android Chrome on `/menu` shows no install prompt; on `/admin/` the staff app is installable.

### F13 — price confirmation (P1)
- **R-F13-1** [IT] Price changed after the menu was loaded → `409 PRICE_CHANGED` with current prices; nothing reserved; resubmission with updated expected prices succeeds.
- **R-F13-2** [IT] Concurrent price edit and order creation → the order uses either the old price with old expectation or fails with `PRICE_CHANGED`; never charges a price the client did not send.

### F14 — hosting & PostgreSQL (P1)
- **R-F14-1** [CI] Test matrix runs migrations and integration tests on PostgreSQL 16 and 17.
- **R-F14-2** [M] T0 feasibility report exists and is approved before T13.
