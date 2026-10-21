# Security — Rokh Tia Café

## 1. Assets and threats

| Asset | Main threats | Primary controls |
|-------|--------------|------------------|
| Admin accounts | Credential stuffing, brute force, session theft | argon2id, login rate limits, HttpOnly/Secure cookies, server-side sessions |
| Financial records | Tampering, silent deletion | RBAC, append-only triggers, void-with-reason, audit log |
| Stock | Fake/overflow orders reserving all stock | Layered limits (§5), serialized global + per-client pending caps, 20-min expiry, ordering hours, kill switch |
| Customer orders | Enumeration or leakage of order links | 256-bit HMAC-derived tokens, stored hashed, sent in a header, 24 h lifetime (§10) |
| Server network | SSRF through push subscription endpoints | Push-host allowlist + public-IP-only dialer (§12) |
| Server | Malicious uploads, SQLi, XSS | Image re-encoding, sqlc parameterised queries, CSP, React escaping |
| Secrets | Leak via public repo | `.gitignore`, `.env.example` placeholders only, secret scanning in CI |

## 2. Authentication & sessions
- Passwords: **argon2id** (`m=19456 KiB, t=2, p=1`, 16-byte salt, PHC string). Min length 10, max 128; checked against a small embedded list of common passwords.
- Unknown username → verify against a dummy hash (uniform timing). Uniform error message.
- Session cookie: `__Host-rokh_session` in production (`Secure; HttpOnly; SameSite=Lax; Path=/`); dev on `http://localhost` uses `rokh_session` with `COOKIE_SECURE=false`.
- Token: 32 bytes from `crypto/rand`; only its SHA-256 is stored. Idle 12 h, absolute 7 days.
- Revocation: logout, password change/reset, deactivation, role change.

## 3. Permission matrix

Central policy: `auth.Can(role, action) bool` backed by one table in `internal/auth/policy.go`;
every admin route declares its action; a test asserts each registered route has a policy entry.

| Action | OPERATOR | ADMIN |
|--------|:-------:|:-----:|
| View dashboard (counts, low stock) | ✅ | ✅ |
| View today's revenue on dashboard | – | ✅ |
| List/view orders | ✅ | ✅ |
| Accept / ready / complete / cancel order | ✅ | ✅ |
| Record payment | ✅ | ✅ |
| Cancel an order that holds a prepayment (auto-refund of that prepayment) | ✅ | ✅ |
| Refund a completed order, void payment | – | ✅ |
| Switch public ordering off/on (kill switch) | – | ✅ |
| View categories/products | ✅ | ✅ |
| Toggle product sold-out | ✅ | ✅ |
| Create/edit categories & products, prices, images | – | ✅ |
| View stock levels (qty) | ✅ | ✅ |
| View costs / inventory value / movements / estimated unit costs | – | ✅ |
| Purchases, adjustments, voids | – | ✅ |
| Expenses | – | ✅ |
| Reports | – | ✅ |
| Push subscription (low-stock alerts) | – | ✅ |
| Users, audit log | – | ✅ |
| Change own password | ✅ | ✅ |

## 4. CSRF
- Synchronizer token per session, returned by `/auth/login` and `/auth/me`, sent as `X-CSRF-Token` on POST/PUT/PATCH/DELETE under `/api/v1/admin` and `/api/v1/auth` (except login).
- Additionally `Origin` (or `Referer` fallback) must match `APP_BASE_URL`.
- `SameSite=Lax` as defence in depth. Public endpoints use no cookies, so CSRF does not apply.

## 5. Rate limiting & abuse controls
In-memory token buckets (`golang.org/x/time/rate`), keyed by client IP. Client IP is taken
from `X-Forwarded-For` **only** when the direct peer is in `TRUSTED_PROXIES`.

