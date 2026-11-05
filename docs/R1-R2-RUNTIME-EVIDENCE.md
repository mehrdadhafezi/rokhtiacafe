# R1/R2 runtime evidence (procedures and record)

**Status (2026-11-05): no runtime evidence collected.**
- **R1-B and R2-B are NOT TESTED.** They need owner authorization (docs/R1-R2-PRODUCTION-READINESS.md §0).
- **Code-level work (R1-A, R2-A) is done and tested.** See §1.
- Every runtime field below stays `NOT TESTED` until an approved run fills it with real output. A simulation never fills it.

## 1. Code-level evidence (R1-A, R2-A)

| Threat | Test | Result |
|---|---|---|
| Forged leftmost / victim + trusted edge / duplicate lines / malformed / IPv6, via the CDN | `TestR1ProxyChainIdentity` (real TCP chain, six LiteSpeed modes × two trust settings) | append and append-line: honest identity in every case. replace and strip: never forged. **preserve and prepend: forged identity wins** (demonstrated) |
| Direct-to-origin with forged headers | same, and `TestR1ProxyChainRateLimits` | append: the attacker's own address. preserve/prepend: **bypass, 10/10 through a 3/h limit** |
| Rate-limit bypass by rotating forged headers | `TestR1ProxyChainRateLimits` | append: exactly the limit |
| IPv6 rotation inside one /64 | `TestR1ProxyChainRateLimits/…IPv6…`, `TestRateKey` | **Defect found and fixed:** 10/10 got through before, 3/3 after (`httpx.RateKey`) |
| Shared buckets | same | With edges trusted: per visitor, stable across edge rotation. Today (loopback only): **per edge**, so visitors behind one edge share a bucket. strip mode: one global bucket |
| Edge missing from the published list | `TestR1ProxyChainIdentity` | Treated as the client (shared bucket), never as a forged address |
| Fallback anchor, hop budget, multi-line order, telemetry | `clientip_*_test.go` (existing) | pass |
| Private responses in a shared cache | `TestSharedCacheNeverServesPrivateResponsesAcrossIdentities` | No cross-identity response, no cache hit |
| The same before R2 | `TestSharedCacheLeaksWithoutNoStoreDefault` | **Defect found and fixed:** five leaks (ADMIN identity, finance summary, staff users and audit logs to OPERATOR; order list to anonymous) |
| Every API response no-store | `TestEveryAPIResponseIsNoStore` (68 routes × anonymous/customer/ADMIN) | pass; the menu stays `no-cache` + `ETag` |
| A cache-everything edge | `TestSharedCacheEverythingPolicyIsOutOfAppControl` | Leaks by design: needs the CDN rule (R2-B) |

## 2. R1-B: LiteSpeed and CDN forwarding (runtime) — NOT TESTED

**Approval:** owner, in writing. The approval names:
- the environment: a staging vhost on a server running the same LiteSpeed version and include as production is preferred; the production origin only if staging is impossible;
- the time window and the operator;
- for each address range used, whether it may be allowlisted.

**Boundaries:**
- No change to the production vhost, the WHM/LiteSpeed global settings, DNS, the CDN, the firewall or `TRUSTED_PROXIES`.
- The diagnostic upstream replaces rokh only on the staging vhost.
- No cookies, tokens or user data are sent.
- Forged values come from the documentation ranges only.

**Steps:**
1. Record:
   - the LiteSpeed version (WHM → LiteSpeed Web Server, or `/usr/local/lsws/bin/lshttpd -v`);
   - the SHA-256 of the installed include, compared with `deploy/litespeed/rokh-proxy.ssl.conf.example`;
   - the date and source of the MizbanCloud edge list (`https://mizbancloud.com/ips.txt`), with its SHA-256.
2. On the staging host, run `xffprobe -listen 127.0.0.1:18081 -trusted 127.0.0.1/32`. Build it with `cd backend && GOOS=linux go build -o xffprobe ./tools/xffprobe`. Point the staging vhost's proxy context at `127.0.0.1:18081`.
3. From a test machine, run:
   `CDN_URL=https://<staging-host>/ ORIGIN_URL=https://<origin-ip>/ ORIGIN_HOST=<staging-host> deploy/r1/probe-matrix.sh > r1-matrix-loopback.jsonl`
4. Restart the probe with `-trusted 127.0.0.1/32,<edge list>` and repeat step 3 into `r1-matrix-edges.jsonl`.
5. Stop the probe and restore the staging vhost.

