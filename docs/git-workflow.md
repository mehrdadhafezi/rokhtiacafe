# Git workflow

## Branches

- `main` — reviewed and validated code. No direct feature work, no force-push.
- `feature/<short-name>` — one branch per task, e.g. `feature/backend-skeleton`.

## Flow

1. `git switch main && git pull`
2. `git switch -c feature/<short-name>`
3. Commit in small, descriptive steps.
4. Before each commit: `git status` and `git diff --cached --stat` — confirm no secrets.
5. `git push -u origin feature/<short-name>` and open a Pull Request into `main`.
6. Merge after review and green CI.

## Deployment

Deployment to production is manual and out of scope for CI. Nothing in this repo
deploys automatically.
