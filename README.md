# Rokh Tia Café

Lightweight café management and QR ordering PWA.

- Production: https://rokh.tiacafe.ir (not deployed from this repo yet)
- Status: project skeleton only — no business features implemented.

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

## Workflow

See [docs/git-workflow.md](docs/git-workflow.md). In short: `main` holds reviewed code;
work happens on `feature/*` branches merged via Pull Request.

## Security

This repository is **public**. Never commit `.env` files, keys, certificates,
database dumps, backups, or credentials. Only `.env.example` with placeholders is tracked.