| Scope | Limit |
|-------|-------|
| `POST /auth/login` per IP | 20 / 15 min |
| `POST /auth/login` per username | 5 / 15 min |
| `POST /public/orders` per IP | 30 / 10 min |
| `POST /public/orders` per `X-Client-Id` | 6 / 10 min |
| Open PENDING orders per client network (`client_key`) | `ORDER_MAX_PENDING_PER_CLIENT` (default 15), checked in the DB under the creation lock |
| Global open PENDING orders | `ORDER_MAX_PENDING` (default 40) → `503 ORDERING_BUSY`, under the creation lock |
| `GET /public/order` per IP | 240 / min |
| Other public per IP | 600 / min |

**Shared IPs are the normal case (F11):** customers on the café Wi-Fi share one public IP, and
Iranian mobile carriers use carrier-grade NAT, so many unrelated customers can share an IP too.
Per-IP limits are therefore loose and act only against bulk abuse. The per-device `X-Client-Id`
limit is tighter but spoofable; the real backstops are the serialized pending caps, the 20-minute
expiry, staff cancellation, and the ADMIN kill switch (`PUT /admin/ordering`). A replay of an
existing idempotency key is not counted against the limits.
Limits reset on restart (acceptable for a single-instance app). Values are owner decision O6.

### 5.1 Client IP behind the CDN (T14, R1)

Production chain: visitor → MizbanCloud edge → LiteSpeed on the origin → rokh on `127.0.0.1:18080`.
rokh's direct peer is always LiteSpeed on loopback. CDN edge addresses are *not* the proxy identity
rokh sees; they can only appear inside `X-Forwarded-For`. Evidence levels used below:
**[vendor]** documented by LiteSpeed/MizbanCloud, not observed here · **[code]** implemented and
unit-tested in Go · **[runtime]** observed on the production server (none yet).

- **Selection [code]** (`internal/httpx/clientip.go`): only when the peer is in `TRUSTED_PROXIES`,
  combine **all** `X-Forwarded-For` field lines in their received order (RFC 9110 §5.3; blank lines
  skipped), read the chain from the **right**, and take the first address that is not a trusted
  proxy. A chain split over several lines resolves exactly like the same chain on one line. A peer
  that is not trusted is never overridden; that is the only guarantee rokh gives on its own.
- **Fail closed [code]:** when the visitor cannot be established, the answer is the **anchor**:
  the right-most entry when it is a trusted proxy (the address LiteSpeed reports as its own peer,
  i.e. the edge it accepted the connection from), otherwise the peer `127.0.0.1`. Entries further
  left are header claims: they are believed only while looking for the visitor, never used as a
  fallback identity, so writing addresses that happen to lie in a trusted range cannot choose a
  bucket (R1-B3, `TestB3FallbackAnchorAdversarial`). Trusted-range membership alone never makes a
  header entry a verified hop.
  Cases: no header (`fallback_missing`), an unparsable or zoned entry (`fallback_malformed`), no
  untrusted entry within the 16 right-most entries (`fallback_too_long`; entries further left are
  never parsed), only trusted proxies (`fallback_proxy_only`).
