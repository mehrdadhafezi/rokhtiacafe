# Implementation Plan — Rokh Tia Café MVP

Revised after the architecture review (GO_WITH_CHANGES, findings F1–F14; see
[REVIEW-RESPONSE.md](REVIEW-RESPONSE.md)). Each task = one `feature/*` branch → PR into `main`
with green CI. Sizes are rough (S ≈ ½ day, M ≈ 1 day, L ≈ 2 days of focused work).

## 1. Prerequisites
- **P1** ✅ Go **1.27.2** installed at `D:\Apps\bin\go.exe` (GOROOT `D:\Apps`). Make sure `D:\Apps\bin`
  is on `PATH` in new shells and the old `C:\Program Files\Go\bin` entry no longer shadows it.
  `go.mod` will declare `go 1.27`. (Windows Installer repair is out of scope for this project.)
- **P2** Install `sqlc` and `goose` CLIs (pinned versions, invoked via Makefile/PowerShell scripts).
- **P3** Architecture corrections reviewed; owner decisions in §5 answered or defaults accepted.

## 2. Tasks (revised order)

| # | Branch | Size | Scope | Done when |
|---|--------|------|-------|-----------|
| **T0** | *(no branch; report in `docs/HOSTING-ASSESSMENT.md` via PR)* | S | **Read-only hosting feasibility assessment** (§3). Moved first (F14) | Report reviewed; D1 decided |
| T1 | `feature/backend-foundation` | M | Go module, config, chi server, slog, request id, recover, security headers, `/healthz` `/readyz`, `docker-compose.yml` (Postgres 17, localhost only, creates `rokh_owner` + `rokh_app`), goose wiring (`rokh migrate` as owner), sqlc config, scripts, CI: gofmt, vet, test, migrations on **PG 16 and 17**, `sqlc diff`, gitleaks | CI green; `/readyz` OK as `rokh_app` |
| T2 | `feature/schema` | M | All migrations from DATABASE.md incl. triggers and grants; constraint/privilege tests; `rokh check` | AC-21, AC-47, R-F2-4, R-F6-* |
| T3 | `feature/auth` | L | argon2id, sessions, CSRF, policy table + route-coverage test, rate limiter, `rokh user create`, audit helper, users API with membership lock | AC-32–36, R-F11-3 |
| T4 | `feature/catalog` | M | Categories/products (incl. estimated cost), availability toggle, image pipeline, public menu | AC-01 (API), AC-37 |
| T5 | `feature/inventory` | L | Balances, purchases + exact void, adjustments with reason codes, WAC, movements API | AC-18–20, 22, R-F4-*, R-F10-2 |
| T6 | `feature/orders` | L | Serialized creation (lock, replay-first, caps, price check), HMAC tracking tokens, tracking/cancel, transitions with order lock, deadline-enforced accept, per-order expiry job, cancel with waste | AC-02–17, R-F1-3, R-F2-*, R-F3-*, R-F7-*, R-F11-2/4, R-F13-* |
| T7 | `feature/payments` | M | Payments, completed-order refunds, cancel prepayment refunds, voids, amounts on orders | AC-23–27, R-F1-1/2/4/5/6 |
| T8 | `feature/reports-expenses` | M | Expenses API, summary, product (incl. estimated margin) and receivables reports | AC-28–30, R-F8-*, R-F9-* |
| T9 | `feature/frontend-foundation` | M | Vite multi-page (customer + staff entries), React/TS, Tailwind v4, shadcn, tokens, Vazirmatn, RTL, router, API client, money/date utils, staff-only PWA setup, CI: typecheck, lint, build, manifest check | CI green; R-F12-1 |
| T10 | `feature/customer-ui` | L | Menu, cart, price-change confirmation, checkout, tracking via fragment token, cancel, states | AC-01, 09, 43, 44, R-F12-2 |
| T11 | `feature/admin-ui` | L | Login, dashboard, kill switch, orders board (waste on cancel), payments, catalog, inventory, purchases, expenses, reports (Jalali months, estimates labelled), users | AC-31, 43, 45 |
| T12 | `feature/web-push` | M | VAPID keygen, subscriptions API with allowlist + safe dialer, leased outbox worker, low-stock detection, SW handlers, enable/test UI | AC-39–42, R-F5-*, R-F11-1 |
| T13 | `feature/release-build` | S | Embed both entries in binary, Linux build, `deploy/` docs (roles setup, backup, restore, rollback, env templates) for the target chosen in D1 — **no deployment** | Single binary serves app locally |

Parallelism: T9 can start right after T1. T10 needs T4+T6 APIs; T11 grows as T3–T8 land.
T12 after T5 (Web Push stays in MVP scope as its own milestone; it may ship after the
core ordering release only with owner approval — O10).
Critical path: T0 → T1 → T2 → T3 → T5 → T6 → T7 → T8 → T11.
P0 regression scenarios (R-F1, R-F2, R-F3, R-F4, R-F9) are merge blockers for T5–T8.

### Testing approach
- Integration tests use real PostgreSQL (Docker locally; service containers for 16 and 17 in CI); each test package gets a fresh database migrated as `rokh_owner` and exercised as `rokh_app`.
- Concurrency tests use goroutines released by a barrier, ≥ 50 iterations, and assert final DB state plus `rokh check`.
- WAC, payment-amount and revenue-policy logic also covered by table-driven unit tests.
- Frontend: typecheck + lint + build in CI; unit tests for cart math, money formatting, and price-change reconciliation.

