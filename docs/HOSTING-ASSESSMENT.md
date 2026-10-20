# Hosting assessment and release gate — rokh.tiacafe.ir

**Status: production is NO-GO.** The hosting assessment (T0) is complete enough to choose the
target (§2), and the deployment templates are hardened for it (T14). Deployment still needs the
open risks in §3 resolved or accepted, the prerequisites in §4, the owner's approval for each
procedure in §5, and a signed release gate (§6). Merging T14 does **not** authorise deployment.

Runbook: [deploy/README.md](../deploy/README.md). Nothing in this document was obtained by
changing the server: Phase A used public lookups from the owner's PC; the server facts below were
gathered by the owner, read-only.

## 1. Verified findings

| # | Area | Finding | Source |
|---|---|---|---|
| F1 | DNS / CDN | `rokh.tiacafe.ir` (and the apex) resolve to MizbanCloud edges 185.226.140.40 and 185.226.142.42 (reverse DNS `*.cdn-by.mizbancloud.com`), nameservers `d.dns.mizbancdn.com/.net`. Visitors never reach the origin directly through DNS. | Phase A, 2026-10-20 |
| F2 | Edge TLS | Let's Encrypt `CN=tiacafe.ir`, valid 2026-09-28 → 2026-12-27, strict hostname check for `rokh.tiacafe.ir` passes. Response `Server: MizbanCloud`, `Mc-Cache-Status: BYPASS`. | Phase A |
| F3 | Origin | 62.204.61.18, AlmaLinux 8.10 x86_64, cPanel/WHM, LiteSpeed Enterprise 6.3.4. Origin TLS: valid Let's Encrypt certificate. | Owner, read-only |
| F4 | Shared server | The server hosts several unrelated production applications. | Owner |
| F5 | Reverse proxy | A cPanel userdata include with Apache `ProxyPass` works through LiteSpeed for another domain on this server. | Owner |
| F6 | Client IP at LiteSpeed | `Use Client IP in Header = Trusted IP Only`; Access Control `ALL,127.0.0.1T` (only loopback is trusted to supply a client IP). | Owner, WHM view |
| F7 | CDN headers | MizbanCloud reports sending `X-Forwarded-For`, `MC_REAL_IP` and `X-Real-IP` to the origin. | MizbanCloud |
| F8 | Disk | Root ~212 GB, ~90 % used, ~22 GB free. `/backup` is a separate mount with ~228 GB free. | Owner |
| F9 | Database | A dedicated PostgreSQL 16 in Docker, not shared with other applications, is the chosen database (template: `deploy/postgres/compose.yaml`). | Owner decision |
| F10 | Application port | `127.0.0.1:18080` is the candidate port for rokh. Free status must be re-checked at install (`ss -ltn`). | Owner |
| F11 | App behaviour (code) | `rokh` takes the client IP from `X-Forwarded-For` only, and only when the direct peer is in `TRUSTED_PROXIES`, using the right-most untrusted entry. It ignores `MC_REAL_IP` and `X-Real-IP` (`internal/httpx/clientip.go`; chain cases in `clientip_cdn_test.go`). | Code + tests |
| F12 | App behaviour (code) | Cache headers: hashed assets `public, max-age=31536000, immutable`; HTML, service workers and manifests `no-cache`; public menu `no-cache` + ETag; order tracking `no-store`; product images long-cached. **Other `/api/v1` responses (including authenticated admin data) send no `Cache-Control`.** | Code |
| F13 | App behaviour (code) | Upload limit 3 MB (+64 KB multipart); server timeouts: header 5 s, read 15 s, write 30 s, idle 60 s. Production refuses a non-loopback `HTTP_ADDR`, missing `TRUSTED_PROXIES`, a relative `UPLOAD_DIR`, the owner/superuser role at runtime, and a missing `APP_SECRET_KEY`. | Code + `deploy/test` |

Not verified (still open): items in §3, and the T0 Phase D checks (mount options, outbound HTTPS
to the push services) that were never run.

## 2. Proposed deployment decisions (D1 = cPanel host)