- **What rokh cannot check:** whether the right-most entry really is the address LiteSpeed saw,
  or which line LiteSpeed wrote. **[vendor]** In "Trusted IP Only" mode LiteSpeed modifies the
  backend `X-Forwarded-For` "to include the connecting peer address" (only "Keep Header from
  Trusted IP" does not); whether on the same line, a new last line, or by replacement is **not
  documented**. If LiteSpeed passed the header through unchanged, or wrote its peer anywhere but
  at the end, the visitor would choose their own address (`TestR1KnownLimitationNoAppend`, case
  M06). **[runtime]** not yet verified.
- **Ignored headers [code]:** rokh never reads `MC_REAL_IP` or `X-Real-IP`; the LiteSpeed include
  also removes them before proxying (whether LiteSpeed honours that is not yet verified).
- **`TRUSTED_PROXIES` today:** `127.0.0.1` only. This is **not** by itself spoof-resistant: rokh
  trusts what LiteSpeed puts at the end of the header. If LiteSpeed appends correctly, each
  visitor appears as their CDN edge (visitors behind one edge share buckets); if it does not, the
  address is visitor-controlled. The MizbanCloud edge ranges (`https://mizbancloud.com/ips.txt`)
  are added **only after** the runtime acceptance passes. A stale list degrades safely: an edge
  missing from it is treated as the client (shared bucket), never as a forged address.
- **Range guard [code]:** production refuses `TRUSTED_PROXIES` entries wider than /16 (IPv4) or
  /32 (IPv6). This is a defence-in-depth heuristic, not a proof: the current edge list is /22 or
  narrower; a legitimately wider range (some CDNs publish IPv6 /29) needs a reviewed change and is
  refused loudly at startup, never accepted silently.
- **Direct-to-origin requests:** *if* LiteSpeed appends the real peer, that untrusted address is
  selected even when the request forges edge entries or a whole first line **[code]**; otherwise
  the forged value wins (same limitation as above).
- **Observability [code]:** every `/api/v1` request is classified into fixed counters (by source,
  multi-line headers, "forwarded with no trusted hop crossed", loopback attribution). Every
  5 minutes with traffic, one log line `"msg":"client ip summary"` is written, at WARN when at least
  20 requests and at least 1 % of all fell back or were attributed to loopback. It contains counts
  only: no addresses, no header values, no cookies (`TestClientIPSummaryLogIsPrivateAndBounded`).
  Each summary is one consistent snapshot (a mutex guards the counter bank; total = sum of
  sources), and the last partial interval is flushed on shutdown, after in-flight requests drained
  (`TestB3StatsConcurrentConservation`, `TestB3StatsFinalFlush`). It cannot detect a forged chain
  that looks valid. Operator use: deploy/README.md §11.
- **Consequences of a fallback [code]:** the fallback address is used like any client IP: for the
  per-IP rate limits, the per-client pending-order cap (`client_key`) and `audit_logs.ip`. Normal
  traffic never lands on it while the chain is healthy; if most traffic does, many customers share
  those limits. No limit is disabled or relaxed automatically; the response is operational
  (deploy/README.md §11, docs/HOSTING-ASSESSMENT.md R1).
- **R1 PASS** requires all of: the runtime acceptance (R1-B2-RT) observed on the server, the owner's
  sign-off of gate G3, and a clean `client ip summary` window after enabling the edge ranges.
  Unit tests alone never make R1 PASS.
- **Caching:** the CDN must not cache `/api/*`, `/admin/*` or HTML (risk R2). Order tracking
  (`GET /public/order`, token in a header) sends `no-store`.

## 6. Input validation
- Strict JSON decoding (`DisallowUnknownFields`), body size limits, explicit per-field validation in Go; mirrored Zod schemas in the frontend for UX only — the server is authoritative.
- Text fields: trimmed, Unicode NFC, control characters removed, length-checked in runes. Persian/Arabic digits normalised to ASCII for numeric inputs.
- All SQL through sqlc-generated parameterised queries. No string-built SQL.

## 7. Image uploads
- Max 3 MB, ≤ 4000×4000 px (checked via `image.DecodeConfig` before full decode).
- Type by content sniffing (JPEG/PNG/WebP only), not by extension or client MIME.
- Decoded and **re-encoded to JPEG** (max 800 px, quality 82) → strips metadata and polyglot payloads.
- Stored outside the web root with random names (`<32 hex>.jpg`); served by Go with fixed `Content-Type: image/jpeg`, `X-Content-Type-Options: nosniff`, long cache.

## 8. HTTP hardening
- Headers: `Content-Security-Policy: default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'`, `Strict-Transport-Security` (prod), `Referrer-Policy: same-origin`, `X-Content-Type-Options: nosniff`, `Permissions-Policy: camera=(), microphone=(), geolocation=()`.
- Go server timeouts: read header 5 s, read 15 s, write 30 s, idle 60 s; graceful shutdown.
- App listens on `127.0.0.1` only; TLS terminated by the reverse proxy.
- Panic recovery returns `500 INTERNAL` without stack traces; details only in logs with request id.

## 9. Data & secrets
- PostgreSQL bound to localhost/unix socket (production: Docker port published on `127.0.0.1` only,
  no `trust` authentication).
- **Two database roles (F6):** `rokh_owner` runs migrations and owns all objects; the server runs as
  `rokh_app` with DML-only grants and no DELETE on financial/inventory/audit tables, so it cannot
  drop or disable the protective triggers. Neither is superuser, `CREATEROLE` or `CREATEDB`.
  Details: [DATABASE.md §6](DATABASE.md#6-protection-triggers-and-database-roles).
  `MIGRATION_DATABASE_URL` is only present when running `rokh migrate`, not in the service environment.
- Secrets only in the server settings files (deploy/README.md §2):
  - `/etc/rokh/rokh.env` (root:rokh 0640): `DATABASE_URL`, `APP_SECRET_KEY`, `VAPID_PRIVATE_KEY`;
  - `/etc/rokh/migrate.env` (root 0600): the owner role;
  - `/etc/rokh/postgres.env` (root 0600): first-initialisation passwords.

  They are never logged. The ops scripts pass database passwords in `PGPASSWORD` and hand settings
  to the rokh binary through its environment, never as command arguments.
- Logs exclude passwords, cookies, CSRF tokens, tracking tokens (logged as prefix only), push keys.
- Backups encrypted at rest when copied off-server; restore tested before go-live.
- CI runs `gitleaks` on every push/PR.

## 10. Tracking tokens (F11)
- Token = base64url(HMAC-SHA256(`APP_SECRET_KEY`, `"trk:" || idempotency_key`)) — 256 bits, unguessable
  without the server key. The server can re-derive it for idempotent replays, so it never stores it.
- DB stores only `SHA-256(token)` (`orders.tracking_token_hash`); a DB leak does not reveal usable links.
- Sent only in the `X-Tracking-Token` header; the customer page keeps it in the URL fragment
  (`/menu/order#…`) and localStorage, so it never reaches access logs, proxies, or `Referer`.
- Valid for 24 h after order creation. Grants only: view that order; cancel it while PENDING.
- Rotating `APP_SECRET_KEY` invalidates outstanding links (acceptable: they are short-lived).

## 11. Staff PWA separation (F12)
Only the staff app (`/admin/`) has a web app manifest and a service worker, scoped to `/admin/`.
Customer pages under `/menu` have no manifest, no service worker, and never download admin code,
so customers are not prompted to install the staff app and nothing admin-related is cached on
their phones.

## 12. Web Push endpoint safety (F11)
Push subscription endpoints are URLs supplied by a browser, and the server makes outbound
requests to them, so they are treated as untrusted:
- Allowlist of push-service hosts (exact host or listed suffix): `fcm.googleapis.com`,
  `updates.push.services.mozilla.com`, `web.push.apple.com`, `*.notify.windows.com`.
  Configurable via `PUSH_HOST_ALLOWLIST`; checked on subscribe and again before every send.
- `https` only, port 443, no userinfo, no IP-literal hosts, length ≤ 2048.
- Custom dialer resolves the host and refuses loopback, private (RFC 1918/4193), link-local,
  CGNAT, multicast and unspecified addresses; connects to the vetted IP (no DNS-rebinding gap).
- No redirects followed; 10 s timeout; response body discarded after 4 KB.
- Only ADMIN users can register subscriptions.

## 13. Admin membership races (F11)
Deactivation, role change and user creation take `pg_advisory_xact_lock(LOCK_ADMIN_MEMBERSHIP)`
before checking "at least one active ADMIN", so two admins demoting each other concurrently
cannot both succeed. Session revocation happens in the same transaction, and every request
re-checks `users.is_active` and `role` via the session join (no cached role in the cookie).

## 14. Out of scope for MVP
2FA, account lockout emails, WAF, per-device session management UI, field-level encryption.
