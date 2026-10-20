# Deployment runbook — Rokh Tia Café

Manual, owner-approved deployments only. Nothing in this repository deploys automatically, and
no server credentials, keys or real `.env` values are ever stored here.

The production target is decided by the read-only hosting assessment
([docs/HOSTING-ASSESSMENT.md](../docs/HOSTING-ASSESSMENT.md), decision D1): the existing cPanel
host if it passes, otherwise a small VPS for `rokh.tiacafe.ir` only. The steps below are the same
for both, except **[Start and restart](#5-start-and-restart)**.

## 1. What gets deployed

One file: `rokh-linux-amd64`. It contains the API, the customer app (`/menu`), the staff PWA
(`/admin/`), the SQL migrations and the background jobs (order expiry, Web Push outbox, cleanup).

```powershell
./scripts/dev.ps1 release        # or: bash scripts/build-release.sh v1.0.0
```

The output in `release/` is `rokh-linux-amd64`, `rokh-linux-amd64.sha256` and `BUILDINFO.txt`. The
build is reproducible: CI (`release` workflow) rebuilds from scratch and compares the SHA-256,
and keeps the binary as a 14-day artifact. Always deploy a binary whose SHA-256 matches CI.

## 2. Server layout

```
~/rokh/
  bin/rokh            current binary            (chmod 750)
  bin/rokh.prev       previous binary (rollback)
  scripts/            copies of deploy/scripts/*.sh
  .env                runtime settings          (chmod 600) — template: env/production.env.example
  .env.migrate        MIGRATION_DATABASE_URL    (chmod 600) — template: env/migrate.env.example
  uploads/            product images, outside any public web root
  backups/            database dumps + uploads archives (chmod 700)
  logs/               rokh.log when the process manager does not capture stdout
```

`rokh` reads `.env` from its working directory, so run every command from `~/rokh`. Settings
already present in the environment take precedence over `.env`.

## 3. First installation

1. **Database (PostgreSQL 16+).** Two roles (docs/DATABASE.md §6): `rokh_owner` owns the
   schema and runs migrations; `rokh_app` is DML-only and used by the server.
   - **VPS** (you have a PostgreSQL superuser): run the same script as development
     (`deploy/local/postgres/roles.sql`) with new passwords:
     `psql -U postgres -v owner_password=… -v app_password=… -v db_name=rokhtiacafe -f roles.sql`.
     Keep the passwords out of shell history, e.g. with a leading space when `HISTCONTROL=ignorespace`.
   - **cPanel:** only if the hosting assessment confirmed that both roles can be created with
     exactly these names and grants. If not, use the VPS option.
2. **Files.** Create the layout above. Copy the binary, check
   `sha256sum -c rokh-linux-amd64.sha256`, then install it as `bin/rokh`.
3. **Settings.** Copy `env/production.env.example` to `~/rokh/.env` and fill it in:
   - `APP_SECRET_KEY` from `bin/rokh secret gen`;
   - `VAPID_*` from `bin/rokh vapid gen` (optional, enables push alerts);
   - the `rokh_app` connection string.

   Put the `rokh_owner` connection string in `.env.migrate`. Then `chmod 600 .env .env.migrate`.
4. **Schema.** `env $(cat .env.migrate) bin/rokh migrate up`, then `bin/rokh migrate status`
   (expect `0 pending`).
5. **Preflight.** `bin/rokh config check` must end with `ready to serve`. It checks the
   settings, the embedded frontend, the upload directory and the database connection, and never
   prints secrets.
6. **First admin.** `bin/rokh user create -username owner -display-name "…" -role ADMIN`
   (the password is read from the prompt, never from arguments).
7. **Start** the service (§5) and **verify** (§6).

## 4. Reverse proxy and TLS

`rokh` listens on loopback only (production refuses anything else) and must sit behind the
TLS-terminating proxy. Set `TRUSTED_PROXIES` to the proxy address so rate limits see the real
client IP.
- **VPS:** Caddy (`caddy/Caddyfile`) gets and renews the certificate and proxies everything to
  `127.0.0.1:8080`.
- **cPanel:** use the host's supported way to proxy a subdomain to `127.0.0.1:<port>`. Only use
  `cpanel/htaccess.example` if the hosting assessment confirmed proxying through `.htaccess` is
  allowed.

No separate static-file hosting is needed: the binary serves `/menu`, `/admin/`, the hashed
assets (cached for a year), the service worker (`no-cache`) and `/media/` product images.

## 5. Start and restart

- **Graceful stop:** send `SIGTERM`. In-flight requests finish within `SHUTDOWN_TIMEOUT`
  (default 15 s), then the process exits with code 0. Order expiry and the push outbox resume on
  the next start; leased push claims are reclaimed after 60 s.
- **VPS (systemd):** install `systemd/rokh.service`, then run
  `sudo systemctl enable --now rokh` and later `sudo systemctl restart rokh`. systemd restarts
  the process on crash.
- **cPanel (no root):** use `scripts/run.sh`, a supervisor loop that restarts after a crash with a
  delay. Start it at boot with the cron line `@reboot cd ~/rokh && ./scripts/run.sh >> logs/rokh.log 2>&1`.
  To restart (e.g. after an upgrade), run `pkill -TERM -f 'bin/rokh serve'`; the loop starts the
  new binary. Use this only if the hosting assessment confirmed long-running processes are allowed.

Note: the ordering kill switch is held in memory and resets to the configured hours on restart
(owner decision O4). Re-apply it after a restart if it was on.

## 6. Verify after every start

```sh
curl -fsS http://127.0.0.1:8080/healthz          # {"status":"ok"}    — process is up
curl -fsS http://127.0.0.1:8080/readyz           # {"status":"ready"} — database reachable
curl -fsSI https://rokh.tiacafe.ir/menu          # 200 through the proxy, Strict-Transport-Security set
bin/rokh check                                   # "ok: all invariants hold"
```

Then open `https://rokh.tiacafe.ir/menu` on a phone and log in at `/admin/`.

## 7. Upgrade

1. **Backup** (§8), and copy the dump off the server.
2. Read the release notes. Migrations are forward-only and stay compatible with the previous
   binary for one release (docs/ARCHITECTURE.md §4).
3. Upload the new binary, check its SHA-256, then `mv bin/rokh bin/rokh.prev && mv rokh-linux-amd64 bin/rokh`.
4. `env $(cat .env.migrate) bin/rokh migrate up`.
5. `bin/rokh config check`, restart (§5), verify (§6).

## 8. Backup

`scripts/backup.sh` (run as the app user from `~/rokh`, daily from cron, e.g.
`30 3 * * * cd ~/rokh && ./scripts/backup.sh >> logs/backup.log 2>&1`) writes:
- `backups/rokh-YYYYMMDD-HHMM.dump`, a `pg_dump --format=custom` taken with the **owner** role,
  so it includes the schema and grants;
- `backups/uploads-YYYYMMDD-HHMM.tar.gz`, the product images.

It keeps 14 days of backups locally. **Copy them off the server** (owner's PC or cloud storage),
encrypted at rest (for example `age` or `gpg`), as SECURITY.md requires. A backup that has never
been restored is not a backup: test a restore (§9) before go-live and after any PostgreSQL
upgrade.

## 9. Restore

`scripts/restore.sh` never touches the live database:
1. Create a **new, empty** database owned by `rokh_owner` and let the app role connect:
   `CREATE DATABASE rokhtiacafe_restore OWNER rokh_owner; GRANT CONNECT ON DATABASE rokhtiacafe_restore TO rokh_app;`
2. Run `RESTORE_DB=rokhtiacafe_restore scripts/restore.sh backups/rokh-….dump [backups/uploads-….tar.gz]`.
   It uses `pg_restore --no-owner --role=rokh_owner`, runs `rokh migrate up` and `rokh check`
   against the restored database, and unpacks uploads into `uploads.restored/`.
3. To switch production to it: stop the service (§5), point `DATABASE_URL` and `.env.migrate`
   at the restored database, replace `uploads/` with `uploads.restored/`, then start and
   verify (§6).

Rehearsing steps 1–2 regularly is the restore test; it does not affect the running café.

## 10. Rollback

- **Binary only:** `mv bin/rokh bin/rokh.bad && mv bin/rokh.prev bin/rokh`, restart, verify.
- **After a migration:** migrations are forward-only, and the previous binary works with the new
  schema for one release, so roll back the binary as above. Restore the pre-upgrade backup (§9)
  only if the data itself is wrong. Restoring loses orders taken since the backup, so it is an
  owner decision.

## 11. Logs and troubleshooting

- Production logs are JSON lines on stdout with a request id. Passwords, tokens, cookies and push
  endpoints are never logged.
- **`config check` fails:** the message names the setting (e.g. HTTP_ADDR not on loopback,
  missing TRUSTED_PROXIES, owner role used at runtime).
- **`readyz` returns 503:** the database is unreachable or the credentials are wrong.
- **Push alerts don't arrive:** check `web push` in `config check`. Outbound HTTPS to the push
  services must be allowed. The dashboard low-stock list works regardless.
