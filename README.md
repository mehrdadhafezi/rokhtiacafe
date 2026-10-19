# Rokh Tia Café

Lightweight café management and QR ordering PWA.

- Production: https://rokh.tiacafe.ir (not deployed from this repo yet)
- Status: architecture & design docs under review — no business features implemented yet.
- Stack: Go 1.27 · chi · pgx · sqlc · goose · PostgreSQL · React + TS + Vite · Tailwind · shadcn/ui · PWA

## Layout

| Path                 | Purpose                                   |
| -------------------- | ----------------------------------------- |
| `backend/`           | Go API server                             |
| `frontend/`          | PWA (customer QR ordering + staff panel)  |
| `deploy/`            | Deployment config (manual, no auto-deploy)|
| `docs/`              | Project documentation                     |
| `.github/workflows/` | CI (lint/build/test only)                 |

## Getting started

```bash
cp .env.example .env   # then edit .env with local values
```

Backend and frontend setup instructions will be added as each is scaffolded.

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
| [IMPLEMENTATION-PLAN](docs/IMPLEMENTATION-PLAN.md) | Tasks, decisions, production inspection checklist |

## Workflow

See [docs/git-workflow.md](docs/git-workflow.md). In short: `main` holds reviewed code;
work happens on `feature/*` branches merged via Pull Request.

## Security

This repository is **public**. Never commit `.env` files, keys, certificates,
database dumps, backups, or credentials. Only `.env.example` with placeholders is tracked.
