# Deployment runbook — Rokh Tia Café

Manual, owner-approved deployments only. Nothing in this repository deploys automatically, and
no server credentials, keys or real settings files are ever stored here. **Every step that
touches the server needs the owner's explicit approval** (list in
[HOSTING-ASSESSMENT.md §5](../docs/HOSTING-ASSESSMENT.md#5-procedures-that-need-the-owners-approval)),
and production stays **NO-GO** until the release gate in
[HOSTING-ASSESSMENT.md §6](../docs/HOSTING-ASSESSMENT.md#6-release-gate-gono-go) is signed off.

**Target (decision D1, proposed):** the existing cPanel/WHM server: AlmaLinux 8.10, LiteSpeed
Enterprise 6.3.4, origin 62.204.61.18, behind the MizbanCloud CDN. The server runs other,
unrelated production sites: nothing here changes global LiteSpeed/Apache settings, DNS, CDN,
firewall, SSL or WHM configuration. A separate VPS (`caddy/Caddyfile`) remains the fallback only.

```
visitor ──HTTPS──▶ MizbanCloud edge ──HTTPS──▶ LiteSpeed (origin, rokh.tiacafe.ir vhost)
                                                   │ userdata include, ProxyPass
                                                   ▼
                                     rokh serve on 127.0.0.1:18080   (systemd, user rokh)
                                                   │ TCP 127.0.0.1:15432
                                                   ▼
                                     PostgreSQL 16 in Docker (rokh-postgres, dedicated)
```

## 1. What gets deployed

One file: `rokh-linux-amd64`. It contains the API, the customer app (`/menu`), the staff PWA
(`/admin/`), the SQL migrations and the background jobs (order expiry, Web Push outbox, cleanup).

```sh
bash scripts/build-release.sh v1.0.0      # or: ./scripts/dev.ps1 release
```

The output in `release/` is `rokh-linux-amd64`, `rokh-linux-amd64.sha256` and `BUILDINFO.txt`.
The build is reproducible: the CI `release` workflow rebuilds from scratch, compares the SHA-256
and keeps the binary as a 14-day artifact. Before installing, run
`deploy/scripts/verify-release.sh <dir> <SHA-256 shown by CI>`. Never install a binary whose
SHA-256 does not match CI.

## 2. Server layout

| Path | Owner, mode | Contents |
|---|---|---|
| `/opt/rokh/bin/rokh`, `rokh.prev` | root:rokh 750 | current and previous binary (rollback) |
| `/opt/rokh/scripts/` | root:root 750 | copies of `deploy/scripts/*` (including `lib.sh`) |
| `/opt/rokh/postgres/compose.yaml`, `initdb/` | root:root 750 | `deploy/postgres/compose.yaml`; `deploy/local/postgres/{10-rokh-init.sh,roles.sql}` |
| `/etc/rokh/rokh.env` | root:rokh 640 | runtime settings and secrets (template `env/production.env.example`) |
| `/etc/rokh/migrate.env` | root:root 600 | `MIGRATION_DATABASE_URL`, owner role (`env/migrate.env.example`) |
| `/etc/rokh/postgres.env` | root:root 600 | first-initialisation passwords for the container (`env/postgres.env.example`) |
| `/etc/rokh/ops.env` | root:root 600 | settings of the ops scripts, no secrets (`env/ops.env.example`) |
| `/var/lib/rokh/` | root:rokh 750 | service working directory; must contain no `.env` |
| `/var/lib/rokh/uploads/` | rokh:rokh 750 | product images; the service's only writable path |
| `/backup/rokh/` | root:root 700 | backup sets on the separately mounted backup disk |
| Docker volume `rokh-pgdata` | Docker | PostgreSQL data (root disk, under `/var/lib/docker`) |

Logs go to the journal (`journalctl -u rokh`) and to Docker's capped JSON log for PostgreSQL.

**Settings files are data, not shell.** systemd reads `rokh.env` (`EnvironmentFile=`); the ops
scripts read single keys from the files and never `source` them. Write values without quotes, and
use passwords made only of `[A-Za-z0-9]` (`openssl rand -hex 24`) so database URLs need no
percent-encoding.

`rokh` also reads `./.env` and `../.env` from its working directory when they exist (a local
development convenience; variables already in the environment win). The service runs in
`/var/lib/rokh` and the scripts run it from `/`; `preflight.sh` fails if `/.env` exists.

**Running a rokh command as the service user with the service settings** (no secret on any command
line), used below as `rokh-as-service`:

```sh
systemd-run --quiet --wait --pipe --uid=rokh --gid=rokh \
  --property=EnvironmentFile=/etc/rokh/rokh.env /opt/rokh/bin/rokh check
```

Add `--pty` instead of `--pipe` for interactive commands (`user create` asks for the password).

## 3. First installation (owner-approved, in this order)

1. **Capacity.** The root disk is ~90 % used (~22 GB free). Confirm at least 5 GB stays free
   after pulling the PostgreSQL image and allowing for database growth. Backups go to `/backup`,
   never to the root disk.
2. **System user and directories:**
   ```sh
   useradd --system --home-dir /var/lib/rokh --shell /sbin/nologin rokh
   install -d -o root -g rokh -m 750 /opt/rokh /opt/rokh/bin /var/lib/rokh
   install -d -o root -g root -m 750 /opt/rokh/scripts /opt/rokh/postgres /opt/rokh/postgres/initdb
   install -d -o rokh -g rokh -m 750 /var/lib/rokh/uploads
   install -d -o root -g root -m 755 /etc/rokh
   install -d -o root -g root -m 700 /backup/rokh
   ```
3. **Settings.** Copy the four templates from `deploy/env/` to `/etc/rokh/` with the owners and
   modes from §2, then fill them in:
   - three new passwords (`openssl rand -hex 24`) in `postgres.env`; the owner and app passwords
     also go into `migrate.env` and `rokh.env`;
   - `APP_SECRET_KEY` from `rokh secret gen`; optionally `VAPID_*` from `rokh vapid gen`.
4. **PostgreSQL** (§4): check that the port is free (`ss -ltn | grep 15432` prints nothing),
   then `cd /opt/rokh/postgres && docker compose up -d`. `docker compose ps` must show `healthy`.
5. **Binary:** `verify-release.sh`, then `install -o root -g rokh -m 750 rokh-linux-amd64 /opt/rokh/bin/rokh`.
6. **Schema:** `/opt/rokh/scripts/migrate.sh up`, then `migrate.sh status` (expect `0 pending`).
7. **Preflight:** `/opt/rokh/scripts/preflight.sh` must end with `preflight ok`.
8. **First admin:** `rokh-as-service` with `--pty` and
   `/opt/rokh/bin/rokh user create -username owner -display-name "…" -role ADMIN`. The password is
   typed at the prompt without echo, never passed as an argument.
9. **Service** (§6), then **reverse proxy** (§5), then **verify** (§7), then the **backup cron** (§8).

## 4. PostgreSQL (dedicated Docker instance)

### 4.1 Template

`postgres/compose.yaml` runs `postgres:16-bookworm` as container `rokh-postgres`, project `rokh`,
network `rokh-db`, volume `rokh-pgdata`. It has `restart: unless-stopped`, a `pg_isready` health
check, `stop_grace_period: 60s`, 1 GB memory and 1.5 CPU limits, `shared_buffers=256MB` and Docker
log rotation (3 × 10 MB). It is for Rokh only: never point Rokh at another project's PostgreSQL,
and never let another project use this one.

- **No public port.** The container port is published on `127.0.0.1:15432` only. The rokh service
  on the host connects to it; nothing outside the host can. Inside the container, socket
  connections use peer authentication and TCP uses `scram-sha-256`; there are no `trust` rules.
- **Roles** (docs/DATABASE.md §6.1). On first start, `initdb/10-rokh-init.sh` runs the same
  `roles.sql` as development and CI: `rokh_owner` owns the database and schema `rokh`, and
  `rokh_app` gets `CONNECT`. The migrations grant `rokh_app` its DML-only privileges. Neither role
  is superuser, `CREATEDB` or `CREATEROLE`. The container superuser `postgres` is used only to
  create restore targets (§9) and for upgrades.
- **Pin the image** after the first pull. Record the digest
  (`docker image inspect --format '{{index .RepoDigests 0}}' postgres:16-bookworm`) in
  `ROKH_PG_IMAGE`, so a later `docker compose pull` cannot change the version unnoticed.
- **Storage.** The volume lives on the root disk. The café database is small (expected well under
  1 GB in the first year), but check `docker system df` and `df -h /` monthly (§11).
- **Docker and the host firewall.** Docker manages its own iptables rules. If the server firewall
  (e.g. CSF) restarts, confirm that `docker compose ps` is still healthy and `/readyz` still
  answers. Restart the container if the rules were flushed.

### 4.2 Minor updates (16.x → 16.y)

Backup (§8) → `docker compose pull` (new digest) → `docker compose up -d` → health check →
`/readyz` → `rokh check`. The data stays in the volume.

### 4.3 Major upgrades (16 → 17)

Never change the major version in place:
1. Back up (§8).
2. Start a second container of the new version, on another loopback port with its own volume.
3. Restore the latest set into it (§9, using a `*_restore_*` database).
4. Run the release gate checks against it.
5. Switch `DATABASE_URL` and `MIGRATION_DATABASE_URL` during a short, owner-approved maintenance
   window.

Keep the old volume until the owner confirms the new one.

## 5. Reverse proxy (LiteSpeed, cPanel userdata include)

### 5.1 Files

- **`litespeed/rokh-proxy.ssl.conf.example`** → `/etc/apache2/conf.d/userdata/ssl/2_4/CPUSER/rokh.tiacafe.ir/rokh-proxy.conf`.
  For the HTTPS vhost of `rokh.tiacafe.ir` only:
  - keeps `/.well-known/` on the document root;
  - proxies everything else to `127.0.0.1:18080` with `ProxyPreserveHost On`;
  - removes the CDN's alternative client-IP headers;
  - adds no security headers (rokh sets CSP, HSTS, nosniff and frame-ancestors).
- **`litespeed/rokh-redirect.std.conf.example`** → `…/userdata/std/2_4/CPUSER/rokh.tiacafe.ir/rokh-redirect.conf`.
  For port 80: redirects to HTTPS except `/.well-known/`. Plain HTTP is never proxied.

The same mechanism (an Apache `ProxyPass` userdata include, read by LiteSpeed) already works for
another domain on this server. Only these two domain-specific files are added; no global file is
edited.

### 5.2 Apply and roll back (owner-approved maintenance window)

1. Save the current state: `cp -a /etc/apache2/conf.d/userdata /root/rokh-userdata-before-$(date +%F)`.
2. Copy the two files, then run `/scripts/ensure_vhost_includes --user=CPUSER`.
3. Check the generated configuration:
   - `apachectl -t` (syntax);
   - only the vhost of `rokh.tiacafe.ir` gained the `Include` lines (`grep -n rokh /etc/apache2/conf/httpd.conf`).
4. Graceful restart of LiteSpeed (WHM → LiteSpeed Web Server → Graceful Restart). Then run §5.3
   and check that another site on the server still works.
5. **Rollback:** remove the two `rokh-*.conf` files, run step 2's command again and restart
   LiteSpeed gracefully. The site falls back to its document root; rokh keeps running harmlessly
   on loopback.

### 5.3 To confirm during the approved deployment (not verified yet)

- **Include survives.** cPanel keeps the include across `rebuildhttpdconf` and updates, and
  LiteSpeed picks it up after a graceful restart. Re-check after the next cPanel update.
- **Directives honoured.** LiteSpeed honours `ProxyPreserveHost`, `ProxyPass … !` and `timeout=`
  for this vhost. A 3 MB upload works through CDN → LiteSpeed → rokh, and a 25-second request is
  not cut off.
- **Forwarding headers** (risk R1). Record what LiteSpeed sends to rokh in `X-Forwarded-For`, for
  a request through the CDN and for one sent straight to the origin. Compare with the visitor's
  real IP, then decide `TRUSTED_PROXIES` (docs/SECURITY.md §5.1).
- **Security headers intact.** `Strict-Transport-Security`, `Content-Security-Policy` and
  `X-Frame-Options` reach the browser exactly once: no duplicates or removals by LiteSpeed or the
  CDN.

## 6. Service (systemd)

Install `systemd/rokh.service` as `/etc/systemd/system/rokh.service`, then
`systemctl daemon-reload && systemctl enable --now rokh`.

- **Identity and settings.** It runs `/opt/rokh/bin/rokh serve` as the unprivileged `rokh` user,
  in `/var/lib/rokh`, with `EnvironmentFile=/etc/rokh/rokh.env`.
- **No host PostgreSQL dependency.** If the database is not reachable yet (e.g. right after a
  reboot, while Docker starts), rokh exits and systemd retries every 5 s indefinitely.
- **Stop.** `SIGTERM`: in-flight requests finish within `SHUTDOWN_TIMEOUT` (15 s), and the process
  exits with 0 and is not restarted. A hard kill follows after `TimeoutStopSec=30`. Order expiry
  and the push outbox resume on the next start; leased push claims are reclaimed after 60 s.
- **Hardening** (all supported by AlmaLinux 8's systemd 239):
  - no capabilities, `NoNewPrivileges`;
  - read-only system (`ProtectSystem=strict`), no `/home` access, private `/tmp` and devices;
  - only `/var/lib/rokh/uploads` writable;
  - IPv4, IPv6 and Unix sockets only; `MemoryDenyWriteExecute`.
- **Operations.** Restart after an upgrade with `systemctl restart rokh`. Status:
  `systemctl status rokh` and `journalctl -u rokh -n 100`.

Note: the ordering kill switch is held in memory and resets to the configured hours on restart
(owner decision O4). Re-apply it after a restart if it was on.

## 7. Verify after every start

```sh
curl -fsS http://127.0.0.1:18080/healthz      # {"status":"ok"}    — process up
curl -fsS http://127.0.0.1:18080/readyz       # {"status":"ready"} — database reachable
/opt/rokh/scripts/preflight.sh                # settings, roles, 0 pending migrations
curl -sSI https://rokh.tiacafe.ir/menu | grep -i -e '^HTTP' -e strict-transport -e mc-cache-status
curl -sSI https://rokh.tiacafe.ir/api/v1/public/menu | grep -i mc-cache-status   # never HIT
```

Then run `rokh check` with `rokh-as-service` (§2) and expect "ok: all invariants hold". Finally,
open `https://rokh.tiacafe.ir/menu` on a phone and log in at `/admin/`.

## 8. Backup

### 8.1 What `backup.sh` does

Run it as root, daily, from root's crontab:
`30 3 * * * /opt/rokh/scripts/backup.sh 2>&1 | logger -t rokh-backup`.

Each run writes one set, `BACKUP_DIR/rokh-YYYYmmdd-HHMMSS/`:
- `db.dump`: `pg_dump --format=custom` as the owner role, so schema, grants and default
  privileges are included;
- `uploads.tar.gz`: the product images, temp files excluded;
- `INFO`: versions and sizes, no secrets;
- `SHA256SUMS`.

The set is built in a hidden `.partial` directory and renamed into place only after every check
has passed.

**Refused (exit 1, nothing written)** when:
- `BACKUP_DIR` is unset, relative, missing (it is never created) or world-writable;
- `BACKUP_DIR` is not on `BACKUP_REQUIRE_MOUNT` (`/backup`), which protects against the backup
  disk not being mounted, or it is on the root filesystem;
- less than `BACKUP_MIN_FREE_MB` is free;
- `UPLOAD_DIR` is missing;
- another backup holds the lock.

**Failed (exit 2, nothing published)** when the dump fails, the dump is unreadable
(`pg_restore --list`), the dump lacks the Rokh schema, archiving fails, or a checksum fails.

**Exit 3** means the backup is fine, but retention kept a set it could not safely remove.

### 8.2 Retention

- Only directories named exactly `rokh-YYYYmmdd-HHMMSS` in `BACKUP_DIR` are candidates.
- The newest `BACKUP_KEEP_MIN` (7) are always kept.
- Older sets past `BACKUP_KEEP_DAYS` (14) are removed only if they contain nothing but the four
  known files. Otherwise they are kept and reported (exit 3).
- Other files and directories, including other applications' backups, are never touched.
- Retention runs only after a successful backup.

Docker volumes are **not** covered by cPanel/JetBackup account backups: these sets are the only
database backups.

### 8.3 Off-site copy (required)

The backup disk is inside the same server, so copy every set off it, encrypted at rest
(docs/SECURITY.md §9), at least weekly and before every upgrade:

1. Pull it from the owner's PC (the server never holds credentials for the destination):
   `scp -r root@SERVER:/backup/rokh/rokh-YYYYmmdd-HHMMSS ./`
2. Verify it: `cd rokh-YYYYmmdd-HHMMSS && sha256sum -c SHA256SUMS`.
3. Encrypt it, e.g. `tar -cf - rokh-YYYYmmdd-HHMMSS | age -r <owner public key> > rokh-YYYYmmdd-HHMMSS.tar.age`.
   Store it in two places (PC and cloud storage), then delete the unencrypted copy from the PC.
4. Record the date in the operations log.

Rehearse a restore (§9) from an off-site copy at least once a quarter.

## 9. Restore

`restore.sh` never touches the live database or the live uploads directory.

### 9.1 Rehearsal (regular; does not affect the running café)

1. **Create a new, empty target** as the container superuser. Names must contain `_restore_`, and
   an existing database is refused:
   `docker exec -i -u postgres rokh-postgres psql -X -v db_name=rokhtiacafe_restore_20261020 < /opt/rokh/scripts/restore-target.sql`
2. **Restore a set:**
   `RESTORE_DB=rokhtiacafe_restore_20261020 RESTORE_UPLOADS_DIR=/var/lib/rokh/uploads.restore-20261020 /opt/rokh/scripts/restore.sh /backup/rokh/rokh-…`

   It first checks:
   - the set: checksums; `db.dump` is a readable dump with the Rokh schema; `uploads.tar.gz`
     holds only plain files and directories with safe relative names (no absolute paths, `..`,
     links or devices);
   - the target: the name is valid; it is not the live database from either settings file; it
     exists, is exactly that database, is owned by `rokh_owner` and is empty; `rokh_app` may
     connect;
   - `RESTORE_UPLOADS_DIR`: absolute, not the live `UPLOAD_DIR`, and not existing yet.

   Then it runs:
   - `pg_restore --single-transaction` (a failure leaves the target empty);
   - `rokh migrate up`, and `migrate status` must show `0 pending`;
   - `rokh check` as the app role, then prints row counts;
   - finally, unpacks the uploads into the new directory.

   Exit 1 means refused (nothing changed); exit 2 means a step failed.
3. **Compare and clean up.** Compare the printed row counts with the live database. Once done,
   remove the rehearsal database by hand (an owner decision; the scripts never drop anything):
   `docker exec -u postgres rokh-postgres dropdb rokhtiacafe_restore_20261020`.

### 9.2 Restore from an off-site copy

Decrypt and verify the copy on the PC (`sha256sum -c SHA256SUMS`), copy the set directory to
`/backup/rokh/`, then follow §9.1.

### 9.3 Switching production to a restored database (owner decision; loses newer orders)

1. `systemctl stop rokh`.
2. In `/etc/rokh/rokh.env` and `/etc/rokh/migrate.env`, change only the database name to the
   restored one.
3. Uploads: `mv /var/lib/rokh/uploads /var/lib/rokh/uploads.before-restore-$(date +%F)`, then
   `mv /var/lib/rokh/uploads.restore-… /var/lib/rokh/uploads` (renames only; nothing is deleted).
4. `preflight.sh`, `systemctl start rokh`, then verify (§7).

## 10. Upgrade and rollback

**Upgrade:**
1. Back up (§8) and copy the set off the server.
2. `verify-release.sh`.
3. `cp -a /opt/rokh/bin/rokh /opt/rokh/bin/rokh.prev`, then install the new binary.
4. `migrate.sh up`, then `preflight.sh`.
5. `systemctl restart rokh`, then verify (§7).

Migrations are forward-only and stay compatible with the previous binary for one release
(docs/ARCHITECTURE.md §4).

**Rollback, binary only:**
`mv /opt/rokh/bin/rokh /opt/rokh/bin/rokh.bad && mv /opt/rokh/bin/rokh.prev /opt/rokh/bin/rokh && systemctl restart rokh`,
then verify. After a migration the previous binary still works with the new schema for one
release, so this is the normal rollback. Restoring the pre-upgrade backup (§9.3) is only for
wrong data, and it loses orders taken since the backup: an owner decision.

**Rollback, proxy:** §5.2 step 5.

**Rollback, everything:** stop and disable `rokh`, remove the proxy include (§5.2), and stop the
container (`docker compose stop`) without removing it or its volume, so nothing is lost.

## 11. Monitoring and troubleshooting

- **Daily:** the backup log (`journalctl -t rokh-backup`) ends with `backup ok`;
  `systemctl is-active rokh`; `curl -fsS http://127.0.0.1:18080/readyz`.
- **Monthly:** `df -h / /backup`, `docker system df`, and the size of the newest `db.dump`.
- **Logs.** Production logs are JSON lines with a request id. Passwords, tokens, cookies and push
  endpoints are never logged.
- **`preflight.sh` fails:** each `FAIL` line names the setting or file.
- **`readyz` returns 503:** the container is down (`docker compose ps`) or the credentials are wrong.
- **Push alerts don't arrive:** check `web push` in `config check`; outbound HTTPS to the push
  services must be allowed. The dashboard low-stock list works regardless.

## 12. Tests for these scripts

`deploy/test/` runs everything above against throwaway PostgreSQL containers only (CI workflow
`deploy-tests`):
- `templates.sh`: static checks, shellcheck, systemd-analyze, compose config;
- `in-docker.sh release`: a fresh cluster, host client tools, a real `/backup` mount;
- `compose.sh release`: a disposable copy of `postgres/compose.yaml`, docker client.
