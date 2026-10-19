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
[ACCEPTANCE](docs/ACCEPTANCE.md) · [IMPLEMENTATION-PLAN](docs/IMPLEMENTATION-PLAN.md)

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
- Status transitions use `UPDATE … WHERE status = <expected>`; side effects only if a row changed.
- Financial/inventory/audit rows are never deleted — void with reason. Audit every admin mutation.
- Authorization goes through `auth.Can(role, action)` only.
- Gross profit ≠ net profit; purchases are not expenses (they become COGS).

Full list: [DATABASE.md §3](docs/DATABASE.md#3-invariants).

## Git workflow

- `main`: reviewed, validated code only.
- `feature/<short-name>`: all implementation work; merge via Pull Request.
- Descriptive, imperative commit messages. Financial/inventory changes need tests.

## Local tooling (Windows)

Go 1.22.3 installed — **upgrade to 1.27.x required** before backend work.
Node 24 / npm 11, Docker + Compose, gh CLI.
Commands will be documented here as T1/T9 add them.
