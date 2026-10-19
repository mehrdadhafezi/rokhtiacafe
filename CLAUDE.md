# CLAUDE.md

Guidance for Claude Code working in this repository.

## Project

Rokh Tia Café — lightweight café management and QR ordering PWA.
Monorepo: `backend/` (Go), `frontend/` (PWA), `deploy/`, `docs/`, `.github/workflows/`.

## Hard rules

- The GitHub repo is **PUBLIC**. Never commit secrets, `.env*` (except `.env.example`),
  keys, certificates, DB dumps, or backups. Review `git diff --cached --stat` before every commit.
- **Never touch production.** The live server (rokh.tiacafe.ir, 62.204.61.18) is off-limits:
  no SSH, no deploys, no remote commands unless the user explicitly asks in chat.
- No automatic production deployment in CI. Workflows may lint, build and test only.
- No force-push, no history rewrites on `main`.

## Git workflow

- `main`: reviewed, validated code only.
- `feature/<short-name>`: all implementation work; merge via Pull Request.
- Descriptive, imperative commit messages.

## Local tooling (Windows)

Go 1.22, Node 24 / npm 11, Docker + Compose, gh CLI.
