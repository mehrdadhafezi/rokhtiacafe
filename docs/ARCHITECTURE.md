# Architecture — Rokh Tia Café

## 1. Shape

One **modular monolith**: a single Go binary that serves the REST API, the built React
SPA (embedded with `go:embed`), uploaded images, and runs background jobs. One
PostgreSQL database on the same host, reachable only via localhost/unix socket.

```mermaid
flowchart LR
  subgraph Phone["Customer / staff phone"]
    SPA["React PWA\n/menu · /admin"]
    SW["Service Worker\n(push, app shell)"]
  end
  RP["LiteSpeed/Apache or Caddy\nTLS termination · reverse proxy"]
  subgraph Host["Server (single host)"]
    BIN["rokh (Go binary)\nchi router · jobs"]
    PG[("PostgreSQL\nlocalhost only")]
    UP[/"uploads dir\n(outside web root)"/]
  end
  PUSH["Browser push services\n(FCM / Mozilla / Apple)"]
  SPA -- HTTPS --> RP --> BIN
  BIN --> PG
  BIN --> UP
  BIN -- "Web Push (VAPID)" --> PUSH --> SW
```

Why: one deployable artifact, no broker, no cache server, trivial rollback (swap
binary). Volume is tiny; PostgreSQL row locks and `SKIP LOCKED` cover all concurrency.

## 2. Backend

### 2.1 Stack
Go **1.27.x**, chi v5, pgx v5 (pool), sqlc (pgx/v5 driver), goose v3 (migrations
embedded in the binary). Additional minimal deps:

| Dependency | Purpose |
|------------|---------|
| `golang.org/x/crypto/argon2` | password hashing |
| `golang.org/x/time/rate` | in-memory rate limiting |
| `golang.org/x/image/draw` | image resize |
| `github.com/SherClockHolmes/webpush-go` | VAPID Web Push encryption/sending |
| `log/slog` (stdlib) | structured logging |

No ORM, no DI framework, no validation library (explicit validation functions).

### 2.2 Layout

```
backend/
  cmd/rokh/main.go          # subcommands: serve | migrate up|status | user create | vapid gen | check inventory
  db/
    migrations/             # goose SQL files (embedded)
    queries/                # sqlc query files, one per module
  sqlc.yaml
  internal/
    config/                 # env parsing + validation, fail fast
    store/                  # sqlc-generated code (do not edit) + Tx helper
    httpx/                  # router, middleware, JSON + error helpers
    auth/                   # password hashing, sessions, CSRF, policy (Can(role, action))
    catalog/                # categories, products, images, public menu
    inventory/              # balances, movements, purchases, adjustments, WAC
    orders/                 # public ordering, tracking, state machine, expiry
    payments/               # payments, refunds, voids, payment_status
    expenses/
    reports/
    push/                   # subscriptions, outbox worker
    audit/                  # audit.Record(ctx, tx, ...)
    jobs/                   # ticker loop running module jobs
  web/                      # embed.go → frontend/dist copied at build time
```

### 2.3 Module rules
- Each module exposes `Service` (business logic) and `Handler` (HTTP). Handlers do
  parsing, authz check, call service, map errors. Services own transactions.
- Cross-module writes happen **inside one transaction**: services accept a
  `store.Querier` bound to a `pgx.Tx`. Example: `orders.Complete` calls
  `inventory.ConsumeReservation(ctx, q, ...)` and `audit.Record(ctx, q, ...)` in the same tx.
