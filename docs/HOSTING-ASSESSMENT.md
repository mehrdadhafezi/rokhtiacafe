# Hosting assessment (T0) — read-only checklist and report

**Status:** not yet run (needs the owner's approval and cPanel access). Production deployment is
blocked until this report is filled in and D1 is decided (IMPLEMENTATION-PLAN §3–4).

**Target:** `rokh.tiacafe.ir` → 62.204.61.18 (shared cPanel host)
**Purpose:** decide D1, i.e. whether the Rokh Tia binary and PostgreSQL can run on this host. If not, use a small separate VPS for `rokh.tiacafe.ir` only and leave the existing server untouched.

**Rules for whoever runs it**
- Read-only. Do not install, start, stop, edit, upload or restart anything.
- No `.htaccess`, cron, DNS, SSL, database or file changes.
- Run it only with the owner's explicit approval, logged in as the owner's cPanel user (never root).
- Paste each result into the right-hand column. Do not paste passwords, tokens, keys or full paths containing usernames into the report or Git.
- Stop at the first hard **No-Go** in items 3–6. The answer is then a VPS, and the rest is informational.

| # | Question | How to check (read-only) | Go if… | Result |
|---|---|---|---|---|
| 1 | DNS points to the host | From any PC: `nslookup rokh.tiacafe.ir` and `nslookup -type=AAAA rokh.tiacafe.ir` | A = 62.204.61.18; no unexpected AAAA | |
| 1b | TLS covers the subdomain | Browser → `https://rokh.tiacafe.ir` → certificate details, or cPanel → SSL/TLS Status | Valid certificate whose names include `rokh.tiacafe.ir`; AutoSSL renews it | |
| 2 | cPanel features available | cPanel home: list **Terminal/SSH Access**, **Cron Jobs**, **Setup Node.js/Python App** (Passenger), **Subdomains** → document root of `rokh` | Terminal or SSH present; subdomain exists (note its doc root) | |
| 3 | A long-running process is allowed | Ask the host, or read its terms/KB: are user processes killed (CloudLinux LVE limits, a "max process run time")? cPanel → **Resource Usage** shows the limits. In Terminal: `ulimit -a`, `cat /proc/self/limits` | Persistent user processes are allowed and survive; there is a supported way to restart on crash and reboot (cron `@reboot`, a host process manager or Passenger) | |
| 4 | Reverse proxy to `127.0.0.1:<port>` | Ask the host: is `mod_proxy` / LiteSpeed proxy via `.htaccess` (`RewriteRule … [P]`) allowed for user apps? Do they set `X-Forwarded-For`? **Do not create or edit any `.htaccess` to test.** | Proxy to a localhost port is supported and forwards the client IP | |
| 5 | PostgreSQL 16+ with two roles | cPanel → is there **PostgreSQL Databases** (not only MySQL)? If yes: version (host KB or `psql --version` in Terminal) and whether you can create two users with different grants | PostgreSQL ≥ 16; an owner role plus a DML-only app role are possible; `pg_dump` is available | |
| 5b | Role names without a prefix | cPanel usually prefixes database and user names (e.g. `cpuser_rokh`). Check whether roles named exactly `rokh_owner` and `rokh_app` can exist; migrations grant to `rokh_app` by name | Exact names possible, or the host can create them | |
| 6 | Linux amd64 binaries can run from home | Terminal: `uname -m` (expect `x86_64`), `mount \| grep -E ' /home'` (look for `noexec`), `cat /etc/os-release` | `x86_64`; home is **not** mounted `noexec` | |
| 7a | Disk and backups | cPanel → **Disk Usage** (quota), **Backup** / JetBackup availability and schedule | ≥ 2 GB free for app, uploads and dumps; backups include files the owner chooses | |
| 7b | Outbound HTTPS to push services | Terminal (read-only GETs): `curl -sS -o /dev/null -w '%{http_code}\n' https://fcm.googleapis.com/` and the same for `https://updates.push.services.mozilla.com/` and `https://web.push.apple.com/` | Any HTTP status (404/405 is fine). A timeout or connection refused means push cannot work from this host | |
| 8 | Do not disturb existing services | cPanel → list existing domains, apps, cron jobs and databases (names only) | Nothing that the café app would need to change or share | |

## Decision

- **Items 3, 4, 5, 5b and 6 all Go:** deploy on this host (T13 docs cover running the binary behind the host proxy).
- **Any of 3–6 No-Go:** small VPS (e.g. 1 vCPU / 1–2 GB RAM, Ubuntu LTS, Caddy for TLS, PostgreSQL 16/17). Point only `rokh.tiacafe.ir` at it. Nothing changes on 62.204.61.18 except, later and with approval, the DNS record.
- **7b No-Go only:** the app can still launch. Push alerts stay off (the dashboard low-stock list keeps working) until outbound HTTPS is available.

## Report

Fill in the Result column and the decision below via a PR. The report must contain no
credentials or private paths.

- Inspected on: _(date)_ by: _(name)_, with the owner's approval on: _(date)_
- **D1 decision:** _(cPanel host / VPS)_ — reason: _(…)_
- Follow-up: deployment steps in [deploy/README.md](../deploy/README.md).
