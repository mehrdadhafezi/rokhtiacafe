# Rokh independent Staging — evidence record (blank)

**Execution status:** NOT RUN. **Production:** NO-GO. Do not fill a PASS from a code simulation, a Claude narrative, or local CI when the row asks for independent Staging runtime verification.

## Environment provenance

| Field | Value |
|---|---|
| Owner's authorised scope | Independent Staging only (2026-11-05) |
| Staging hostname / dedicated VM ID | NOT PROVIDED |
| Isolation proof (no routing to production DB, origin, uploads) | NOT RUN |
| Operator and reviewer | NOT ASSIGNED |
| Test date/time, timezone | NOT RUN |
| Development commit SHA | NOT PINNED FOR STAGING RUN |
| Release binary SHA256 | NOT RUN |
| PostgreSQL version and isolated DB identity | NOT RUN |
| LiteSpeed version/config SHA256 (staging) | NOT RUN |
| CDN provider/zone (staging), rules hash | NOT RUN |
| Synthetic data provenance | NOT RUN |
| Staging logs/evidence directory (restricted, non-PII) | NOT RUN |

## Runtime acceptance matrix

| ID | Scenario | Expected | Observed | Status | Evidence hash/path |
|---|---|---|---|---|---|
| STG-ISO-01 | Independent network, services, DB, uploads | No live system access | — | NOT RUN | — |
| STG-R1-01 | LiteSpeed appends actual peer last | No user-controlled last hop | — | NOT RUN | — |
| STG-R1-02 | Forged XFF and multi-line values | Forged marker never used | — | NOT RUN | — |
| STG-R1-03 | Direct-to-staging-origin forged header | Actual observed peer anchors IP | — | NOT RUN | — |
| STG-R1-04 | IPv6 and edge rotation | Stable /64 limit, no spoofing | — | NOT RUN | — |
| STG-R1-05 | Two test visitors behind shared edge | Independent visitor buckets | — | NOT RUN | — |
| STG-R1-06 | Missing/malformed header and metrics | Bounded fallback, no unsafe identity | — | NOT RUN | — |
| STG-R2-01 | Customer A/B `/me`, history | Private, no cache HIT | — | NOT RUN | — |
| STG-R2-02 | ADMIN/OPERATOR reports, errors and exports | Auth isolated, no HIT | — | NOT RUN | — |
| STG-R2-03 | Distinct tracking tokens at same URL | Never cross-token cached | — | NOT RUN | — |
| STG-R2-04 | HTML, SW and hashed assets | HTML private; hashed assets may cache | — | NOT RUN | — |
| STG-R2-05 | Staging CDN→origin TLS | Certificate strictly validated | — | NOT RUN | — |
| STG-C6-01 | Synthetic restore and migrate 00008 | Historic rows unchanged | — | NOT RUN | — |
| STG-C6-02 | Guest/customer orders + PII separation | Only owner accesses history | — | NOT RUN | — |
| STG-C6-03 | Cross-day refund and ledger | Reconciles with summary | — | NOT RUN | — |
| STG-C6-04 | Rebuilt-old-binary rollback | Legacy works, customer rows preserved | — | NOT RUN | — |
| STG-C6-05 | Playwright browser acceptance | Full suite passes | — | NOT RUN | — |

For each executed row record: sanitized command reference (no passwords, cookies or tokens), exact expected/observed behavior, URL/path of access-controlled evidence, SHA256, operator, reviewer and date. Never paste a production or real customer address/header into this document.

## Out-of-scope gates (must remain BLOCKED / NOT RUN)

| Gate | Status | Reason |
|---|---|---|
| Production LiteSpeed behavior | BLOCKED | Not authorized |
| Production MizbanCloud cache/TLS behavior | BLOCKED | Not authorized |
| Real production backup restore | NOT RUN | Transfer not authorized |
| Actual installed production binary rollback | NOT RUN | Artifact acquisition not authorized |
| Phone OTP and account recovery | BLOCKED | Owner/policy decision required |
| Privacy and retention policy | BLOCKED | Owner/legal decision required |
| Final production release | NO-GO | Multiple unresolved gates |

## Acceptance sign-off

- Staging operator: **unassigned**
- Independent code/security reviewer: **unassigned**
- Evidence completeness: **NOT RUN**
- Staging-only conclusion: **NOT RUN**
- Production conclusion: **NO-GO**
