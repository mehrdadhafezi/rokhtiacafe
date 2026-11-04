# C6 — Staging release and rollback rehearsal (Rokh)

**Status:** DEVELOPMENT/STAGING-EQUIVALENT ACCEPTANCE AUTOMATED AND EXECUTED on disposable PostgreSQL 16 and 17 with a **synthetic** pre-00008 dataset (see §7). No sanitized production backup was used, so restoration of real production data is NOT proven. Production remains NO-GO.
**Base:** development merge `ec40c4ea80b455843361e6b18924de066fbff09b` (PR #19 → `feature/customer-accounts-history-reporting`).
**Production release:** NO-GO until this rehearsal, R1 client-IP trust protection, privacy/retention and account-recovery decisions are separately accepted.
**Hard restriction:** No live server services, shared LiteSpeed/WHM, CDN/DNS/firewall, production PostgreSQL or live uploads are to be modified by C6. Owner approval is required before any live-sourced backup is transferred or staging network service created.

## 1. Environment isolation and evidence

Use a dedicated local/dev machine or disposable VM/container network. The original live database must NEVER be used as a test target. Do not run `restore.sh` or `migrate.sh` using production `/etc/rokh/*.env`. Require explicitly separate `STAGING_DATABASE_URL`, PostgreSQL 16 disposable cluster, isolated `UPLOAD_DIR`, distinct `APP_SECRET_KEY`, separate port and blank staff/user test identities. Only sanitized, access-controlled copies of real backups may be used; when not authorized or not available, use a synthetic fixture backup and clearly mark coverage limitation.

Record:
- current `main` SHA, `feature/customer-accounts-history-reporting` SHA and C6 branch SHA;
- provenance SHA256 of sanitized backup, uploads archive and release binary;
- DB target identifier and `SELECT current_database(), inet_server_addr(), inet_server_port()` to confirm isolated target;
- migration status before and after, `rokh check` output; schema counts and totals (no customer PII in logs);
- test start/end and all return codes, evidence links, CI checks and any deviations.

Failure to prove isolation means **STOP**, not a best-effort execution.

## 2. Backup and forward-only migration

1. Snapshot or assemble a **disposable** pre-00008 database (migrations 00001–00007, representative legacy guest order, staff sessions, inventory, payment and audit rows). Capture baseline invariants: all table counts, sum of financial amounts, pending inventory, completed and cancelled orders, and audit events.
2. Generate a checksum-verified custom-format pg_dump and uploads archive using the repository backup method. Verify `SHA256SUMS` and `pg_restore --list`; do not restore straight into the running production database.
3. Create a **new empty** staging database owned by the migration role. Use `deploy/scripts/restore-target.sql` and `deploy/scripts/restore.sh` only after inspecting the configured DB identity and verifying it differs from the original. Examine `deploy/README.md §9` before running.
4. Apply migration `00008_customer_accounts.sql` to the restored snapshot using the appropriate version of the test binary. Verify `rokh migrate status` has zero pending and `rokh check` passes. Count all legacy orders and financial records before/after; assert exact unchanged values and inventory invariants.
5. Validate new tables `customers`, `customer_sessions`, `customer_events`, `customer_order_links` and constraints. Confirm old guest orders have no attribution; no backfill by label or phone.
6. Verify PostgreSQL 16 as release target. PostgreSQL 17 compatibility remains CI evidence and may also be exercised separately.

## 3. Staging end-to-end flows (database + HTTP + browser)

A. Guest: browse menu, add products, create order and track with legacy token; retries use unchanged idempotency hash; staff accept, record payment, ready, complete and cancel pathways remain correct.

B. Two customers: register/login with separate identities; account A creates order, receives tracking, sees history and item snapshots; account B receives uniform 404 for A's order, cannot read A's activity, cannot reuse A's idempotency key to claim it; verify that customer ID in JSON is ignored/rejected; stock is reserved exactly once and link/event are created exactly once. Test logout and password change invalidating sessions, CSRF and staff/customer isolation.

C. Finance: capture complete, unpaid, partial payment, fully refunded, cancelled with prepaid refund, voided payment, and guest completed order. Reconcile per-customer metrics against the global summary **minus unattributed guest activity**. Test old open orders with a reporting window excluding their creation date. Inspect real XLSX and CSV in Excel/LibreOffice or an independent parser; verify RTL, integer rial values, BOM and escaped formula-like names.

D. **Cross-day refund regression (release gate):** construct a sale completed on Tehran business day D, refund entered on D+1, and query single-day reports for D, D+1 and combined range. Assert sale is recognized on D, cash refund only on D+1, `revenue_rial` matches the existing report's established time-bucket semantics, and net collected is separately time-bucketed. Do not bypass DB immutability or rewrite live payment timestamps; use an isolated controllable-clock test mechanism or explicitly document that staging must run across the boundary and mark gate BLOCKED if untested.

E. Browser: test mobile viewport, Persian numbers and Jalali dates, loading and network error of `/customer/auth/me` (identity must not become guest), retry success and checkout, admin-only customer list, customer detail, 366-day bounded report and real XLSX/CSV downloads; staff OPERATOR must get 403 for customer PII. Prefer automated Playwright in an isolated browser test workflow; manual E2E alone must be recorded as a coverage gap.

F. Frontend PWA: ensure staff SW remains scoped to `/admin`, customer pages never receive staff cache, authentication/profile and reports use `Cache-Control: no-store`.

## 4. Binary rollback rehearsal (forward-only)

- Rehearse new binary startup on staged migrated DB and check readiness and all key flows.
- Switch **staging only** to the previous verified binary while leaving migration 00008 and customer tables in place. Verify guest order creation/track, staff admin operations, stock and payments still work. Existing customer account endpoints should return 404 because the old binary does not implement them; this is expected and must be documented as a temporary feature rollback.
- Switch **staging only** back to the new binary and confirm customer login/history/order ownership and customer/admin reports still work with preserved data. Verify total rows and sums before and after each version transition.
- **Do not issue a down-migration**. Preserve data; no blanket truncate or destructive rollback. Confirm old sessions are not accidentally reinterpreted as staff sessions.
- Define stop conditions for rollback rehearsal: unexpected DB mismatch, negative inventory, cross-account data access, report mismatch, or customer API cached by CDN/proxy.

## 5. Security and release blockers

- R1 client-IP / forwarded-header anti-spoof issue is **unresolved** on production. Do not bypass it, do not enable public signups, and do not switch `TRUSTED_PROXIES` until validated.
- Document a phone-ownership / OTP verification and account recovery policy. Users must not be described as phone-verified until a proven OTP flow exists.
- Approve minimum retention periods, privacy notice and deletion/anonymization constraints for customer PII, sessions and append-only events, in compliance with applicable law.
- Confirm backup encryption/offsite access and a tested restore. Staging may not claim production readiness by testing only a synthetic DB.
- Automated browser E2E and a time-controlled cross-day refund test remain **open coverage gaps** from prior independent audit.

## 6. Decision table and artifact contract

For each item, record `PASS`, `FAIL`, `BLOCKED`, or `NOT RUN`, with exact command/environment, evidence, actual versus expected, and reproducible next step:
1. isolated staging DB + backup integrity;
2. forward migration + legacy invariants;
3. guest and customer ordering, idempotency, CSRF and private history;
4. admin PII role controls;
5. ledger reconciliation including mixed guest;
6. D/D+1 refund boundary test;
7. real XLSX/CSV and browser E2E;
8. staging rollback / roll-forward;
9. protected headers, caching and R1 trust verification;
10. retention/OTP/recovery signoff.

Only report **C6 PASS** when 1–8 genuinely pass AND release blockers 9–10 have an approved resolution. A green GitHub workflow does NOT establish R1 resolution or production go/no-go.

## Developer assignment — next implementation work

- Add automated cross-day refund test with a controllable isolated test clock, without weakening immutable production payment/audit rules.
- Add automated browser acceptance tests for customer login/account/checkout and ADMIN reports.
- Extend disposable deploy rehearsal to verify the binary-only rollback against schema 00008 and retained customer data, including the older binary's compatibility. Do not run it against production.
- Make evidence collection repeatable, security-safe and CI-visible.
- Run and validate all workflows on the C6 branch; open a draft PR targeting `feature/customer-accounts-history-reporting` with actual evidence, not a claim of completed C6.

## 7. Execution (PR #20)

Everything below runs only against throwaway clusters (loopback/private addresses, random
passwords, removed afterwards). Nothing touches a live server, database, upload directory, proxy,
DNS or CDN.

| Gate | How it runs | Where |
|---|---|---|
| 1–2, 8 Backup, restore, forward migration, binary rollback and roll-forward | `deploy/test/c6/in-docker.sh NEW OLD TOOL [EVIDENCE]`. The previous binary (`origin/main`) seeds legacy data. `backup.sh` takes the backup; `restore.sh` restores it with the C1–C5 binary, which applies `00008` and runs `rokh check`. The C1–C5, previous and C1–C5 binaries then run in turn against the migrated database. Row-digest snapshots S0–S5 and `rokh check` with both binaries are taken at every transition. | CI `c6-acceptance / rehearsal` (PG16, PG17); evidence artifact `c6-rehearsal-pg*` |
| 3–5 Guest and customer ordering, idempotency (including the cross-binary replay), CSRF, isolation, ADMIN-only customer data, reconciliation with guest activity | HTTP phases of `backend/tools/c6`, plus the Go integration suite | the same jobs; `backend / test` |
| 6 D/D+1 refund boundary (Asia/Tehran) | `deploy/test/clock/run.sh`: PostgreSQL with libfaketime and the server clocks moved together. `TestCrossDayRefundTehran` checks the summary and customer reports for D, D+1 and D..D+1. No row is rewritten and no trigger is disabled. | CI `backend / clock` (PG16, PG17) |
| 7 Browser E2E and real XLSX/CSV | `scripts/e2e.sh` runs Playwright (`frontend/e2e`) on desktop and Pixel 7 against a release build and a new throwaway database | CI `c6-acceptance / e2e` |
| 9 R1, CDN/proxy caching | NOT automatable here. The app sends `no-store` on customer/admin data (tested); the live CDN and proxy must be verified by the owner. R1 is still **BLOCKED**. | owner |
| 10 Retention, OTP and account recovery | NOT implemented; owner decision required | owner |

Run locally (Docker, Go, Node, Git Bash on Windows):

```bash
scripts/build-release.sh local                       # C1–C5 build → release/
git worktree add --detach ../rokh-baseline origin/main && (cd ../rokh-baseline && scripts/build-release.sh baseline)
(cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../release/c6-linux-amd64 ./tools/c6)
deploy/test/c6/in-docker.sh release ../rokh-baseline/release release/c6-linux-amd64 /tmp/c6-evidence
deploy/test/clock/run.sh                              # PG_MAJOR=17 for PostgreSQL 17
TEST_DATABASE_ADMIN_URL=… MIGRATION_DATABASE_URL=… DATABASE_URL=… scripts/e2e.sh   # throwaway cluster prepared with roles.sql
```

Known limitations: the dataset is synthetic. The previous binary is `origin/main`, not a hash-verified
copy of the binary actually deployed. The rehearsal runs inside a container rather than on a
staging VM with LiteSpeed/CDN in front; the network edge (R1, caching) is outside its scope.

