# Security — Rokh Tia Café

## 1. Assets and threats

| Asset | Main threats | Primary controls |
|-------|--------------|------------------|
| Admin accounts | Credential stuffing, brute force, session theft | argon2id, login rate limits, HttpOnly/Secure cookies, server-side sessions |
| Financial records | Tampering, silent deletion | RBAC, append-only triggers, void-with-reason, audit log |
| Stock | Fake/overflow orders reserving all stock | Rate limits, global pending cap, 20-min expiry, ordering hours, quantity limits |
| Customer orders | Enumeration of other customers' orders | 128-bit random tracking tokens; no sequential public ids |
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
| Refund, void payment | – | ✅ |
| View categories/products | ✅ | ✅ |
| Toggle product sold-out | ✅ | ✅ |
| Create/edit categories & products, prices, images | – | ✅ |
| View stock levels (qty) | ✅ | ✅ |
| View costs / inventory value / movements | – | ✅ |
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
| `POST /auth/login` per IP | 10 / 15 min |
| `POST /auth/login` per username | 5 / 15 min |
| `POST /public/orders` per IP | 10 / 10 min (generous: café Wi-Fi customers share one IP) |
| `GET /public/orders/{token}` per IP | 120 / min |
| Other public per IP | 300 / min |
| Global pending orders | `ORDER_MAX_PENDING` (default 40) → `503 ORDERING_BUSY` |

Plus: ordering hours, 15 lines × 20 qty max, 20-minute expiry, customer cancel.
Limits reset on restart (acceptable for a single-instance app).

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
- PostgreSQL bound to localhost/unix socket; app role is owner of its schema only (no superuser, no `CREATEROLE`).
- Secrets only in server `.env` (mode 0600): `DATABASE_URL`, `VAPID_PRIVATE_KEY`. Never logged.
- Logs exclude passwords, cookies, CSRF tokens, tracking tokens (logged as prefix only), push keys.
- Backups encrypted at rest when copied off-server; restore tested before go-live.
- CI runs `gitleaks` on every push/PR.

## 10. Tracking tokens
Stored in plaintext (unique index) so idempotent replays can return them. Justification:
a token only grants viewing that order and cancelling it while PENDING; anyone who can read
the DB already sees all orders. 128 bits of entropy (22 base64url chars).

## 11. Out of scope for MVP
2FA, account lockout emails, WAF, per-device session management UI, field-level encryption.
