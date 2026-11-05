# Rokh — R1/R2 and final release-readiness gates

**Current state:** PLANNING / IMPLEMENTATION NOT STARTED. This document records a work package and acceptance criteria, **not** evidence that R1, R2 or production release gates passed.

**Baseline:** C1–C6 development branch merge `12d7e61e604996efe452e3156dcfb0c1a111588c`. Work exclusively on `feat/r1-r2-production-readiness`, with a Draft PR against `feature/customer-accounts-history-reporting`. Neither `main` nor the live site is an approved target.

## 0. Immutable boundaries

- NO production deployments, live DB/migration/backup/restore, server restarts, WHM/LiteSpeed user-data include changes, DNS, CDN, firewall, certificate, trusted-proxy or service reconfiguration in this task.
- Owner approval is required for any active runtime experiments that reach the production origin or edge, including read-only diagnostic probes if those require infrastructure access/allowlisting. First implement/test in a disposable reverse-proxy + CDN simulation/staging environment.
- Do not enable customer signup to the public until ownership verification, recovery and retention policy are approved.
- Never log or submit customer PII, cookies, auth tokens, passwords, sensitive headers or raw traffic into CI artifacts.
- Keep all existing guest/staff/customer flows and the C6 acceptance harness green.

## 1. R1 threat model and trust boundaries

Production topology described in `docs/SECURITY.md §5.1`, `docs/HOSTING-ASSESSMENT.md`, `deploy/README.md §5/§11`:

`visitor → MizbanCloud CDN edge → LiteSpeed vhost on shared cPanel/WHM origin → rokh on 127.0.0.1:18080`.

The application sees a loopback peer. Its `backend/internal/httpx/clientip.go` reads the full `X-Forwarded-For` header sequence right-to-left only if the direct peer is trusted. It must **not** accept visitor-controlled forwarded identity as verified. Existing unit tests explicitly show unsafe behavior if LiteSpeed merely forwards or prepends an attacker-supplied XFF instead of appending its observed connection peer at the end (`TestR1KnownLimitationNoAppend`, `M06`). The CDN edge address list alone does not close this threat.

### R1-A: code and simulation work

1. Audit `backend/internal/httpx/clientip.go`, `clientip_*_test.go`, `backend/internal/httpx/clientip_stats.go`, `backend/internal/api/api.go`, the rate limiter keys, config validation and the LiteSpeed vhost example.
2. Implement tests and any necessary hardened behavior in a disposable, reproducible HTTP chain with realistic XFF *append, preserve, replace, prepend, duplicate-line, empty, malformed* modes. Explicitly demonstrate the unsafe cases rather than hiding them behind a green unit test.
3. Define fail-closed acceptance for spoofed or ambiguous headers. Do not silently switch to `X-Real-IP`, `MC_REAL_IP`, arbitrary `Forwarded` or public CIDR trust; don't trust proxies from unverified inputs.
4. Require direct-to-origin attack test (bypassing CDN) and two legitimate visitors behind a shared edge. Verify per-IP login, registration, guest order and tracking throttles cannot be bypassed through spoofing and do not unnecessarily collapse into a global bucket.
5. Review the published CDN edge prefix provenance and expiration/rotation procedure. Do **not** add broad trusted ranges without owner-approved current evidence and an origin-hop assertion. Keep fallback telemetry free of raw sensitive headers.
6. An application-only solution can NOT establish that LiteSpeed appends its actual peer; mark R1-B (runtime) **BLOCKED** until the owner supplies verifiable staging or approved origin evidence.

### R1-B: approved infrastructure/runtime acceptance

Execute **only after separate owner authorization**, preferably on realistic staging, without modifying shared production services:

- Capture sanitised evidence of how LiteSpeed forwards XFF to a diagnostic upstream: same line versus separate line, and whether the actual connecting peer is appended **last**. Verify forged leftmost, duplicated headers, IPv4/IPv6, absent header, direct origin request and genuine CDN client paths. Use test-only identifiers and redact addresses outside synthetic documentation as necessary.
- Verify the edge cannot be bypassed, or explicitly harden direct origin access according to an owner-reviewed approach without unintended disruption to other sites.
- Validate whether the `RequestHeader unset` commands in `deploy/litespeed/rokh-proxy.ssl.conf.example` are actually honored by the deployed LiteSpeed version. Never alter shared configuration during the audit.
- Prove `TRUSTED_PROXIES` includes only necessary actual proxies, and every trusted hop is operationally verified. Document behavior if CDN edge ranges change.
- Record client-IP telemetry on an approved interval and check fallback/loopback buckets, as required by `docs/SECURITY.md` (minimum 20 requests, <1% warning thresholds per current implementation).
- If rightmost-hop append cannot be established, **STOP**, design a verified proxy-boundary rewrite/normalization before attempting production; don't mark R1 PASS.

## 2. R2: CDN/cache isolation

