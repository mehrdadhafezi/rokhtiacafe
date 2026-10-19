# Implementation Plan — Rokh Tia Café MVP

Each task = one `feature/*` branch → PR into `main` with green CI. Sizes are rough
(S ≈ ½ day, M ≈ 1 day, L ≈ 2 days of focused work).

## 1. Prerequisites (before T1)
- **P1** Upgrade local Go from **1.22.3** to **1.27.x** (approved stack). `go.mod` will declare `go 1.27`.
- **P2** Install `sqlc` and `goose` CLIs (or run via `go run` pinned versions in `backend/tools.go`-style Makefile targets).
- **P3** Architecture review by Codex Astra; decisions in §4 confirmed by owner.

## 2. Tasks

| # | Branch | Size | Scope | Done when |
|---|--------|------|-------|-----------|
| T1 | `feature/backend-foundation` | M | Go module, config, chi server, slog, request id, recover, security headers, `/healthz` `/readyz`, `docker-compose.yml` (Postgres 17, localhost only), goose wiring, `rokh migrate`, sqlc config, Makefile/`scripts/*.ps1`, CI job `backend` (gofmt, vet, test, migrations on service Postgres, `sqlc diff`), gitleaks job | CI green; `rokh serve` responds on `/readyz` |
| T2 | `feature/schema` | M | All migrations from DATABASE.md incl. triggers; constraint tests (AC-21) | AC-21, AC-47 |
| T3 | `feature/auth` | L | argon2id, sessions, CSRF, policy table + route-coverage test, rate limiter, `rokh user create`, audit helper, users API | AC-32–36 |
| T4 | `feature/catalog` | M | Categories/products API, availability toggle, image upload pipeline, public menu | AC-01 (API), AC-37 |
| T5 | `feature/inventory` | L | Balances, purchases (+void), adjustments, WAC, movements API, reconciliation command | AC-18–20, AC-22 |
| T6 | `feature/orders` | L | Public create (idempotent, reservations, numbering), tracking, customer cancel, admin transitions, expiry job, ordering hours/cap | AC-02–08, 10–17 |
| T7 | `feature/payments` | M | Payments, refunds, voids, status projection | AC-23–27 |
| T8 | `feature/reports-expenses` | M | Expenses API, summary + product reports | AC-28–30 |
| T9 | `feature/frontend-foundation` | M | Vite/React/TS, Tailwind v4, shadcn init, tokens, Vazirmatn, RTL, router, API client (CSRF, error→Persian map), money/date utils, PWA plugin, CI job `frontend` (typecheck, lint, build) | CI green, empty shells render RTL |
| T10 | `feature/customer-ui` | L | Menu, cart, checkout, tracking, cancel, error/empty/loading states | AC-01, 09, 43, 44 |
| T11 | `feature/admin-ui` | L | Login, dashboard, orders board, payments, catalog, inventory, purchases, expenses, reports (Jalali months), users | AC-31, 43, 45 |
| T12 | `feature/web-push` | M | VAPID keygen command, subscriptions API, outbox worker, low-stock detection, SW handlers, enable/test UI | AC-39–42 |
| T13 | `feature/release-build` | S | Embed SPA in binary, Linux build script, `deploy/` docs (backup, restore, rollback, env template) — **no deployment** | Single binary serves app locally |
| T14 | *(no branch)* | S | Read-only production inspection (§3) and deployment proposal for owner approval | Report approved |

Parallelism: T9 can start right after T1. T10 needs T4+T6 APIs; T11 grows as T3–T8 land.
T12 after T5. Critical path: T1 → T2 → T3 → T5 → T6 → T7 → T8 → T11.

### Testing approach
- Integration tests use a real PostgreSQL (Docker locally, service container in CI); each test runs in its own schema/database created from migrations.
- Concurrency tests (AC-05, AC-14) use goroutines + `sync.WaitGroup` against the real pool.
- WAC and payment-status logic also covered by table-driven unit tests.
- Frontend: typecheck + lint + build in CI; component tests only for cart math and money formatting.

