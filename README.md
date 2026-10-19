# Rokh Tia Café

Lightweight café management and QR ordering PWA.

- Production: https://rokh.tiacafe.ir (not deployed from this repo yet)
- Status: architecture & design docs under review — no business features implemented yet.
- Stack: Go 1.27 · chi · pgx · sqlc · goose · PostgreSQL 16+ · React + TS + Vite · Tailwind · shadcn/ui · PWA

## Layout

| Path                 | Purpose                                   |
| -------------------- | ----------------------------------------- |
| `backend/`           | Go API server                             |
| `frontend/`          | PWA (customer QR ordering + staff panel)  |
| `deploy/`            | Deployment config (manual, no auto-deploy)|
| `docs/`              | Project documentation                     |
| `.github/workflows/` | CI (lint/build/test only)                 |

## Getting started

Requirements: Go 1.27, Docker Desktop (Compose v2), PowerShell 7.

```powershell
./scripts/dev.ps1 setup   # create .env, start PostgreSQL 17 on 127.0.0.1:55432, apply migrations
./scripts/dev.ps1 run     # API on http://127.0.0.1:8080  (GET /healthz, GET /readyz)
./scripts/dev.ps1 check   # gofmt, go vet, build, tests incl. PostgreSQL integration, sqlc diff
```

Other commands: `db-up`, `db-down`, `db-reset`, `migrate`, `status`, `build`, `fmt`, `vet`,
`test`, `sqlc`, `sqlc-diff`, `smoke`. The server binary itself: `rokh serve | migrate up |
migrate status | version`.

### Frontend

Requirements: Node 24, npm 11. Two Vite entry points share `src/components` and `src/lib`:
`index.html` → customer app (`/menu…`, plain web, no service worker) and `admin/index.html` →
staff app (`/admin/…`, installable PWA scoped to `/admin/`).

```powershell
./scripts/dev.ps1 fe-dev     # http://127.0.0.1:5173/menu and /admin/ (proxies /api, /healthz, /readyz to :8080)
./scripts/dev.ps1 fe-check   # typecheck, ESLint, vitest, build, staff-only PWA check (same as CI)
```

Also `fe-install`, `fe-test`, `fe-build`, or the npm scripts in `frontend/package.json`.

### Without Docker

Install PostgreSQL 16+ locally, run
`psql -U postgres -v owner_password=... -v app_password=... -v db_name=rokhtiacafe -f deploy/local/postgres/roles.sql`,
and point `DATABASE_URL` / `MIGRATION_DATABASE_URL` / `TEST_DATABASE_ADMIN_URL` in `.env` at it.

Database roles: migrations run as `rokh_owner` (`MIGRATION_DATABASE_URL`); the server runs as
`rokh_app` (`DATABASE_URL`, DML only). The `.env.example` values are local development defaults;
production credentials are separate and never committed.

## Design docs

| Doc | What it covers |
|-----|----------------|
| [PRD](docs/PRD.md) | Scope, roles, non-goals |
| [ARCHITECTURE](docs/ARCHITECTURE.md) | Modular monolith, layout, design tokens, deployment shape |
| [DATABASE](docs/DATABASE.md) / [ERD](docs/ERD.md) | Schema, invariants, cost model, report definitions |
| [API-CONTRACT](docs/API-CONTRACT.md) | REST `/api/v1` endpoints and errors |
| [WORKFLOWS](docs/WORKFLOWS.md) | Order, inventory, payment, push, auth flows |
| [SECURITY](docs/SECURITY.md) | Threats, permission matrix, controls |
| [ACCEPTANCE](docs/ACCEPTANCE.md) | Testable acceptance criteria |
| [IMPLEMENTATION-PLAN](docs/IMPLEMENTATION-PLAN.md) | Tasks, decisions, hosting assessment, owner decisions |
| [REVIEW-RESPONSE](docs/REVIEW-RESPONSE.md) | Architecture review findings F1–F14 and their dispositions |

## Workflow

See [docs/git-workflow.md](docs/git-workflow.md). In short: `main` holds reviewed code;
work happens on `feature/*` branches merged via Pull Request.

## Security

This repository is **public**. Never commit `.env` files, keys, certificates,
database dumps, backups, or credentials. Only `.env.example` with placeholders is tracked.