Risk in `docs/HOSTING-ASSESSMENT.md`: caching `/api/v1/public/order` or privileged customer/admin API endpoints could disclose private data between users. C6 only proved application `Cache-Control` headers and does **not** prove MizbanCloud honors a bypass rule.

### R2-A: code and disposable staging

- Audit every `/api/v1/{auth,customer,admin,public/order}` handler response (including 2xx, 3xx, 4xx and 5xx), static HTML, admin service worker and customer app; avoid breaking intended caching of hashed public assets and product images.
- Prefer defense-in-depth default `Cache-Control: no-store` for all private/sensitive API responses, with carefully scoped exceptions for genuinely public content. Evaluate `Vary` and cookies/Authorization/tracking-token-sensitive paths; don't rely on CDN default behavior.
- Add automated tests for per-user data isolation through a configurable simulated caching proxy, including one path shared by users with token in a **header**; verify no cached response is served cross-user or cross-role.
- Check that staff PWA is scoped to `/admin/` and does not cache authenticated API responses.

### R2-B: approved CDN edge verification

- Owner-approved rule: bypass cache for `/api/*`, `/admin/*` and HTML (including customer account pages). Cache only the explicitly public static asset/media types under documented policy.
- Verify via actual response headers and `Mc-Cache-Status` in an approved environment for anonymous, signed-in customer A, signed-in B, staff ADMIN and OPERATOR, plus tracking requests with distinct header tokens; never send real user cookies or tokens in evidence.
- Verify origin/edge HTTPS and the CDN-to-origin certificate validation (full/strict) under a separate authorization. Distinguish browser cache from CDN cache; never assume `no-store` at origin proves edge bypass.
- R2 remains BLOCKED without actual edge verification.

## 3. Customer identity, privacy and retention decisions (owner signoff)

Before public registration:
- Phone number must be explicitly labeled **unverified** until an OTP ownership check is implemented. Decide whether self-registration without OTP is permitted at launch; evaluate hijacking of an unverified phone identity and any support-based recovery process. Do not implement insecure recovery based on name/phone alone.
- Define account recovery and loss-of-access workflow with auditable verification, abuse limits, and separation from staff reset.
- Approve user-facing privacy notice and minimum/maximum retention for customers, audit events, historical orders, sessions, backups, and deletion/anonymization requests (subject to legal accounting-record preservation).
- Do not collect/send actual PII to GitHub CI or staging without explicit approval.

## 4. Realistic staging and verified deployed-binary rollback

The C6 synthetic rehearsal PASS is insufficient for Production GO. After explicit owner permission:
1. Obtain an authorized sanitised copy of the real database and uploads with provenance and SHA256 checks. Test **restore only into a new isolated database**; prove old orders and financial totals unchanged after migration.
2. Obtain the actual previously deployed `/opt/rokh/bin/rokh` artifact with verified hash, not merely a rebuild of `main`; inspect version differences and compatibility before any staging rollback. Do not copy secrets to repo/CI.
3. Stage the full requested network path, including real or controlled equivalent LiteSpeed and CDN, with approved isolated credentials, no routes to live data.
4. Repeat guest/customer ordering, multi-day payments and refunds, per-role PII permissions, snapshot comparison, XLSX/CSV, browser E2E, rolling-back the **actual prior artifact**, and roll-forward.
5. Define and rehearse exit/abort conditions, decision-owner approvals, backed-up binary, recoverability, monitoring, and a planned maintenance/rollback window. **No production action as part of this task.**

## 5. Required artifacts

- `docs/R1-R2-RUNTIME-EVIDENCE.md`: threat cases, observed versus expected, sanitised peer append traces, CDN cache matrix, source of edge ranges, operator validation. All unavailable evidence fields must be `NOT TESTED`, not fictitious PASS.
- `docs/RELEASE-GATES-DECISION.md`: signoff matrix for R1, R2, real-data restore, actual-binary rollback, end-to-end browser + payment reconciliation, privacy/retention, phone verification/recovery, and owner approval. Gate statuses: PASS / FAIL / BLOCKED / NOT RUN.
- CI tests: client-IP spoof and rate-limit boundary, private API caching, old/new binary compatibility, plus existing C6 PG16/17 + Playwright.
- A report of all modified files, commits, CI links, blockers, and the precise next action requiring owner approval.

## 6. Merge / release rules

- Draft PR may be merged **only into the C1–C6 development branch**, after independent review and all applicable automated checks green.
- **Never merge `feature/customer-accounts-history-reporting` into `main` as part of this task.**
- **Never deploy to the live origin** without separately signed R1/R2 and real-data staging gates.
- A green build or synthetic simulation is **not** evidence of R1-B or R2-B runtime acceptance.

Current decision: R1-B **BLOCKED**, R2-B **BLOCKED**, real-data restore **NOT RUN**, actual-production-binary rollback **NOT RUN**, phone/retention policy **BLOCKED**, Production **NO-GO**.