## 3. Production inspection checklist (read-only, T14)
Requires owner-provided access; nothing is changed on the server.
1. DNS: `rokh.tiacafe.ir` A/AAAA → 62.204.61.18; TLS certificate (AutoSSL) covers the subdomain.
2. cPanel account, subdomain document root, available features (Terminal/SSH, Cron, "Setup Application").
3. Can a user-level process run persistently (CloudLinux LVE limits on memory/processes, whether long-running processes get killed)?
4. Is a reverse proxy to `127.0.0.1:<port>` permitted (LiteSpeed/Apache `mod_proxy` via `.htaccess` `[P]`, or host-provided app proxy)?
5. PostgreSQL available in cPanel? Version ≥ 14? Can the app role own its schema? Is `pg_dump` available?
6. Linux amd64 static binaries executable from the home directory (no `noexec` on `/home`)?
7. Disk quota, backup facilities, outbound HTTPS allowed (needed for Web Push).
8. Existing services on the host that must not be disturbed.

## 4. Critical decisions

Each has one recommendation. ✅ = assumed in the docs; confirm or override during review.

| ID | Decision | Recommendation | Why |
|----|----------|----------------|-----|
| **D1** | Where production runs if cPanel cannot host a persistent Go process + reverse proxy + PostgreSQL ≥ 14 | Run the read-only inspection first; **if any of those fail, use a small separate VPS** (1 vCPU / 1–2 GB) for `rokh.tiacafe.ir` only, via a DNS A record change — the existing server stays untouched | Shared hosting often kills long-running processes and offers old/no PostgreSQL; the single-binary design works on either target |
| **D2** ✅ | Costing of made-to-order drinks | `UNTRACKED` products with COGS 0; ingredients as expenses | Avoids BOM; no double counting; operating result stays correct |
| **D3** ✅ | Session storage | Add a 14th table `sessions` | Revocation, idle timeout, per-session CSRF; signed stateless cookies cannot be revoked |
| **D4** ✅ | Pending-order expiry | 20 min, PENDING only, configurable | Frees stock from abandoned orders without surprising customers already being served |
| **D5** ✅ | Revenue recognition | On completion; payments tracked independently; refunds reduce revenue on refund date | Matches COGS timing; payment state independent as required |
| **D6** ✅ | Cancelling paid orders | Forbidden until refunded (net paid = 0); refunds & voids ADMIN-only | Prevents cash disappearing through cancellations |
| **D7** ✅ | Split payments / partial refunds | Split tender allowed; refunds full only | Covers cash+card case; partial refunds rare in a café |
| **D8** ✅ | Ordering hours & closing | `ORDERING_HOURS` env (e.g. `08:00-23:00`) + global pending cap; admin toggle deferred | No settings table needed; protects stock overnight |
| **D9** ✅ | Live updates | Polling every 5 s (customer tracking, admin board) | No SSE/WebSocket through LiteSpeed/proxy uncertainty; trivial load |
| **D10** ✅ | Calendar | Jalali in UI via `Intl`; API uses ISO dates; months computed client-side | Zero backend calendar dependency |
| **D11** ✅ | Who receives push | ADMIN only in MVP | Matches "authorized managers"; operators see dashboard |
| **D12** ✅ | Tracking token storage | Plaintext random 128-bit token | Enables idempotent replay; low sensitivity (SECURITY §10) |
| **D13** ✅ | Image storage | Local disk outside web root, re-encoded JPEG, included in backups | No object storage needed |
| **D14** ✅ | PostgreSQL version | Develop on 17; support ≥ 14 (avoid newer-only features such as `MERGE … RETURNING`) | Hosting may offer older versions |
| **D15** | Go 1.27 on the dev machine | Upgrade before T1 (local is 1.22.3) | Approved stack |
| **D16** ✅ | Customer price display | Toman with Persian digits («۹۵٬۰۰۰ تومان»); admin enters tomans, API stores rials (×10) | Local convention; avoids ×10 mistakes in API |

## 5. Ready for implementation
- Schema, invariants, API contract, workflows, permission matrix, acceptance criteria: **ready**, pending review.
- T1–T13 can proceed locally without any production access.
- **Blocked:** production deployment (D1 + T14 need owner-provided read-only access).