| # | Decision | Why |
|---|---|---|
| P1 | Run on the existing cPanel/WHM host, not a VPS. | Root/WHM access gives systemd and Docker; F5 proves the proxy mechanism. |
| P2 | `rokh` as a systemd service (`deploy/systemd/rokh.service`), dedicated system user `rokh`, layout under `/opt/rokh`, `/etc/rokh`, `/var/lib/rokh`. | Supervised restarts, hardening, no cPanel-account coupling. |
| P3 | PostgreSQL 16 in its own Docker Compose project (`rokh`), published on `127.0.0.1:15432` only, roles `rokh_owner` / `rokh_app` from the shared `roles.sql`. | Dedicated instance (F9); no public port; same role model as CI. |
| P4 | LiteSpeed reaches rokh through two domain-specific userdata includes (`deploy/litespeed/`); plain HTTP only redirects. | No global configuration changes (F4). |
| P5 | Start with `TRUSTED_PROXIES=127.0.0.1`. Add the MizbanCloud edge ranges only after R1 is verified. | Safe default: never trusts client-supplied headers; worst case is shared rate-limit buckets, not spoofing. |
| P6 | Backups as root to `/backup/rokh` (required mount), 14 days / at least 7 sets, plus encrypted off-site copies pulled by the owner. | Root disk nearly full (F8); Docker volumes are not in cPanel backups. |
| P7 | The CDN must bypass caching for `/api/*`, `/admin/*` and HTML (cache only `/assets/*` and `/media/*`). | Mitigates R2 without an application change. |

## 3. Unresolved risks and blockers

| # | Risk | Impact | Status / mitigation |
|---|---|---|---|
| **R1** | **Client IP chain unverified.** Whether LiteSpeed appends to the CDN's `X-Forwarded-For` or replaces it, and the official MizbanCloud edge IP ranges, are unknown. | With loopback-only trust, every visitor behind one edge shares a rate-limit bucket (orders 30/10 min, login 20/15 min per IP) — busy hours could hit `429`. If LiteSpeed replaced the header, trusting the edges would make everyone `127.0.0.1`. | **Blocker for go-live** until measured (deploy/README.md §5.3) and `TRUSTED_PROXIES` decided. If LiteSpeed does not append, the fix needs either a LiteSpeed trusted-IP change for the edge ranges (global, owner/host decision) or a separately approved app change (read a configured header); neither is part of T14. |
| **R2** | **CDN caching of API responses.** Most `/api/v1` responses have no `Cache-Control`; order tracking uses one URL for all customers (token in a header). | A CDN rule that caches them would serve one user's data to another. | **Blocker** until the CDN bypass rule (P7) is confirmed with `Mc-Cache-Status` on `/api/v1/public/order`, `/api/v1/admin/*` and `/admin/`. Recommended follow-up (needs approval, out of T14 scope): default `Cache-Control: no-store` for `/api/v1` responses in the app. |
| R3 | Direct-to-origin requests bypass the CDN (and its WAF). | Rate limits still see the attacker's own IP (F11, tested); only the CDN's filtering is skipped. | Accept, or restrict origin 443 to MizbanCloud ranges in the firewall (affects other sites: owner/host decision). |
| R4 | Root disk ~90 % used. | Docker image, volume growth, journal and Docker logs could fill it and stop every site on the server. | Docker log rotation in the template; backups on `/backup`; monthly checks (README §11); gate item G1. |
| R5 | LiteSpeed support for `ProxyPreserveHost`, `ProxyPass … !`, `timeout=` and `RequestHeader unset` in userdata includes, and include persistence after cPanel rebuilds, is unconfirmed. | Wrong Host, broken ACME renewals, cut-off uploads. | Verify in the approved window (README §5.3). |
| R6 | Docker iptables rules vs the host firewall (e.g. CSF restarts). | Lost loopback port mapping → `readyz` 503. | Check after firewall restarts; README §4.1. |
| R7 | CDN → origin TLS mode (full/strict) not confirmed in the MizbanCloud panel. | Plain-HTTP or unverified hop between CDN and origin. | Gate item G2. |
| R8 | Outbound HTTPS to push services not tested from the server (Phase D6). | Push alerts may not arrive (the dashboard list still works). | Test during the approved window; push may stay disabled. |
| R9 | Backups live on the same server until copied. | Server loss loses them. | Off-site procedure (README §8.3) is manual; owner must schedule it. |
| R10 | In-memory rate limits and kill switch reset on restart (O4, O6). | Known, accepted. | Re-apply kill switch after restarts. |
| R11 | `rokh` loads `./.env` and `../.env` from its working directory. | A stray file could add settings. | Layout avoids it; `preflight.sh` checks `/.env`. Not changed in code (out of scope). |

## 4. Deployment prerequisites

1. T14 merged after review; CI green (backend, frontend, release, deploy-tests).
2. A release build whose SHA-256 matches the CI `release` job (`verify-release.sh`).
3. R1 and R2 resolved, or explicitly accepted by the owner in writing.
4. MizbanCloud: cache bypass rule for `/api/*`, `/admin/*`, HTML; TLS mode full/strict; upload
   limit ≥ 4 MB; origin timeout ≥ 30 s.
5. Free: port `127.0.0.1:18080` and `127.0.0.1:15432`; ≥ 5 GB on `/` after installation.
6. Docker Engine and Compose v2 on the server (owner/host confirms; installing them is a server
   change needing approval).
7. An off-site destination and an encryption key for backups (owner).
8. A maintenance window agreed with the owner (other sites share the web server).