- No generic repository layer: sqlc queries are the data layer.
- Domain errors are typed (`ErrInsufficientStock`, `ErrIllegalTransition`, …) and mapped
  to the error codes in [API-CONTRACT.md §2](API-CONTRACT.md#2-conventions).

### 2.4 Transactions & concurrency
- Default isolation READ COMMITTED; correctness relies on **conditional UPDATEs**
  (`... WHERE status = 'PENDING'`, `... WHERE on_hand - reserved >= $q`) and DB constraints,
  not on application-level reads.
- When several inventory rows are touched, lock in ascending `product_id` order to
  avoid deadlocks.
- Daily order numbers use `pg_advisory_xact_lock` keyed on business date.
- Jobs pick rows with `FOR UPDATE SKIP LOCKED`, so a second instance would be safe.

### 2.5 Background jobs (in-process, `jobs` ticker)
| Job | Interval | Work |
|-----|----------|------|
| Expire pending orders | 30 s | Cancel PENDING orders with `expires_at < now()`, release reservations |
| Push outbox | 10 s | Send due `notification_events`, retry with backoff |
| Session cleanup | 1 h | Delete expired sessions |
| Notification cleanup | 24 h | Delete SENT/FAILED/GONE events older than 30 days |

### 2.6 Time
All timestamps `timestamptz`. Business timezone `Asia/Tehran` (binary imports
`time/tzdata` so it works on Windows and minimal servers). "Business date" = local
date in Asia/Tehran. Jalali calendar handled only in the frontend via
`Intl.DateTimeFormat('fa-IR-u-ca-persian')`; the API speaks ISO dates.

## 3. Frontend

### 3.1 Stack
React + TypeScript + Vite, Tailwind CSS v4, shadcn/ui (Radix), TanStack Query,
React Hook Form + Zod, vite-plugin-pwa, React Router (only extra routing dep),
`@fontsource-variable/vazirmatn` (self-hosted font). No global state library: cart
uses a React context persisted to localStorage.

### 3.2 Layout
```
frontend/src/
  app/            # router, QueryClient, error boundary
  components/ui/  # shadcn components (generated, lightly themed)
  components/     # shared: Money, StatusBadge, EmptyState, PageHeader, ...
  features/
    menu/ cart/ order-tracking/                  # public bundle
    admin/{auth,dashboard,orders,catalog,inventory,purchases,expenses,reports,users,push}
  lib/            # api.ts (fetch + CSRF + error mapping), money.ts, date.ts, errors-fa.ts
  styles/tokens.css
```
Public and admin routes are **code-split**; the customer bundle never loads admin code.

### 3.3 PWA
Single manifest, `start_url: /admin`, `scope: /`, `dir: rtl`, `lang: fa`. Service worker
precaches the app shell; API calls are network-only (no stale prices or order states).
The SW handles `push` and `notificationclick` (opens `/admin/inventory?filter=low`).

### 3.4 Design tokens (`styles/tokens.css`)
| Token | Light | Use |
|-------|-------|-----|
| `--background` | `#FBF7F2` cream | page |
| `--surface` | `#FFFFFF` | cards |
| `--foreground` | `#2B1E16` espresso | text (contrast 15:1 on cream) |
| `--muted` / `--muted-foreground` | `#F1E8DD` / `#6E5B4D` | secondary surfaces / text (≥ 4.5:1) |
| `--primary` / `--primary-foreground` | `#6B4226` coffee / `#FFF8F0` | main actions |
| `--accent` | `#B5835A` caramel | highlights (not for text) |
| `--border` | `#E4D6C5` | dividers |
| `--success` `--warning` `--danger` | `#2F6B4F` `#9A6212` `#B3261E` | states (all ≥ 4.5:1 on white) |
| `--radius` | `12px` | cards, buttons |
| spacing | 4 px scale (`1`=4px) | Tailwind default |
| type | Vazirmatn; 14/16/18/22/28 px | body 16 px minimum on mobile |

Order status colors: PENDING=warning, PREPARING=primary, READY=success, COMPLETED=muted, CANCELLED=danger — always paired with a text label, never color alone.
RTL: use logical utilities only (`ms-/me-/ps-/pe-/start-/end-`); numbers rendered with `fa-IR` digits.

## 4. Deployment architecture (pending server inspection)

Target is the existing cPanel/CloudLinux host. The app needs: a persistent process,
a reverse proxy from `rokh.tiacafe.ir` to `127.0.0.1:<port>`, and PostgreSQL ≥ 14.
These are **not guaranteed** on shared hosting; see
[IMPLEMENTATION-PLAN.md §4 Decision D1](IMPLEMENTATION-PLAN.md#4-critical-decisions).

Build artifact: `rokh` Linux amd64 static binary with embedded SPA + migrations.
Runtime layout (proposal):
```
~/rokh/
  bin/rokh, bin/rokh.prev        # rollback = swap and restart
  .env                           # 0600, not in git
  uploads/                       # product images (outside public_html)
  backups/                       # pg_dump + uploads tarballs, 14-day retention, copied off-server
```
Deploy procedure (manual, owner-approved): backup → upload binary → `rokh migrate up`
→ restart → health check → keep previous binary. Migrations are forward-only and
must be backward-compatible with the previous binary for one release.

## 5. Observability
- `slog` JSON logs to stdout/file with request id, route, status, latency; never log
  passwords, tokens, cookies, or full push endpoints.
- `GET /healthz` (process up) and `GET /readyz` (DB reachable).

## 6. Explicitly rejected
Microservices, Kubernetes, Redis, message brokers, GraphQL, ORMs, WebSockets/SSE,
multi-tenancy, event sourcing, generic CRUD frameworks.