## 3. Hosting feasibility assessment (T0)
Read-only; requires owner-provided access; **nothing is changed on the server**. Output: a
report with a go/no-go per item and a recommendation for D1.
1. DNS: `rokh.tiacafe.ir` A/AAAA → 62.204.61.18; TLS certificate (AutoSSL) covers the subdomain.
2. cPanel account, subdomain document root, features (Terminal/SSH, Cron, "Setup Application").
3. Can a user-level process run persistently (CloudLinux LVE memory/process limits; are long-running processes killed)? Is there a supported way to restart it on boot/crash?
4. Is a reverse proxy to `127.0.0.1:<port>` permitted (LiteSpeed/Apache `mod_proxy` via `.htaccess` `[P]`, or host-provided app proxy)? Does it pass `X-Forwarded-For`?
5. PostgreSQL available? **Version ≥ 16?** Can two roles exist (owner + runtime) with the grants in DATABASE §6.1? Is `pg_dump` available?
6. Linux amd64 static binaries executable from the home directory (no `noexec`)?
7. Disk quota, backup facilities, outbound HTTPS to push services allowed.
8. Existing services on the host that must not be disturbed.

## 4. Decisions

✅ = adopted in the docs. *(Fn)* = changed by the review.

| ID | Decision | Adopted |
|----|----------|---------|
| D1 | Production host | Decide after T0; if cPanel fails any of §3 items 3–6, use a small separate VPS for `rokh.tiacafe.ir` only (existing server untouched) |
| D2 ✅ *(F8)* | Made-to-order drinks | `UNTRACKED`, actual COGS 0, ingredients as expenses; optional estimated unit cost for a separate margin view only |
| D3 ✅ | Session storage | 14th table `sessions` |
| D4 ✅ *(F2)* | Pending orders | 20-min acceptance deadline, enforced on accept and by a per-order expiry job |
| D5 ✅ *(F9)* | Revenue recognition | At completion; refunds of completed orders reduce revenue on refund date; receivables for unpaid completed orders |
| D6 ✅ *(F1, F9)* | Cancelling paid orders | Allowed; cancel auto-refunds prepayments (OPERATOR allowed); completed-order refunds and voids ADMIN-only |
| D7 ✅ | Split payments / partial refunds | Split tender allowed; refunds always full |
| D8 ✅ *(F11)* | Ordering hours | `ORDERING_HOURS` + global and per-client caps + in-memory ADMIN kill switch |
| D9 ✅ | Live updates | Polling every 5 s |
| D10 ✅ | Calendar | Jalali in UI via `Intl`; API uses ISO dates |
| D11 ✅ | Push recipients | ADMIN only |
| D12 ✅ *(F11)* | Tracking token | HMAC-derived, stored hashed, header transport, 24 h lifetime |
| D13 ✅ | Image storage | Local disk outside web root, re-encoded JPEG, in backups |
| D14 ✅ *(F14)* | PostgreSQL | ≥ 16 (14 EOL Nov 2026); dev/CI on 17, CI also on 16 |
| D15 ✅ | Go | 1.27.2 installed |
| D16 ✅ | Price display | Toman with Persian digits; API stores rials |
| D17 ✅ *(F1)* | Concurrency model | Order-row lock for all order mutations; global advisory lock for creation |
| D18 ✅ *(F13)* | Price changes at checkout | Client sends expected prices; mismatch → confirm and resubmit |
| D19 ✅ *(F12)* | PWA | Staff-only PWA scoped to `/admin/`; customer menu is plain web |
| D20 ✅ *(F6)* | DB privileges | `rokh_owner` for migrations, `rokh_app` runtime with DML-only grants |
| D21 ✅ *(F9)* | Payments timing | Only after acceptance (no money on PENDING orders) |

## 5. Unresolved owner decisions

Each has a recommended default that the docs already assume; implementation can proceed unless
the owner overrides.

| ID | Question | Recommended default |
|----|----------|---------------------|
| O1 | Production target (D1) | Decide after T0; VPS fallback |
| O2 | If the host offers only one PostgreSQL role, accept a single role (triggers then bypassable by the app role)? | **No** — choose the VPS route instead |
| O3 | Unpaid completed orders that are never paid: add a write-off action? | Defer; show receivables list with age |
| O4 | Kill switch persistence across restarts | In-memory is acceptable (restart is rare and staff-initiated) |
| O5 | Who maintains estimated unit costs for drinks | Owner, per product, optional; reviewed monthly |
| O6 | Abuse-limit values (per IP 30/10 min, per device 6/10 min, per network 15 open, global 40) | Accept defaults; tune after the first week |
| O7 | Refunds recorded by mistake | Final in MVP; explain in audit note |
| O8 | Operators may refund prepayments when cancelling | Yes |
| O9 | Payments only after acceptance | Yes |
| O10 | May Web Push ship as a follow-up milestone after core ordering goes live? | Yes, as milestone M2 within MVP scope — not removed |

## 6. Ready for implementation
- Schema, invariants, transaction contracts, API contract, workflows, permission matrix, acceptance and regression criteria: **ready** after this correction.
- T0 should run first; T1–T12 do not depend on production access, so local work may start once the owner confirms (T0 needs read-only server access).
- **Blocked:** production deployment until T0 and D1/O1–O2 are resolved.
