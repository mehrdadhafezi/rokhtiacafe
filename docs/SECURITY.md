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
- PostgreSQL bound to localhost/unix socket.
- **Two database roles (F6):** `rokh_owner` runs migrations and owns all objects; the server runs as
  `rokh_app` with DML-only grants and no DELETE on financial/inventory/audit tables, so it cannot
  drop or disable the protective triggers. Neither is superuser, `CREATEROLE` or `CREATEDB`.
  Details: [DATABASE.md §6](DATABASE.md#6-protection-triggers-and-database-roles).
  `MIGRATION_DATABASE_URL` is only present when running `rokh migrate`, not in the service environment.
- Secrets only in server `.env` (mode 0600): `DATABASE_URL`, `APP_SECRET_KEY`, `VAPID_PRIVATE_KEY`. Never logged.
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
