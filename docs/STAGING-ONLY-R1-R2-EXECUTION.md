# Rokh — independent staging-only execution plan

**Authorized scope:** owner approved **independent Staging only**, on 2026-11-05. This approval does **not** authorize production-origin requests, real CDN accounts/traffic, live LiteSpeed/WHM configuration inspection, production network access, real-data backup extraction, live binary extraction or any Production change. Do not interpret access credentials as permission.

**Current state:** PLAN PREPARED — NOT RUN. R1/R2 code simulations are already green in PR #21. This project is for staging-runtime evidence only. Production remains **NO-GO**.

## A. Staging inventory / inputs required

| Input | Required value/evidence | Allowed source | Current |
|---|---|---|---|
| Host | independent VM/container host, OS version, CPU/RAM/disk, SSH operator | new staging resource only | NOT PROVIDED |
| Domain | staging-only hostname and staging-only DNS; **not** production hostname | independent staging DNS | NOT PROVIDED |
| Reverse proxy | separately installed **LiteSpeed** matching target version and vhost rules (license if necessary) | staging-only installation | NOT PROVIDED |
| CDN | staging-only MizbanCloud zone/account mirroring cache/TLS behavior, or explicitly identify simulated cache proxy | staging account only | NOT PROVIDED |
| PostgreSQL | isolated PostgreSQL 16 with unique service/user/schema, no route to production | disposable container/VM | NOT PROVIDED |
| Data | synthetic customer, staff and orders data; no real PII or production backup | C6 fixture scripts | AVAILABLE IN REPO |
| App artifacts | pinned Git commit, reproducible release and checksum; staging-only secrets and ports | current development branch + CI | AVAILABLE IN REPO |
| Testing workstation | curl, bash, openssl, Go/Node/Playwright, Docker, SQL clients | staging or test PC | TO VERIFY |
| Evidence destination | restricted staging directory; log sanitization and cleanup owner | dedicated staging area | NOT PROVIDED |

**Critical distinction:** if LiteSpeed is unavailable, an Nginx/Caddy/mocked proxy is useful for *simulated* regression testing but cannot produce a PASS for **LiteSpeed-specific R1 behavior**. If only a simulated CDN is available, R2-A can pass but **MizbanCloud-specific R2-B remains NOT TESTED**.

## B. Setup and isolation checks (before test traffic)

1. Assign a separate operator and establish staging access. Use generated one-time passwords, distinct cookies and test accounts; do not replicate production secrets.
2. Validate the staging host is not the production host and has **no routing to production DB**, production uploads or internal services. Configure its database to bind to staging loopback/isolated bridge only. Record isolated DB identity, server version, host/port and volumes; redact secrets.
3. Build from a pinned development commit and verify release checksum. Do **not** use a production binary, unless separately approved in a later real-data acceptance stage.
4. Create disposable customer A and B, ADMIN and OPERATOR, guest orders, completed/refunded orders, prepared inventory and cash ledger via existing C6 seed and test harness.
5. Confirm staging reverse-proxy vhost terminates on its dedicated backend port and does not share a server with live domains. Do not use `rokh.tiacafe.ir`, production origin IP or any live backend.
6. Record the staging LiteSpeed build, installed config hash and actual header rules. If target behavior cannot be reproduced, mark evidence **SIMULATION ONLY**.
7. For CDN, establish a separate staging zone that does not share production rules or credentials. Save its cache policies and TLS mode (without account identifiers, API tokens or passwords).

**Stop condition:** any ambiguity about target isolation, routing to live environments, unexpected data or shared infrastructure means STOP and report BLOCKED.

## C. R1 test matrix (staging only)

The repository already provides:
- `backend/tools/xffprobe` as a diagnostic upstream (bind **staging loopback only**).
- `deploy/r1/probe-matrix.sh` for anonymous, synthetic header probes.
- `docs/R1-R2-RUNTIME-EVIDENCE.md §2` for the actual expected results.

On the **isolated staging** vhost only, route a restricted temporary probe path to the diagnostic upstream. Confirm the probe is not exposed through production.

| Case | Expected | Evidence |
|---|---|---|
| No forged header through CDN | client identity comes from verified chain, not browser claim | sanitised probe JSON |
| Forged single/multi XFF line | forged marker never selected | classified entries and resolution |
| IPv6, malformed, too long headers | safe fallback / bounded rightmost parse | verdict + source |
| Direct request to staging origin | proxy appends observed peer; forged marker never selected | origin matrix |
| Same CDN edge, two test visitors | independent buckets when edge ranges genuinely trusted | rate limiter test and metrics |
| Proxy prepend or preserve mode | **FAIL**; cannot trust header chain | stop/error report |
| Edge range absent/rotated | no forged identity selected, detect shared bucket | test and incident procedure |
| Telemetry on staging | expected forwarded/fallback/loopback counters | sanitized count summary |

