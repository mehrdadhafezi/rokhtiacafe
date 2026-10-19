# CLAUDE.md

Guidance for Claude Code working in this repository.

## Project

Rokh Tia Café — small café management + QR ordering PWA (Persian, RTL, mobile-first).
Modular monolith: one Go binary (API + embedded SPA + jobs) and one PostgreSQL database.

| Path | Contents |
|------|----------|
| `backend/` | Go 1.27, chi, pgx v5, sqlc, goose |
| `frontend/` | React + TS + Vite, Tailwind v4, shadcn/ui, TanStack Query, RHF + Zod, vite-plugin-pwa |
| `deploy/` | Deployment docs/config — manual only |
| `docs/` | Source of truth for design — read before changing behavior |

Key docs: [PRD](docs/PRD.md) · [ARCHITECTURE](docs/ARCHITECTURE.md) · [DATABASE](docs/DATABASE.md) ·
[API-CONTRACT](docs/API-CONTRACT.md) · [WORKFLOWS](docs/WORKFLOWS.md) · [SECURITY](docs/SECURITY.md) ·
[ACCEPTANCE](docs/ACCEPTANCE.md) · [IMPLEMENTATION-PLAN](docs/IMPLEMENTATION-PLAN.md) ·
[REVIEW-RESPONSE](docs/REVIEW-RESPONSE.md)

## Hard rules

- The GitHub repo is **PUBLIC**. Never commit secrets, `.env*` (except `.env.example`),
  keys, certificates, DB dumps, or backups. Review `git diff --cached --stat` before every commit.
- **Never touch production.** The live server (rokh.tiacafe.ir, 62.204.61.18) is off-limits:
  no SSH, no deploys, no remote commands unless the user explicitly asks in chat.
- No automatic production deployment in CI. Workflows may lint, build and test only.
- No force-push, no history rewrites on `main`.

## Domain invariants (do not break)

- Money is `bigint` **rials** (`*_rial`); UI shows tomans. Never floats.
- Server computes all prices; order lines snapshot price at creation, COGS at completion.
- Stock: `0 ≤ reserved ≤ on_hand`; changes via conditional UPDATEs, rows locked in ascending `product_id`.
- Every `on_hand` change writes an `inventory_movements` row in the same tx.
- **Every order mutation (transition, expiry, payment, refund, void, cancel) starts with
  `SELECT … FROM orders WHERE id = $1 FOR UPDATE`.** Follow the lock order in DATABASE §3.1.
- Order creation runs under the global `LOCK_ORDER_CREATE` advisory lock; idempotent replay is
  checked first, before hours/caps/prices/stock.
- No money on PENDING or CANCELLED orders; refunds are full; `payment_status` is a generated column.
- Purchase voids are exact or rejected — never clamp values.
- Only the corrections listed in WORKFLOWS §6 exist; no generic reversal.
- Financial/inventory/audit rows are never deleted — void with reason. Audit every admin mutation.
- The server runs as `rokh_app` (DML only); migrations run as `rokh_owner`.
- Never make outbound requests to push endpoints outside the allowlist; never put tracking tokens in URLs.
- Authorization goes through `auth.Can(role, action)` only.
- Gross profit ≠ net profit; purchases are not expenses (they become COGS).

Full list: [DATABASE.md §3](docs/DATABASE.md#3-invariants).

## Git workflow

- `main`: reviewed, validated code only.
- `feature/<short-name>`: all implementation work; merge via Pull Request.
- Descriptive, imperative commit messages. Financial/inventory changes need tests.

## Local tooling (Windows)

Go 1.27.2 at `D:\Apps\bin\go.exe` (GOROOT `D:\Apps`) — if `go` is not found, put `D:\Apps\bin`
on PATH; do not touch Windows Installer settings. Node 24 / npm 11, Docker + Compose, gh CLI.
PostgreSQL: dev/CI 17, minimum 16.

## Commands

- `./scripts/dev.ps1 setup | run | check | test | migrate | status | sqlc | smoke` (see README).
- Integration tests need the local DB (`./scripts/dev.ps1 db-up`); each test gets its own
  throwaway database via `internal/testdb`. `REQUIRE_DB_TESTS=1` makes them fail instead of skip.
- sqlc runs from the pinned Docker image `sqlc/sqlc:1.29.0`; commit generated `internal/store`.
- New migrations: `backend/db/migrations/NNNNN_name.sql` with `-- +goose Up` (forward-only).
  They run as `rokh_owner`; grant DELETE explicitly only where DATABASE.md allows it.
- First admin: `echo <password> | rokh user create -username NAME -display-name NAME -role ADMIN` (password via stdin only).
- Admin routes must be registered through `guarded.handle(method, pattern, action, h)` in `internal/api` so the
  route-coverage test sees their policy action; never call `auth.Can` ad hoc in handlers.
- `rokh check` verifies the app-enforced invariants (INV-3/4/7b/8/11/16); run it after data changes in tests.
- Trigger SQLSTATEs: `RK001` delete forbidden, `RK002` immutable/append-only, `RK003` illegal order transition.