## 5. Procedures that need the owner's approval

Each of these is a separate, explicit approval in chat; none is implied by merging T14.

| Procedure | Touches | Runbook |
|---|---|---|
| Create user `rokh`, directories, `/etc/rokh` files | Server filesystem | README §3 |
| Install Docker / pull and start `rokh-postgres` | Docker, iptables, root disk | README §3, §4 |
| Install the binary, run migrations, create the first admin | Database | README §3 |
| Install and enable `rokh.service` | systemd | README §6 |
| Add the two LiteSpeed userdata includes; graceful restart | Web server shared with other sites | README §5.2 |
| MizbanCloud cache/TLS/limit settings; any DNS change | CDN | §4 item 4 |
| Firewall changes (only if R3 is to be mitigated) | Whole server | §3 R3 |
| Root cron entry for backups | Server | README §8.1 |
| Restore into production, or any restore rehearsal on the server | Database, uploads | README §9 |
| Upgrades and rollbacks | Service, database | README §10 |

## 6. Release gate (Go/No-Go)

Production is **NO-GO** until every line is checked with evidence and the owner signs below.
"Evidence" is the command output or screenshot, redacted (no passwords, keys or private paths).

| # | Gate | Go when | Evidence |
|---|---|---|---|
| G1 | Main disk capacity | ≥ 5 GB free on `/` after install; `/backup` mounted with ≥ 50 GB free | `df -h / /backup` |
| G2 | Origin TLS | Origin certificate valid for `rokh.tiacafe.ir`, auto-renewing; CDN → origin in full/strict mode | cert details; MizbanCloud panel |
| G3 | CDN and trusted client IP | R1 measured; `TRUSTED_PROXIES` decided; two different visitors get different rate-limit buckets; `/api/*`, `/admin/*`, HTML show `Mc-Cache-Status` ≠ `HIT` | README §5.3, §7 |
| G4 | Reverse proxy | Includes active only for `rokh.tiacafe.ir`; `/.well-known/` served from docroot; HTTP → HTTPS redirect; 3 MB upload works; security headers present once; other sites unaffected | curl output |
| G5 | Go service | `systemctl is-active rokh`; survives `systemctl restart`; graceful stop logs `server stopped`; restarts after `kill -9`; runs as `rokh` | journal |
| G6 | PostgreSQL roles and privileges | Port bound to 127.0.0.1 only; `rokh_app` cannot DELETE financial tables or run DDL; neither role superuser/CREATEDB/CREATEROLE | `preflight.sh`, psql |
| G7 | Migration status | `migrate.sh status` shows `0 pending`; `rokh check` ok | output |
| G8 | Backup verification | `backup.sh` exit 0 on the server; set verified with `sha256sum -c`; cron entry active; one off-site encrypted copy made | log |
| G9 | Restore rehearsal | Restore of the server's own backup into a `*_restore_*` database: exit 0, row counts match | `restore.sh` output |
| G10 | Rollback rehearsal | Binary rollback (`rokh.prev`) and proxy include rollback performed once | notes |
| G11 | Application smoke tests | `/healthz`, `/readyz`, `/menu` 200, `/admin/` login, place and complete a test order, image upload | notes |
| G12 | Browser and mobile acceptance | docs/ACCEPTANCE.md on a real phone (Android Chrome, iOS Safari) at 375/390 px; Persian RTL, Jalali dates, tomans | screenshots |
| G13 | Monitoring and handover | Daily/monthly checks (README §11) assigned; owner knows restart, rollback and restore steps; credentials stored by the owner only | sign-off |

Owner decision: ☐ GO ☐ NO-GO — name, date: ______________________

## 7. T14 validation record

Tests of the deployment scripts and templates run only against throwaway PostgreSQL containers
(`deploy/test/`). Results are recorded in the T14 pull request, separated into tests run locally,
tests run in CI and tests not run. No production system was contacted.

## Appendix — T0 checklist outcome

| T0 item | Outcome |
|---|---|
| 1 DNS | Points to MizbanCloud edges, not the origin (F1). |
| 1b TLS | Valid at the edge (F2) and at the origin (F3). |
| 2 Panel features | WHM/root access available (F3). |
| 3 Long-running process | systemd as root (P2). |
| 4 Reverse proxy | Userdata include + ProxyPass works for another domain (F5); this vhost: R5. |
| 5 / 5b PostgreSQL roles | Dedicated Docker PostgreSQL 16 with exact role names (F9, P3). |
| 6 Executables | Not checked in Phase D; binary runs from `/opt/rokh`, not `/home` — confirm mount options at install. |
| 7a Disk / backups | F8; Docker volume not in cPanel backups (P6). |
| 7b Outbound push | Not tested (R8). |
| 8 Other services | Present (F4): no shared configuration changes. |