Use documentation-range synthetic markers only. **Do not enter real origin IP or production URL in environment variables**. To verify TLS, use a separate certificate-validating HTTPS test, not `curl -k`; the probe matrix's `-k` is diagnostic-only.

If actual LiteSpeed does not append its observed peer last, stop; no software- or config-only workaround may be called verified without a new design and independent review. **Never change production `TRUSTED_PROXIES`.**

## D. R2 test matrix (staging only)

Run the existing `deploy/r2/cache-matrix.sh` only against the dedicated staging hostname (prefer CDN zone with identical software/rules). Use newly generated test cookies, never copied live cookies.

| Route class | Expected cache behavior | Required evidence |
|---|---|---|
| Customer / staff `/api/v1/*` including 200/401/403/404 | `Cache-Control: no-store`; second response never edge HIT | headers, `Age`, cache-status |
| Tracking `/api/v1/public/order` with different header tokens | no cross-token cache and no HIT | two synthetic order tokens |
| Customer pages and staff `/admin/*` | HTML and private navigation never cached across users | cache header matrix |
| CSV/XLSX reports and admin PII | only ADMIN, no cache | auth checks, headers |
| `/api/v1/public/menu` | public `no-cache` with revalidation/ETag | ETag/304 evidence |
| Hashed `/assets/*` and intended public media | long-lived cache allowed | asset status |
| TLS edge→origin | valid certificate/hostname, strict verification | sanitised config observation |

**Staging-equivalent** PASS is not evidence that the production MizbanCloud zone has the same rules. If using the actual staging CDN zone, R2 staging PASS may be granted, but production-edge gate remains BLOCKED pending a separately authorised production config verification.

## E. C6 compatibility regression on independent staging

- Run already-written C6 isolated tests, not a live-data migration: PostgreSQL 16/17 backend, controlled Tehran cross-day refund; C6 backup/restore/forward migration 00008 with synthetic data and binary rollback to a **source-rebuilt** baseline; Playwright account/history/admin downloads.
- Capture exact Git SHA, workflow links, staging DB identity, sanitized row-digest snapshots, check counts, error output, and rollback/roll-forward result.
- Any attempt to transfer/copy real production backup or exact installed production binary is **outside this approval**. Mark real-data restore and real-binary rollback NOT RUN. Do not invent evidence.

## F. Preparation checklist and evidence convention

Checklist for the operator:
- [ ] Host/domain/network ownership and isolation documented
- [ ] Synthetic data created; no real PII/secrets
- [ ] Identical LiteSpeed version confirmed or marked unavailable
- [ ] Staging CDN isolated and actual rule parity assessed
- [ ] R1 matrix executed only on staging with no forged markers selected
- [ ] R2 matrix executed only on staging with no private cache HIT
- [ ] TLS strict verified independently from the diagnostic curl settings
- [ ] C6 regression passes using staging DB
- [ ] Test evidence sanitized, stored in restricted staging location, cleanup complete
- [ ] Independent review, no release approval inferred

Required fields for every observation: test ID, staging hostname **masked in public report**, UTC/Tehran timestamp, exact git SHA and binary SHA256, proxy/CDN version, sanitized request pattern, expected/observed, result PASS/FAIL/BLOCKED/NOT RUN, evidence filename/hash, operator, independent reviewer and remediation link.

## G. Owner decisions still outstanding

| Gate | Current result after this approval |
|---|---|
| Authorization to prepare and run tests in **independent staging** | APPROVED (scope only; environment not provisioned) |
| R1 real **staging** LiteSpeed append verification | NOT RUN |
| R2 real **staging** CDN cache/TLS check | NOT RUN |
| R1/R2 production-edge behavior | BLOCKED — not authorized |
| Production trusted proxy / CDN config change | NOT AUTHORIZED |
| Real-production-data backup transfer | NOT AUTHORIZED |
| Exact deployed production binary extraction | NOT AUTHORIZED |
| Phone OTP, recovery, PII retention decisions | BLOCKED |
| Merge PR #18 into main / Production rollout | NO-GO |

### Next stage when staging resources exist

Supply only a staging hostname, staging host type/OS, whether LiteSpeed and an isolated MizbanCloud staging zone are available, and the staging operator. Store credentials in staging secrets management, **never in GitHub comments or chats**. Then execute and document the isolated tests using the existing scripts, record each actual result, and perform independent acceptance. An environment-independent plan must never be marked a passed runtime test.
