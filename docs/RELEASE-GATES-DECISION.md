# Release gates — decision record

**Production: NO-GO.** No gate below may be marked PASS without the evidence named. A green CI run
or a simulation is development evidence only. Statuses: PASS / FAIL / BLOCKED / NOT RUN.

| # | Gate | Status | Evidence | Next action | Approver |
|---|---|---|---|---|---|
| 1 | R1-A client-IP code and simulation | PASS (development) | `TestR1ProxyChain*`, `TestRateKey`, `clientip_*_test.go`; IPv6 /64 defect fixed | independent review | reviewer |
| 2 | R1-B LiteSpeed append behaviour, direct-origin, alternative headers | BLOCKED | none (docs/R1-R2-RUNTIME-EVIDENCE.md §2) | approve and run the staging probe matrix | owner |
| 3 | R1 enabling the CDN edge ranges in `TRUSTED_PROXIES` + telemetry window | BLOCKED (needs 2) | none | separate production change after 2 PASS | owner |
| 4 | R2-A application cache headers and shared-cache isolation | PASS (development) | `TestEveryAPIResponseIsNoStore`, `TestSharedCache*`; pre-R2 leak reproduced and fixed | independent review | reviewer |
| 5 | R2-B MizbanCloud bypass rule and TLS mode | BLOCKED | none (§3) | approve, read the rule, run `deploy/r2/cache-matrix.sh` | owner |
| 6 | C6 synthetic restore, migration, rollback (PG16/17) | PASS (development) | PR #20 CI and evidence artifacts; rerun on this branch | — | reviewer |
| 7 | Real-data restore in isolated staging | NOT RUN | none (§4) | approve the backup copy and staging host | owner |
| 8 | Rollback to the verified deployed binary | NOT RUN | none (§4) | supply `/opt/rokh/bin/rokh` + SHA-256 | owner |
| 9 | Browser E2E and payment reconciliation incl. cross-day refund | PASS (development) | Playwright suite, `TestCrossDayRefundTehran` (PG16/17) | — | reviewer |
| 10 | Phone ownership (OTP) and account recovery | BLOCKED | not implemented; phones shown as unverified | owner decides whether to launch without OTP; recovery design | owner |
| 11 | Privacy notice and retention policy | BLOCKED | none | owner/legal decision, then implementation | owner |
| 12 | Public customer registration | BLOCKED (needs 2, 3, 5, 10, 11) | registration exists but must not be announced | — | owner |
| 13 | Operational approval: deployment window, rollback plan, monitoring | NOT RUN | deploy/README.md §10–11 | after 2–11 | owner |

Signatures (name, date, decision): none recorded.
