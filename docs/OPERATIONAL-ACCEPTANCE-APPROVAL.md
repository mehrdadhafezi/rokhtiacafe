# R1/R2 Operational acceptance — owner approval and evidence checklist

**Status:** NOT EXECUTED / PRODUCTION NO-GO. Prepared after development merge `9a84537dd0f98bfd0868500ada00ecd19c46a72f`. This is an approval/evidence plan, not authorization to run commands.

## Scope boundary

Tests on the real CDN/LiteSpeed, including read-only requests to the origin, **require separate explicit owner approval** of the host, narrow time window, responsible operator, allowed routes, safe test accounts, and stop conditions. Do not replace the live rokh upstream, restart any shared service, change CDN/cache/routing, or move real DB data under a general testing approval. No production changes are authorized by this checklist.

Prebuilt test utilities:
- `backend/tools/xffprobe`
- `deploy/r1/probe-matrix.sh`
- `deploy/r2/cache-matrix.sh`
- `docs/R1-R2-RUNTIME-EVIDENCE.md`
- `docs/RELEASE-GATES-DECISION.md`
- `docs/HOSTING-ASSESSMENT.md`

## Package A — R1 real header forwarding

**Approval scope:** an isolated staging vhost with the same LiteSpeed rules, plus a specifically approved low-impact validation through the actual CDN/origin if required. Prefer a dedicated vhost/port with no unrelated traffic. Running xffprobe as an alternate handler of a live vhost is prohibited without a separately approved maintenance plan.

- [ ] Name staging hostname, responsible operator, test window and documented approval ID.
- [ ] Confirm diagnostic service has no secrets and is bound to loopback; any new public path is temporary and has owner-approved isolation and removal plan.
- [ ] Through CDN and controlled direct-origin routes, send *document-range* forged values, split XFF lines, missing and malformed headers, IPv6 and direct-bypass probes. Do **not** use real customer addresses or credentials.
- [ ] Collect sanitized observations verifying the **actual connecting peer appended last** by LiteSpeed, not merely vendor promises; if preserve/prepend is observed, FAIL R1 and stop.
- [ ] Verify origin can be reached only through expected trust boundary or document explicit origin-bypass mitigation required.
- [ ] Verify independent users behind the same CDN edge get independent per-IP rate-limit buckets when verified edge ranges are enabled; never change trusted ranges in Production during the test.
- [ ] Observe a `client ip summary` telemetry window on staging (fallback, loopback, unexpected multiple lines). No customer IP values in public reports.
- [ ] Record PASS/FAIL/BLOCKED and linked evidence. A code-only simulation is not an acceptable runtime PASS.

## Package B — R2 actual CDN caching, TLS and private data

**Approval scope:** passive inspection of approved CDN configuration, plus test-only traffic against a staging endpoint mirroring actual MizbanCloud rules.

- [ ] Verify actual cache-bypass rules for `/api/*`, `/admin/*`, HTML and every account or tracking route.
- [ ] Compare cache headers, `Age`, `Mc-Cache-Status`, on repeated requests for test customer A/B, ADMIN/OPERATOR and anonymous users, using only synthetic accounts.
- [ ] Verify that an identical tracking URL with distinct tracking tokens in headers is never cached cross-identity; test 200, 401, 403, 404 responses and downloads.
- [ ] Confirm frontend HTML and staff service worker are never served stale or cross-scope; retain efficient caching of hashed public assets.
- [ ] Inspect CDN-to-origin TLS mode and certificate verification. **Do not treat the R1 diagnostic's curl -k as TLS proof.** Capture strict validated TLS evidence separately.
- [ ] No rule change to the production CDN as part of evidence gathering. Log exact discrepancy and submit a separate change request if needed.

## Package C — Isolated real-data restore and exact-binary rollback

**Approval scope:** separate written authorization to copy **sanitized** data and a hash-verified installed artifact from the live host into an isolated, access-controlled staging environment.

- [ ] Define the data minimization/anonymization procedure, recipient and retention/delete-after-test timeline; no raw customer PII or secrets in GitHub or logs.
- [ ] Confirm separate staging DB identity; empty new target, no route to the live DB and no shared uploads.
- [ ] Record backup/archive SHA256, source date, PostgreSQL version and recovery baseline.
- [ ] Restore using forward-only migrations; run `rokh check`, reconcile guest orders, payment ledger and inventory, sample historical anomalies.
- [ ] Obtain and hash the **actually installed** previous binary (not a rebuild of main); rehearse binary-only rollback and roll-forward on the isolated migrated DB.
- [ ] Execute C6 browser, cross-day payments/refunds, CSRF, auth, reports and exports on staging.
- [ ] Record cleanup, staging snapshot and evidence; NOT RUN unless authorized and executed.

## Package D — Customer policy and final release decision

Before public registration, the owner/legal/security reviewers decide and sign:
- [ ] Whether phone OTP ownership verification is required before account activation; no unverified account may claim historical orders from phone match alone.
- [ ] Safe recovery process when number/password changes, with identity verification and abuse rate limits.
- [ ] Privacy notice, customer data retention/deletion, financial preservation exceptions and backup retention.
- [ ] Who can approve production and when: release SHA, CI artifact hash, monitoring owner, maintenance window, rollback threshold, customer support plan and explicit GO/NO-GO.
- [ ] Confirm no public registration announcement or deployment until the above are resolved.

## Approval record (not filled; no approval inferred)

| Package | Approved scope / exact host | Approver / date / written reference | Status |
|---|---|---|---|
| A — R1 live/staging probes | — | — | BLOCKED: approval missing |
| B — R2 CDN configuration and test traffic | — | — | BLOCKED: approval missing |
| C — sanitized backup and deployed binary export | — | — | BLOCKED: approval missing |
| D — OTP, recovery and privacy | — | — | BLOCKED: policy decision missing |
| Final production change | — | — | NOT AUTHORIZED |

### Rules

Write factual evidence to `docs/R1-R2-RUNTIME-EVIDENCE.md` and update `docs/RELEASE-GATES-DECISION.md` **only after an approved test has actually run**. Evidence must distinguish simulator/local/CI from staging network and production-edge observations. If any proof is missing, keep the matching gate BLOCKED or NOT RUN. Do not merge PR #18 into main or deploy from this documentation branch.
