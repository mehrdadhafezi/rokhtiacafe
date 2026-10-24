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
                                                   │ TCP 127.0.0.1:ROKH_PG_PORT (15433 proposed)
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
| `/opt/rokh/postgres/.env` | root:root 600 | Compose variables `ROKH_PG_PORT`, `ROKH_PG_IMAGE`; no secrets (template `env/compose.env.example`) |
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

1. **Capacity (gate G1).** The root disk is ~90 % used (~22 GB free) and shared with other
   production sites. Installation starts only after the disk-capacity acceptance in
   [HOSTING-ASSESSMENT.md §6.1](../docs/HOSTING-ASSESSMENT.md#61-disk-capacity-acceptance-g1) is
   signed off (baseline with `scripts/capacity-report.sh`, host confirmation). Backups go to
   `/backup`, never to the root disk.
2. **System user and directories:**
   ```sh
   useradd --system --home-dir /var/lib/rokh --shell /sbin/nologin rokh
   install -d -o root -g rokh -m 750 /opt/rokh /opt/rokh/bin /var/lib/rokh
   install -d -o root -g root -m 750 /opt/rokh/scripts /opt/rokh/postgres /opt/rokh/postgres/initdb
   install -d -o rokh -g rokh -m 750 /var/lib/rokh/uploads
   install -d -o root -g root -m 755 /etc/rokh
   install -d -o root -g root -m 700 /backup/rokh
   install -d -o root -g root -m 700 /var/lib/rokh-backup   # backup run records (§8.1)
   ```
3. **Settings.** Copy `production.env.example` (→ `rokh.env`), `migrate.env.example`,
   `postgres.env.example` and `ops.env.example` from `deploy/env/` to `/etc/rokh/`, and
   `compose.env.example` to `/opt/rokh/postgres/.env`, with the owners and modes from §2, then fill
   them in:
   - `ROKH_PG_PORT` in `/opt/rokh/postgres/.env` (15433 is only proposed: owner decision), and the
     same port in `DATABASE_URL` and `MIGRATION_DATABASE_URL`;
   - three new passwords (`openssl rand -hex 24`) in `postgres.env`; the owner and app passwords
     also go into `migrate.env` and `rokh.env`;
   - `APP_SECRET_KEY` from `rokh secret gen`; optionally `VAPID_*` from `rokh vapid gen`.
4. **PostgreSQL** (§4): right before the first start, check that the chosen port is free
   (`ss -ltnH 'sport = :15433'` prints nothing, with your `ROKH_PG_PORT`), pin the image (§4.1),
   and check `cd /opt/rokh/postgres && docker compose config` shows `127.0.0.1:<port>` and the
   `@sha256:` image. Then `docker compose up -d`; `docker compose ps` must show `healthy`.
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

- **No public port.** The container port is published on `127.0.0.1:ROKH_PG_PORT` only. The rokh
  service on the host connects to it; nothing outside the host can. Inside the container, socket
  connections use peer authentication and TCP uses `scram-sha-256`; there are no `trust` rules.
- **Roles** (docs/DATABASE.md §6.1). On first start, `initdb/10-rokh-init.sh` runs the same
  `roles.sql` as development and CI, reading the passwords from the container environment with
  psql's `\getenv` (never as command-line arguments): `rokh_owner` owns the database and schema `rokh`, and
  `rokh_app` gets `CONNECT`. The migrations grant `rokh_app` its DML-only privileges. Neither role
  is superuser, `CREATEDB` or `CREATEROLE`. The container superuser `postgres` is used only to
  create restore targets (§9) and for upgrades.
- **Compose variables.** `compose.yaml` has no default for `ROKH_PG_PORT` or `ROKH_PG_IMAGE`:
  without them Compose refuses to start, so it can never fall back to a port another service uses.
  Both live in `/opt/rokh/postgres/.env` (root:root 600, template `env/compose.env.example`), which
  Compose reads from the project directory whenever it runs on `/opt/rokh/postgres/compose.yaml`.
  A variable **exported in the shell overrides that file**: never export `ROKH_PG_*`, and run
  `docker compose config` (port and image) before every `up`. `preflight.sh` checks that the port
  equals the one in both database URLs and that the image is pinned.
- **Pin the image** (reproducible): before the first start and for every minor update,
  1. `docker pull postgres:16-bookworm`
  2. `docker image inspect --format '{{index .RepoDigests 0}}' postgres:16-bookworm` prints
     `postgres@sha256:<64 hex>`;
  3. write `ROKH_PG_IMAGE=postgres:16-bookworm@sha256:<64 hex>` in `/opt/rokh/postgres/.env` and
     record the digest and date in the operations log;
  4. `docker compose config --images` must print exactly that reference.
  With a digest, `docker compose pull` and `up` always use that exact image; changing it is an
  explicit edit of `.env`.
- **Storage.** The volume lives on the root disk. The café database is small (expected well under
  1 GB in the first year), but check `docker system df` and `df -h /` monthly (§11).
- **Docker and the host firewall.** Docker manages its own iptables rules. If the server firewall
  (e.g. CSF) restarts, confirm that `docker compose ps` is still healthy and `/readyz` still
  answers. Restart the container if the rules were flushed.

### 4.2 Minor updates (16.x → 16.y)

Backup (§8) → pin the new digest (§4.1, steps 1–4) → `docker compose up -d` → health check →
`/readyz` → `rokh check`. The data stays in the volume. To go back, restore the previous
`ROKH_PG_IMAGE` line and `docker compose up -d` again (same major version only).

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
- **Forwarding headers** (risk R1). Capture the `X-Forwarded-For` header *lines* that reach rokh
  (e.g. a one-off loopback listener in place of rokh during the window) for:
  1. a normal request through the CDN, from a phone whose public IP you know;
  2. the same request with a forged `X-Forwarded-For: 198.51.100.77` added by the client;
  3. a request sent straight to the origin (`curl --resolve rokh.tiacafe.ir:443:62.204.61.18`)
     with a forged `X-Forwarded-For: 198.51.100.77, <an edge IP>`.

  Go only if, in every case, the **right-most entry of all header lines combined in order** is the
  address LiteSpeed actually saw (the edge in 1–2, your own IP in 3) and the forged value is never
  right-most. LiteSpeed may write its entry on the same line or as a new *last* line (rokh combines
  lines). Writing it anywhere else, or passing the header through, keeps client-IP handling
  blocked (**STOP**). If the CDN's part contains no visitor (only edges), keep
  `TRUSTED_PROXIES=127.0.0.1` and see R1. Full procedure: R1-B2 runtime acceptance; none of it runs
  before the owner approves it and gate G1 (disk) passes.
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

### 8.1 Scheduling and alerts

Schedule backups **only through `backup-cron.sh`**, never `backup.sh` in a pipeline. cron runs
`/bin/sh`, where `backup.sh | logger` reports `logger`'s exit status, so a failed backup would
look like a success. Root's crontab (`crontab -e` as root):

```
# Replace with the real address that receives backup alerts.
MAILTO=owner@example.com
30 3 * * * /opt/rokh/scripts/backup-cron.sh
0  9 * * * /opt/rokh/scripts/backup-status.sh
```

- **`backup-cron.sh`** runs `backup.sh` without a pipe, captures its real exit code and:
  - records it in `/var/lib/rokh-backup/last-run` (`result=ok|refused|failed|retention-warning`,
    `exit=`, start and finish times), and the finish time in `last-success` for exit 0 or 3;
  - sends the output to the journal (`journalctl -t rokh-backup`): priority `err` unless the
    exit code is 0;
  - prints nothing on success (no mail), and an `ALERT` with the result, the next step and the
    last lines of output for **any** other exit code, which cron mails to `MAILTO`;
  - exits with `backup.sh`'s own code. Exit 4 means the run could not be recorded.
- **`backup-status.sh`** is the fail-closed signal. It is silent with exit 0 only when the last
  run succeeded **and** the last success is younger than `BACKUP_MAX_AGE_HOURS` (26). A missing
  or unreadable record, a refused or failed run, a stale success (e.g. cron stopped, or the
  wrapper never ran) all print an `ALERT` and exit 1; a retention warning prints a `WARNING` and
  exits 3. Because it also alerts when nothing ran at all, a silent mail system is the only gap:
  check it by hand (`backup-status.sh -v`) during the daily check (§11).
- **Mail delivery must be proven** before G8: with owner approval, run
  `BACKUP_MAX_AGE_HOURS=0 /opt/rokh/scripts/backup-status.sh` from cron once (it always alerts)
  and confirm the mail arrives.

| `backup.sh` exit | Meaning | Alert | Recorded success |
|---|---|---|---|
| 0 | backup ok | none | yes |
| 1 | refused: precondition failed, nothing written | ALERT | no |
| 2 | failed: nothing published | ALERT | no |
| 3 | backup ok, retention kept something it could not remove | ALERT (mail), WARNING (status) | yes |
| other / 4 | unexpected / could not record | ALERT | no |

### 8.2 What `backup.sh` does

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
- less than `BACKUP_MIN_FREE_MB` is free. `/backup` is shared with the server's other backups,
  so this is a reserve that protects them: the template sets 84480 MB (about 10 % of the 825 GB
  filesystem observed in LRA-2). Recompute it if the mount changes, and use the host's alert
  threshold if it is stricter. When the reserve is reached Rokh's backup is refused and alerts
  (exit 1); it never deletes anything to make room;
- `UPLOAD_DIR` is missing;
- another backup holds the lock.

**Failed (exit 2, nothing published)** when the dump fails, the dump is unreadable
(`pg_restore --list`), the dump lacks the Rokh schema, archiving fails, or a checksum fails.

**Exit 3** means the backup is fine, but retention kept a set it could not safely remove.

### 8.3 Retention

- Only real directories (not symlinks) directly in `BACKUP_DIR`, named exactly
  `rokh-YYYYmmdd-HHMMSS`, are candidates.
- A set is **complete** when it holds exactly `db.dump`, `uploads.tar.gz`, `INFO` and
  `SHA256SUMS` as regular files. Only complete sets count towards `BACKUP_KEEP_MIN` (7), so
  broken or partial sets can never push good ones out.
- Complete sets beyond the newest `BACKUP_KEEP_MIN` and older than `BACKUP_KEEP_DAYS` (14) are
  removed.
- Incomplete sets, sets with extra files and `.partial` directories from interrupted runs are
  **never** removed; they are reported and the run exits 3 so the log shows it.
- Nothing else in `BACKUP_DIR`, and nothing outside it (other applications' backups elsewhere on
  `/backup`), is ever touched. Retention runs only after a successful backup.

Docker volumes are **not** covered by cPanel/JetBackup account backups: these sets are the only
database backups.

**Local sets are not a backup strategy on their own.** `/backup` is a disk in the same server:
a server loss, compromise, ransomware or a mistaken command as root can take the live data and
every local set together. Local retention only gives quick restores of recent days; protection
against losing the server comes **only** from the off-site copies in §8.4. Without a recent,
verified off-site copy, gate G8 is not met.

### 8.4 Off-site copy (required)

The backup disk is inside the same server, so copy every set off it, encrypted at rest
(docs/SECURITY.md §9), at least weekly and before every upgrade.

**The encryption tool, the key custody and the pull account are an open owner decision:**
[docs/BACKUP-OFFSITE-DECISION.md](../docs/BACKUP-OFFSITE-DECISION.md). No tool is selected, no key
exists and nothing is installed until the owner records that decision; until then gate G8 cannot be
met. The tool-independent steps are:

1. Pull the set from the owner's PC (the server never holds credentials for the destination),
   with the account chosen in the decision record.
2. Verify it: `cd rokh-YYYYmmdd-HHMMSS && sha256sum -c SHA256SUMS`.
3. Encrypt it with the recorded tool and public key, store it in the recorded places, then delete
   the unencrypted copy from the PC.
4. Prove it can be read back: decrypt it on a second machine and run `sha256sum -c SHA256SUMS`.
5. Record the date, the set name and the SHA-256 of the encrypted file in the operations log.

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

   Exit 1 means refused (nothing changed). Exit 2 means a step failed.

   **Status marker.** The target database carries its status as a comment: `rokh restore IN
   PROGRESS` while running, then either `rokh restore VALIDATED: …` (only after migrations,
   `rokh check` and the uploads all succeeded, printed as `RESTORE VALIDATED`) or `rokh restore
   FAILED at <step>: NOT a validated recovery`. If `pg_restore` fails, its single transaction
   leaves no restored data; if a later step fails, the data stays but is marked FAILED and the run
   says `RESTORE FAILED`. Uploads are unpacked into `<RESTORE_UPLOADS_DIR>.partial` and renamed to
   `RESTORE_UPLOADS_DIR` only on success, so that directory exists only for a complete copy.
   Read the marker with:
   `docker exec -u postgres rokh-postgres psql -XAtc "SELECT datname, shobj_description(oid, 'pg_database') FROM pg_database WHERE datname LIKE '%\_restore\_%'"`
3. **Compare and clean up.** Compare the printed row counts with the live database, then remove
   the rehearsal database (§9.4).

### 9.2 Restore from an off-site copy

Decrypt and verify the copy on the PC (`sha256sum -c SHA256SUMS`), copy the set directory to
`/backup/rokh/`, then follow §9.1.

### 9.3 Switching production to a restored database (owner decision; loses newer orders)

0. Confirm the marker (§9.1) of the database you switch to starts with `rokh restore VALIDATED`.
   Never switch to one marked `IN PROGRESS` or `FAILED`, or without a marker.
1. `systemctl stop rokh`.
2. In `/etc/rokh/rokh.env` and `/etc/rokh/migrate.env`, change only the database name to the
   restored one.
3. Uploads: `mv /var/lib/rokh/uploads /var/lib/rokh/uploads.before-restore-$(date +%F)`, then
   `mv /var/lib/rokh/uploads.restore-… /var/lib/rokh/uploads` (renames only; nothing is deleted).
4. `preflight.sh`, `systemctl start rokh`, then verify (§7).

### 9.4 Cleaning up rehearsal and failed restore targets

The scripts never drop or delete anything; cleanup is a manual, owner-approved step. For each
database to remove:
1. Its name contains `_restore_` and it is **not** the name in `DATABASE_URL` or
   `MIGRATION_DATABASE_URL` (`grep -h DATABASE_URL= /etc/rokh/rokh.env /etc/rokh/migrate.env`).
2. Its marker (§9.1) is `FAILED`, `IN PROGRESS` (from a run that is no longer running), or a
   `VALIDATED` rehearsal you have finished with.
3. Nothing is connected to it:
   `docker exec -u postgres rokh-postgres psql -XAtc "SELECT count(*) FROM pg_stat_activity WHERE datname = 'NAME'"` prints 0.
4. Then: `docker exec -u postgres rokh-postgres dropdb NAME` (without `--force`).

For uploads: only directories named `/var/lib/rokh/uploads.restore-*` or
`…uploads.restore-*.partial` that are not the live `UPLOAD_DIR` (check with
`grep UPLOAD_DIR= /etc/rokh/rokh.env` and `readlink -f`) may be removed, with
`rm -r -- /var/lib/rokh/uploads.restore-YYYYMMDD.partial` after a look at their contents. Never
use wildcards in the `rm` command, and never touch `/var/lib/rokh/uploads` itself.

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

- **Automatic:** `backup-cron.sh` mails an `ALERT` for every backup exit other than 0, and
  `backup-status.sh` mails one every morning while the last run failed or the last success is
  older than 26 h (§8.1). Act on any mail the same day.
- **Daily, by hand:** `/opt/rokh/scripts/backup-status.sh -v` prints `backup ok: last success …`
  (anything else is an incident, even without a mail); `systemctl is-active rokh`;
  `curl -fsS http://127.0.0.1:18080/readyz`. Details of a run: `journalctl -t rokh-backup -n 50`.
- **Client IP (risk R1):** `journalctl -u rokh --since today | grep '"client ip summary"'` shows one
  line per 5 minutes with API traffic: counts only (no addresses). Healthy with
  `TRUSTED_PROXIES=127.0.0.1`: `source_forwarded` ≈ `total`, `forwarded_direct` ≈ `total` (each
  visitor appears as its edge), `fallbacks` and `loopback` near 0. Healthy after the edge ranges
  are trusted: `forwarded_direct` near 0 (non-zero = direct-to-origin traffic or an edge missing
  from the list). A **WARN** line (≥ 20 requests and ≥ 1 % fell back or became loopback in one
  interval) is an incident: compare `source_fallback_*` counters, check that LiteSpeed and the
  include are unchanged, and if it started after a `TRUSTED_PROXIES` change, set it back to
  `127.0.0.1` and restart. Limits are never relaxed to compensate.
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