**Pass criteria.** Every row must hold, otherwise **STOP: R1 FAIL**. No edge ranges are added, and a proxy-boundary rewrite needs design first.
- **Every `origin/*` row:**
  - the verdict is `APPENDED_AFTER_CLIENT_ENTRIES`, or `ADDED` when no header was sent;
  - `rokh_resolves` is the same `peer:<hash>` as the `origin/no-header` row (the tester's own address);
  - it is never `marker:*`.
- **Every `cdn/*` row:**
  - never a `marker:*` in `rokh_resolves`;
  - with edges trusted, `rokh_resolves` is the tester's address (one hash, the same on all rows);
  - loopback-only, it is an edge (`peer:` hashes that differ from the tester's).
- **Line structure:** `xff_lines` shows whether LiteSpeed appends on the same line or a new one. Either is accepted (`TestR1MultiLineOrdering`).
- **Alternative headers:** `other_ip_headers_present` is absent on both `*/alt-headers` rows. Otherwise the `RequestHeader unset` lines are not honoured: record it. rokh still ignores those headers.
- **Telemetry:** after enabling the edges in production (a separate approved change), the `client ip summary` window shows `fallback_*` and loopback below 1 % (deploy/README.md §11).

**Direct-origin exposure:** record whether `ORIGIN_URL` answers at all. If it does, the owner decides whether to restrict the origin to the CDN; any such change is owner-approved and must not affect other sites on the server.

| Evidence field | Value |
|---|---|
| LiteSpeed version / include SHA-256 | NOT TESTED |
| Edge list source, date, SHA-256 | NOT TESTED |
| `origin/*` verdicts (loopback-only, with edges) | NOT TESTED |
| `cdn/*` resolved identity | NOT TESTED |
| Alternative headers stripped | NOT TESTED |
| Direct origin reachable | NOT TESTED |
| Telemetry window after enabling edges | NOT TESTED |
| Operator, approver, date | NOT TESTED |

## 3. R2-B: CDN cache bypass (runtime) — NOT TESTED

**Approval:** owner, in writing, for:
- reading the MizbanCloud cache configuration (and changing it, if a bypass rule is missing);
- running the matrix.

**Required CDN rule:**
- **Bypass the cache for:** `/api/*`, `/admin/*`, `/menu*`, `/` and every HTML response.
- **Cache allowed for:** `/assets/*` (hashed, immutable) and `/media/*` only.
- **TLS to the origin:** full/strict.

**Steps:**
1. Export or screenshot the cache rules (no credentials).
2. Run `CDN_URL=https://<host> deploy/r2/cache-matrix.sh > r2-matrix.txt`.
3. Optionally repeat with staging test accounts in `CUSTOMER_A_COOKIE`, `CUSTOMER_B_COOKIE`, `ADMIN_COOKIE` and `OPERATOR_COOKIE`. They are never printed, and never come from real users.

**Pass criteria:**
- **Private paths:** on the second request, every `/api/*`, `/admin/*` and HTML row shows the edge bypass status (`Mc-Cache-Status` BYPASS/MISS or the equivalent) and no `Age` above 0.
- **Origin headers:** private rows also show `Cache-Control: no-store` from the origin.
- **Hashed assets:** may HIT.
- **Signed-in rows:** each identity's second request is still a bypass.

| Evidence field | Value |
|---|---|
| Cache rule export | NOT TESTED |
| Anonymous private paths: second request status | NOT TESTED |
| Tracking with two tokens | NOT TESTED |
| Customer A/B, ADMIN/OPERATOR rows | NOT TESTED |
| HTML / service worker | NOT TESTED |
| Hashed asset caching | NOT TESTED |
| TLS mode to origin | NOT TESTED |
| Operator, approver, date | NOT TESTED |

## 4. Real-data restore and actual-binary rollback — NOT RUN

**Approval:** owner, in writing. The approval covers:
- taking a backup with `deploy/scripts/backup.sh` (already scheduled on the server);
- transferring a copy, encrypted, to an isolated staging host;
- copying `/opt/rokh/bin/rokh` with its SHA-256.

No production database is touched beyond the normal backup.

**Steps (staging only):**
1. Prepare the restore target and restore the copy:
   - `restore-target.sql` creates `rokhtiacafe_restore_<date>`;
   - `restore.sh` then restores into it with the **new** binary, which runs migrate up (`00008`) and `rokh check`.
2. Before and after the migration, run `backend/tools/c6 snapshot` (row digests and sums only, no personal data). Then run `c6 compare -mode restore -new customers,customer_sessions,customer_events,customer_order_links`.
3. Rollback rehearsal against the binary actually deployed:
   - put the copied binary with its BUILDINFO in a directory;
   - run `deploy/test/c6/in-docker.sh <new release> <that directory> <c6 tool>` (synthetic data), then the HTTP phases against the restored copy.

**Pass:** every legacy row is identical after the migration, `rokh check` passes with both binaries, and the C6 phases pass.

| Evidence field | Value |
|---|---|
| Backup set name, SHA256SUMS verified | NOT RUN |
| Restore + migration: legacy rows identical | NOT RUN |
| Deployed binary SHA-256 / version | NOT RUN |
| Rollback / roll-forward with that binary | NOT RUN |
