# Database — Rokh Tia Café

PostgreSQL **≥ 16** (dev/CI: 17). PostgreSQL 14 reaches end of life in November 2026 and
15 in November 2027, so neither is accepted for a new deployment (review F14).
Migrations: goose, `backend/db/migrations`, forward-only. Diagram: [ERD.md](ERD.md).
Review traceability: changes from the architecture review are tagged *(Fn)*; see
[REVIEW-RESPONSE.md](REVIEW-RESPONSE.md).

## 1. Conventions

- Primary keys: `bigint GENERATED ALWAYS AS IDENTITY` (sessions use the token hash).
- Money: `bigint` **rials**, column suffix `_rial`. No `numeric`/`float` for stored money.
  Selling prices are multiples of 10 rials (whole tomans).
- Quantities: `integer`.
- Enumerations: `text` + `CHECK (... IN (...))` (easy to extend with a migration; sqlc maps to string types).
- Timestamps: `timestamptz`, default `now()`. Business dates: `date` in Asia/Tehran.
- Financial and inventory history is **never deleted**: corrections are the explicit
  operations in [WORKFLOWS.md §6](WORKFLOWS.md#6-supported-corrections). Enforced by triggers and privileges (§6).
- Every mutating admin action writes an `audit_logs` row in the same transaction.
- **NULL-safe CHECKs** *(F6)*: a CHECK passes when its expression is NULL, so every
  "required when X" rule is written with explicit `IS NOT NULL`
  (e.g. `voided_at IS NULL OR (void_reason IS NOT NULL AND btrim(void_reason) <> '')`), never
  `char_length(col) > 0` alone. Length limits on optional columns (`char_length(x) <= n`)
  intentionally allow NULL.

## 2. Tables (14)

The brief proposed 13 tables. **One addition: `sessions`**, required for server-side session
revocation, idle timeout, and per-session CSRF tokens (Decision D3). The review corrections
add columns only; the table count stays 14.

### 2.1 users
```sql
CREATE TABLE users (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  username      text NOT NULL UNIQUE CHECK (username ~ '^[a-z0-9_.]{3,32}$'),
  display_name  text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 60),
  password_hash text NOT NULL,                    -- argon2id PHC string
  role          text NOT NULL CHECK (role IN ('ADMIN','OPERATOR')),
  is_active     boolean NOT NULL DEFAULT true,
  last_login_at timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
```
App rule (INV-16): at least one active ADMIN. Every role/active change takes
`pg_advisory_xact_lock(LOCK_ADMIN_MEMBERSHIP)` first, then counts active admins, so two
concurrent demotions cannot both succeed *(F11)*.

### 2.2 sessions *(added)*
```sql
CREATE TABLE sessions (
  token_hash   bytea PRIMARY KEY CHECK (length(token_hash) = 32),  -- SHA-256 of cookie token
  user_id      bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf_token   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  ip           inet,
  user_agent   text
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
```

### 2.3 categories
```sql
CREATE TABLE categories (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name       text NOT NULL UNIQUE CHECK (char_length(name) BETWEEN 1 AND 60),
  sort_order integer NOT NULL DEFAULT 0,
  is_active  boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
```

### 2.4 products
```sql
CREATE TABLE products (
  id                       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  category_id              bigint NOT NULL REFERENCES categories(id) ON DELETE RESTRICT,
  name                     text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
  description              text CHECK (char_length(description) <= 300),
  price_rial               bigint NOT NULL CHECK (price_rial > 0 AND price_rial % 10 = 0),
  inventory_mode           text NOT NULL CHECK (inventory_mode IN ('TRACKED','UNTRACKED')),
  low_stock_threshold      integer CHECK (low_stock_threshold >= 0),
  estimated_unit_cost_rial bigint CHECK (estimated_unit_cost_rial >= 0),   -- (F8) UNTRACKED only, optional
  is_active                boolean NOT NULL DEFAULT true,   -- false = hidden from menu (soft delete)
  is_available             boolean NOT NULL DEFAULT true,   -- false = shown as sold out
  image_path               text,                            -- relative path under UPLOAD_DIR
  sort_order               integer NOT NULL DEFAULT 0,
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now(),
  UNIQUE (category_id, name),
  CHECK (inventory_mode = 'TRACKED'   OR low_stock_threshold IS NULL),
  CHECK (inventory_mode = 'UNTRACKED' OR estimated_unit_cost_rial IS NULL)
);
CREATE INDEX products_category_idx ON products (category_id, sort_order);
```
App rules: products are never deleted. Switching `TRACKED → UNTRACKED` is allowed only
when `on_hand = 0 AND reserved = 0`. Switching to `TRACKED` creates the balance row.
Price changes do not touch existing orders (§4) and are protected from silent customer
impact by the expected-price check in order creation *(F13)*.

### 2.5 inventory_balances
One row per TRACKED product (created with the product or on mode switch).
```sql
CREATE TABLE inventory_balances (
  product_id            bigint PRIMARY KEY REFERENCES products(id) ON DELETE RESTRICT,
  on_hand               integer NOT NULL DEFAULT 0 CHECK (on_hand >= 0),
  reserved              integer NOT NULL DEFAULT 0 CHECK (reserved >= 0),
  inventory_value_rial  bigint  NOT NULL DEFAULT 0 CHECK (inventory_value_rial >= 0),
  last_unit_cost_rial   bigint  CHECK (last_unit_cost_rial >= 0),   -- last purchase cost; pre-fill only
  low_stock_alerted_at  timestamptz,      -- non-null while a low-stock episode is active
  updated_at            timestamptz NOT NULL DEFAULT now(),
  CHECK (reserved <= on_hand),
  CHECK (on_hand > 0 OR inventory_value_rial = 0)
);
```
`available = on_hand - reserved` (computed, never stored).
Average unit cost = `inventory_value_rial / on_hand` (computed; see §5).

### 2.6 purchase_entries
```sql
CREATE TABLE purchase_entries (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  product_id       bigint NOT NULL REFERENCES products(id),
  quantity         integer NOT NULL CHECK (quantity > 0),
  unit_cost_rial   bigint  NOT NULL CHECK (unit_cost_rial >= 0),
  total_cost_rial  bigint  NOT NULL,
  supplier         text CHECK (char_length(supplier) <= 80),
  purchased_on     date NOT NULL,
  note             text CHECK (char_length(note) <= 300),
  idempotency_key  uuid NOT NULL UNIQUE,
  created_by       bigint NOT NULL REFERENCES users(id),
  created_at       timestamptz NOT NULL DEFAULT now(),
  voided_at        timestamptz,
  voided_by        bigint REFERENCES users(id),
  void_reason      text,
  CHECK (total_cost_rial = quantity * unit_cost_rial),
  CHECK ((voided_at IS NULL) = (voided_by IS NULL)),
  CHECK ((voided_at IS NULL AND void_reason IS NULL) OR
         (voided_at IS NOT NULL AND void_reason IS NOT NULL AND btrim(void_reason) <> ''))   -- (F6)
);
CREATE INDEX purchase_entries_product_idx ON purchase_entries (product_id, purchased_on DESC);
```
Only TRACKED products can be purchased here. Ingredients for UNTRACKED drinks are **expenses**.
Void rule *(F4)*: see §5.1 — exact reversal only when nothing has left stock since the purchase.

### 2.7 inventory_movements
Append-only ledger of every `on_hand` change. Reservations are *not* movements (they are
derivable from orders and do not change physical stock).
```sql
CREATE TABLE inventory_movements (
  id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  product_id        bigint NOT NULL REFERENCES products(id),
  movement_type     text NOT NULL CHECK (movement_type IN
                      ('PURCHASE','PURCHASE_VOID','SALE','WASTE','ADJUSTMENT_IN','ADJUSTMENT_OUT')),
  reason_code       text CHECK (reason_code IN
                      ('OPENING_BALANCE','COUNT_CORRECTION','RETURN_TO_STOCK','DAMAGED','EXPIRED')),  -- (F10)
  quantity_delta    integer NOT NULL CHECK (quantity_delta <> 0),
  value_delta_rial  bigint  NOT NULL,
  on_hand_after     integer NOT NULL CHECK (on_hand_after >= 0),
  value_after_rial  bigint  NOT NULL CHECK (value_after_rial >= 0),
  order_id          bigint REFERENCES orders(id),
  purchase_entry_id bigint REFERENCES purchase_entries(id),
  note              text CHECK (char_length(note) <= 300),
  actor_user_id     bigint REFERENCES users(id),
  idempotency_key   uuid UNIQUE,                       -- set for manual adjustments
  created_at        timestamptz NOT NULL DEFAULT now(),
  CHECK (
    (movement_type IN ('PURCHASE','ADJUSTMENT_IN') AND quantity_delta > 0 AND value_delta_rial >= 0) OR
    (movement_type IN ('SALE','WASTE','PURCHASE_VOID','ADJUSTMENT_OUT') AND quantity_delta < 0 AND value_delta_rial <= 0)
  ),
  CHECK ((movement_type IN ('SALE','WASTE')) = (order_id IS NOT NULL)),
  CHECK ((movement_type IN ('PURCHASE','PURCHASE_VOID')) = (purchase_entry_id IS NOT NULL)),
  -- (F6/F10) reason codes are mandatory for adjustments and forbidden elsewhere
  CHECK (
    (movement_type = 'ADJUSTMENT_IN'  AND reason_code IS NOT NULL AND reason_code IN ('OPENING_BALANCE','COUNT_CORRECTION','RETURN_TO_STOCK')) OR
    (movement_type = 'ADJUSTMENT_OUT' AND reason_code IS NOT NULL AND reason_code IN ('COUNT_CORRECTION','DAMAGED','EXPIRED')) OR
    (movement_type NOT IN ('ADJUSTMENT_IN','ADJUSTMENT_OUT') AND reason_code IS NULL)
  ),
  CHECK (movement_type NOT IN ('ADJUSTMENT_IN','ADJUSTMENT_OUT') OR (note IS NOT NULL AND btrim(note) <> ''))
);
CREATE INDEX inventory_movements_product_idx ON inventory_movements (product_id, id DESC);
CREATE UNIQUE INDEX inventory_movements_order_once ON inventory_movements (order_id, product_id, movement_type)
  WHERE order_id IS NOT NULL;                         -- one SALE / one WASTE per order line
CREATE UNIQUE INDEX inventory_movements_purchase_once ON inventory_movements (purchase_entry_id, movement_type)
  WHERE purchase_entry_id IS NOT NULL;                -- one PURCHASE and at most one PURCHASE_VOID
```

### 2.8 orders
```sql
CREATE TABLE orders (
  id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  business_date       date    NOT NULL,                  -- Asia/Tehran date at creation
  display_number      integer NOT NULL CHECK (display_number > 0),
  tracking_token_hash bytea   NOT NULL UNIQUE CHECK (length(tracking_token_hash) = 32),  -- (F11) SHA-256
  idempotency_key     uuid    NOT NULL UNIQUE,
  request_hash        bytea   NOT NULL CHECK (length(request_hash) = 32),
  client_key          bytea   NOT NULL CHECK (length(client_key) = 32),  -- (F11) HMAC(APP_SECRET_KEY, client IP)
  customer_label      text CHECK (char_length(customer_label) <= 40),
  customer_note       text CHECK (char_length(customer_note) <= 200),
  status              text NOT NULL DEFAULT 'PENDING' CHECK (status IN
                        ('PENDING','PREPARING','READY','COMPLETED','CANCELLED')),
  total_rial          bigint NOT NULL CHECK (total_rial > 0),
  -- (F1/F9) money held for this order; maintained under the order row lock
  paid_rial           bigint NOT NULL DEFAULT 0,
  refunded_rial       bigint NOT NULL DEFAULT 0,
  payment_status      text GENERATED ALWAYS AS (
                        CASE WHEN refunded_rial > 0         THEN 'REFUNDED'
                             WHEN paid_rial = total_rial    THEN 'PAID'
                             ELSE 'UNPAID' END) STORED,
  expires_at          timestamptz NOT NULL,              -- acceptance deadline while PENDING (F2)
  cancel_reason       text CHECK (cancel_reason IN ('CUSTOMER','STAFF','EXPIRED')),
  cancel_note         text CHECK (char_length(cancel_note) <= 200),
  created_at          timestamptz NOT NULL DEFAULT now(),
  accepted_at         timestamptz,
  ready_at            timestamptz,
  completed_at        timestamptz,
  cancelled_at        timestamptz,
  accepted_by         bigint REFERENCES users(id),
  completed_by        bigint REFERENCES users(id),
  cancelled_by        bigint REFERENCES users(id),       -- NULL for CUSTOMER/EXPIRED
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_date, display_number),
  CHECK (expires_at > created_at),
  CHECK ((status = 'CANCELLED') = (cancelled_at IS NOT NULL)),
  CHECK ((status = 'CANCELLED') = (cancel_reason IS NOT NULL)),
  CHECK ((status = 'COMPLETED') = (completed_at IS NOT NULL)),
  CHECK (status NOT IN ('PREPARING','READY','COMPLETED') OR accepted_at IS NOT NULL),
  CHECK (status NOT IN ('READY','COMPLETED') OR ready_at IS NOT NULL),
  CHECK (accepted_at IS NULL OR accepted_at < expires_at),                    -- (F2) accepted before deadline
  CHECK (cancel_reason IS DISTINCT FROM 'EXPIRED' OR accepted_at IS NULL),   -- (F2) only never-accepted orders expire
  -- (F1/F9) payment amounts
  CHECK (paid_rial >= 0 AND paid_rial <= total_rial),
  CHECK (refunded_rial = 0 OR refunded_rial = paid_rial),                     -- refunds are always full
  CHECK (status <> 'PENDING' OR paid_rial = 0),                               -- no money before acceptance
  CHECK (status <> 'CANCELLED' OR paid_rial = refunded_rial)                  -- no money kept on a cancelled order
);
CREATE INDEX orders_open_idx ON orders (status, created_at) WHERE status IN ('PENDING','PREPARING','READY');
CREATE INDEX orders_completed_idx ON orders (completed_at) WHERE status = 'COMPLETED';
CREATE INDEX orders_expiry_idx ON orders (expires_at) WHERE status = 'PENDING';
CREATE INDEX orders_client_pending_idx ON orders (client_key) WHERE status = 'PENDING';
```
`payment_status` is a generated column, so it cannot disagree with `paid_rial`/`refunded_rial`.
`paid_rial`/`refunded_rial` must equal the sums of non-void `payments` rows (INV-11, app-enforced
under the order row lock, verified by the reconciliation check).

### 2.9 order_items
```sql
CREATE TABLE order_items (
  id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  order_id             bigint NOT NULL REFERENCES orders(id),
  product_id           bigint NOT NULL REFERENCES products(id),
  product_name         text   NOT NULL,                   -- snapshot at creation
  inventory_mode       text   NOT NULL CHECK (inventory_mode IN ('TRACKED','UNTRACKED')),  -- snapshot
  unit_price_rial      bigint NOT NULL CHECK (unit_price_rial > 0),                       -- snapshot
  quantity             integer NOT NULL CHECK (quantity BETWEEN 1 AND 20),
  line_total_rial      bigint NOT NULL,
  cogs_rial            bigint CHECK (cogs_rial >= 0),     -- actual cost; set once at completion
  estimated_cost_rial  bigint CHECK (estimated_cost_rial >= 0),  -- (F8) UNTRACKED: qty × estimate, set at completion/waste
  wasted_quantity      integer NOT NULL DEFAULT 0,        -- (F7) set at cancel-after-preparation
  UNIQUE (order_id, product_id),
  CHECK (line_total_rial = unit_price_rial * quantity),
  CHECK (wasted_quantity BETWEEN 0 AND quantity),
  CHECK (inventory_mode = 'UNTRACKED' OR estimated_cost_rial IS NULL),
  CHECK (inventory_mode = 'TRACKED' OR cogs_rial IS NULL OR cogs_rial = 0)
);
```
App-enforced (and tested): `orders.total_rial = Σ line_total_rial`;
`cogs_rial IS NOT NULL` ⇔ parent COMPLETED; `wasted_quantity > 0` ⇒ parent CANCELLED; max 15 lines.

### 2.10 payments
```sql
CREATE TABLE payments (
  id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  order_id             bigint NOT NULL REFERENCES orders(id),
  kind                 text NOT NULL CHECK (kind IN ('PAYMENT','REFUND')),
  method               text NOT NULL CHECK (method IN ('CASH','CARD_TERMINAL')),
  amount_rial          bigint NOT NULL CHECK (amount_rial > 0),
  refund_of_payment_id bigint UNIQUE REFERENCES payments(id),   -- (F9) each REFUND returns exactly one PAYMENT
  note                 text CHECK (char_length(note) <= 200),
  idempotency_key      uuid UNIQUE,                             -- required for PAYMENT; NULL for REFUND (refunds are state-idempotent)
  created_by           bigint NOT NULL REFERENCES users(id),
  created_at           timestamptz NOT NULL DEFAULT now(),
  voided_at            timestamptz,
  voided_by            bigint REFERENCES users(id),
  void_reason          text,
  CHECK ((kind = 'REFUND') = (refund_of_payment_id IS NOT NULL)),
  CHECK (kind = 'REFUND' OR idempotency_key IS NOT NULL),
  CHECK (kind = 'PAYMENT' OR voided_at IS NULL),                   -- (F10) refunds cannot be voided
  CHECK ((voided_at IS NULL) = (voided_by IS NULL)),
  CHECK ((voided_at IS NULL AND void_reason IS NULL) OR
         (voided_at IS NOT NULL AND void_reason IS NOT NULL AND btrim(void_reason) <> ''))   -- (F6)
);
CREATE INDEX payments_order_idx ON payments (order_id);
CREATE INDEX payments_created_idx ON payments (created_at);
```
A REFUND copies the `method` and `amount_rial` of the PAYMENT it returns (app-enforced,
tested). Refunds are state-idempotent: a repeated refund/cancel finds the order already refunded
under the order lock, and `UNIQUE (refund_of_payment_id)` makes a second refund of the same payment impossible. Full rules: [WORKFLOWS.md §3](WORKFLOWS.md#3-payments).

### 2.11 expenses
```sql
CREATE TABLE expenses (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  category        text NOT NULL CHECK (category IN
                    ('INGREDIENTS','SUPPLIES','RENT','UTILITIES','SALARY','MAINTENANCE','OTHER')),
  amount_rial     bigint NOT NULL CHECK (amount_rial > 0),
  expense_date    date NOT NULL,
  description     text NOT NULL CHECK (char_length(description) BETWEEN 1 AND 200),
  idempotency_key uuid NOT NULL UNIQUE,
  created_by      bigint NOT NULL REFERENCES users(id),
  created_at      timestamptz NOT NULL DEFAULT now(),
  voided_at       timestamptz,
  voided_by       bigint REFERENCES users(id),
  void_reason     text,
  CHECK ((voided_at IS NULL) = (voided_by IS NULL)),
  CHECK ((voided_at IS NULL AND void_reason IS NULL) OR
         (voided_at IS NOT NULL AND void_reason IS NOT NULL AND btrim(void_reason) <> ''))   -- (F6)
);
CREATE INDEX expenses_date_idx ON expenses (expense_date) WHERE voided_at IS NULL;
```

### 2.12 push_subscriptions
```sql
CREATE TABLE push_subscriptions (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id         bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  endpoint        text NOT NULL UNIQUE CHECK (endpoint LIKE 'https://%' AND char_length(endpoint) <= 2048),
  p256dh          text NOT NULL,
  auth            text NOT NULL,
  user_agent      text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  last_success_at timestamptz,
  failure_count   integer NOT NULL DEFAULT 0
);
```
The endpoint host must match the push-service allowlist (SECURITY §12) — validated in the
app on subscribe **and** before every send *(F11)*.

### 2.13 notification_events
Outbox: one row per (logical event × subscription). Claimed with short leases *(F5)*.
```sql
CREATE TABLE notification_events (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  event_type      text NOT NULL CHECK (event_type IN ('LOW_STOCK','TEST')),
  dedup_key       text NOT NULL,                  -- e.g. LOW_STOCK:42:1729338000
  subscription_id bigint REFERENCES push_subscriptions(id) ON DELETE SET NULL,
  product_id      bigint REFERENCES products(id),
  payload         jsonb NOT NULL,
  status          text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','SENDING','SENT','FAILED','GONE')),
  attempts        integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  claim_token     uuid,
  lease_until     timestamptz,
  last_error      text CHECK (char_length(last_error) <= 300),
  created_at      timestamptz NOT NULL DEFAULT now(),
  sent_at         timestamptz,
  UNIQUE (dedup_key, subscription_id),
  CHECK ((status = 'SENDING') = (claim_token IS NOT NULL AND lease_until IS NOT NULL)),
  CHECK ((status = 'SENT') = (sent_at IS NOT NULL))
);
CREATE INDEX notification_events_due_idx ON notification_events (next_attempt_at) WHERE status = 'PENDING';
CREATE INDEX notification_events_lease_idx ON notification_events (lease_until) WHERE status = 'SENDING';
```

### 2.14 audit_logs
```sql
CREATE TABLE audit_logs (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  actor_user_id bigint REFERENCES users(id),      -- NULL = customer or system job
  actor_kind    text NOT NULL CHECK (actor_kind IN ('USER','CUSTOMER','SYSTEM')),
  action        text NOT NULL,                    -- e.g. order.complete, payment.void, product.price_change
  entity_type   text NOT NULL,
  entity_id     text NOT NULL,
  details       jsonb NOT NULL DEFAULT '{}',      -- before/after for changed fields; never secrets
  ip            inet,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CHECK ((actor_kind = 'USER') = (actor_user_id IS NOT NULL))
);
CREATE INDEX audit_logs_entity_idx ON audit_logs (entity_type, entity_id, id DESC);
CREATE INDEX audit_logs_created_idx ON audit_logs (created_at DESC);
```

## 3. Invariants

"DB" = enforced by PostgreSQL even if application code is wrong. "App" = enforced by
transaction logic under a lock, with a reconciliation test and the `rokh check` command *(F6)*.

| ID | Invariant | Enforced by |
|----|-----------|-------------|
| INV-1 | `0 ≤ reserved ≤ on_hand` for every tracked product | **DB** CHECK |
| INV-2 | No negative stock; `inventory_value_rial ≥ 0`; zero stock ⇒ zero value | **DB** CHECK |
| INV-3 | `reserved` = Σ (qty) of TRACKED lines in PENDING/PREPARING/READY orders | **App** — changed only inside order txs holding the order lock; `rokh check` |
| INV-4 | `on_hand` = Σ `quantity_delta`; `inventory_value_rial` = Σ `value_delta_rial` (per product) | **App** — ledger row in same tx; `on_hand_after`/`value_after_rial` **DB**-checked ≥ 0; `rokh check` |
| INV-5 | A completed order deducts each line exactly once | **DB** unique `inventory_movements_order_once` + **App** order lock |
| INV-6 | Order prices are server-computed and frozen at creation | **App** (no client prices used) + **DB** triggers forbid updating snapshot columns |
| INV-7 | `line_total = unit_price × qty` | **DB** CHECK |
| INV-7b | `orders.total_rial = Σ line_total_rial` | **App** + `rokh check` |
| INV-8 | COGS set once, only at completion | **App** + **DB** trigger (NULL → value once) |
| INV-9 | Only legal status transitions; accept only before `expires_at` | **App** (order lock) + **DB** CHECKs on timestamps/deadline |
| INV-10 | No money held on PENDING or CANCELLED orders; never overpaid; refunds are full | **DB** CHECKs on `paid_rial`/`refunded_rial` |
| INV-11 | `paid_rial`/`refunded_rial` = sums of non-void PAYMENT/REFUND rows | **App** (order lock) + `rokh check` |
| INV-11b | `payment_status` consistent with amounts | **DB** generated column |
| INV-12 | Financial/inventory/audit rows never deleted; only void columns may change, once | **DB** triggers + runtime role has no DELETE (§6) |
| INV-13 | Same idempotency key + same body ⇒ same result, even concurrently; different body ⇒ 409 | **DB** UNIQUE + **App** global creation lock + `request_hash` |
| INV-14 | Daily order numbers unique per business date | **DB** UNIQUE + **App** creation lock |
| INV-15 | One low-stock episode per product; one notification per subscription per episode | **App** `low_stock_alerted_at` + **DB** UNIQUE `(dedup_key, subscription_id)` |
| INV-16 | At least one active ADMIN | **App** under `LOCK_ADMIN_MEMBERSHIP` |
| INV-17 | Open PENDING orders ≤ `ORDER_MAX_PENDING` (global) and ≤ `ORDER_MAX_PENDING_PER_CLIENT` | **App** under the global creation lock *(F3)* |
| INV-18 | Wasted quantity ≤ ordered quantity; WASTE movement once per line | **DB** CHECK + unique index *(F7)* |

### 3.1 Lock order
To avoid deadlocks every transaction acquires locks in this order and never in reverse:
1. Advisory locks (`LOCK_ORDER_CREATE`, `LOCK_ADMIN_MEMBERSHIP`) — at most one per tx.
2. `orders` row (`SELECT … FOR UPDATE`) — at most one order per tx.
3. `products` rows `FOR SHARE` (order creation only), ascending id.
4. `inventory_balances` rows, ascending `product_id`.
5. `payments` rows of that order.

## 4. Order money & snapshot rules

- **Selling price** is snapshotted into `order_items.unit_price_rial` at **creation**. The
  customer's client sends the unit prices it displayed; creation fails with `409 PRICE_CHANGED`
  if any differ from the current price, so the customer is never charged a price they did not
  see *(F13)*. The server never uses client prices for calculation.
- **Product name and inventory mode** are snapshotted at creation.
- **Actual cost** (`cogs_rial`) is snapshotted at **completion** — the moment stock leaves.
- **Estimated cost** of UNTRACKED lines (`estimated_cost_rial = qty × products.estimated_unit_cost_rial`)
  is snapshotted at completion (or for wasted quantity at cancellation); NULL if no estimate is set *(F8)*.
- PENDING/PREPARING/READY orders carry no cost. CANCELLED orders never get `cogs_rial`.

## 5. Cost model

### 5.1 TRACKED products — moving weighted average (value-based)
The balance stores total **inventory value** rather than an average unit cost, which
avoids rounding drift:

- **Purchase** of `q` at `c`: `on_hand += q`, `value += q × c`, `last_unit_cost = c`.
- **Consumption** of `q` (SALE, WASTE, ADJUSTMENT_OUT): `cost = round_half_up(value × q / on_hand)`;
  `on_hand -= q`, `value -= cost`. When `q = on_hand`, `cost = value` exactly, so value
  returns to 0 with the stock. Computed in SQL with `numeric` intermediates to avoid overflow.
- **Adjustment in** of `q`: admin supplies `unit_cost_rial` (UI pre-fills the current
  average, or `last_unit_cost_rial` if stock is 0). `value += q × unit_cost`.
- **Purchase void** *(F4)* — exact reversal only. Allowed when **all** hold:
  1. the purchase is not already voided;
  2. no outflow movement (`SALE`, `WASTE`, `ADJUSTMENT_OUT`, `PURCHASE_VOID`) exists for the
     product with `id >` the purchase's `PURCHASE` movement id;
  3. `on_hand − reserved ≥ q`.

  Then `on_hand -= q`, `value -= q × c` exactly. Because only inflows happened since the
  purchase, `value ≥ q × c` is guaranteed and no historical COGS was computed from the
  purchase's cost, so history stays correct. **No clamping.** Otherwise the void is rejected
  with `409 PURCHASE_ALREADY_CONSUMED`; the admin corrects stock with explicit
  `COUNT_CORRECTION` adjustments instead ([WORKFLOWS.md §6](WORKFLOWS.md#6-supported-corrections)).
- Average unit cost shown in UI = `value / on_hand` (display only).

### 5.2 UNTRACKED products (made-to-order drinks)
No balance, no reservation, `cogs_rial = 0`. Ingredient purchases (beans, milk, syrups,
cups) are recorded as **expenses** (`INGREDIENTS`, `SUPPLIES`).

Optional `estimated_unit_cost_rial` per product *(F8)* feeds a separate **estimated product
margin** view only. It never enters COGS, gross profit, or the operating result — the
ingredient spend is already counted once through expenses, so using estimates in the
operating result would double count.

## 6. Protection: triggers and database roles

### 6.1 Roles
| Role | Used by | Privileges |
|------|---------|-----------|
| `rokh_owner` | `rokh migrate` only (`MIGRATION_DATABASE_URL`) | Owns schema `rokh` and all objects; DDL. Never used by the running server. |
| `rokh_app` | `rokh serve` (`DATABASE_URL`) | `USAGE` on schema; `SELECT, INSERT, UPDATE` on all tables; `DELETE` **only** on `sessions`, `push_subscriptions`, `notification_events`; `USAGE` on identity sequences. No `TRUNCATE`, no DDL, not table owner, no `CREATE` on schema. |

Because `rokh_app` does not own the tables it cannot `ALTER TABLE … DISABLE TRIGGER`, and
it is not superuser so it cannot set `session_replication_role`. Grants are written in a
migration with `ALTER DEFAULT PRIVILEGES FOR ROLE rokh_owner`. Neither role is superuser
or has `CREATEROLE`/`CREATEDB`. If the host cannot provide two roles, see owner decision O2.

### 6.2 Triggers (initial migration)
- `forbid_delete()` BEFORE DELETE on `orders`, `order_items`, `payments`, `expenses`,
  `purchase_entries`, `inventory_movements`, `audit_logs`, `products`, `inventory_balances`.
- `forbid_update()` BEFORE UPDATE on `inventory_movements`, `audit_logs`.
- `order_items_guard()` BEFORE UPDATE on `order_items`: only `cogs_rial` / `estimated_cost_rial`
  NULL → value (once) and `wasted_quantity` 0 → n (once) may change; snapshots are immutable.
- `orders_guard()` BEFORE UPDATE on `orders`: `total_rial`, `idempotency_key`, `request_hash`,
  `tracking_token_hash`, `client_key`, `business_date`, `display_number`, `created_at` immutable;
  `status` may only follow the legal transition table (defence in depth for INV-9).
- `void_only_update()` BEFORE UPDATE on `payments`, `expenses`, `purchase_entries`:
  only `voided_at`, `voided_by`, `void_reason` may change, and only from NULL.
- `set_updated_at()` on tables with `updated_at`.

## 7. Reporting queries (definitions)

All grouped by Asia/Tehran business date of the relevant timestamp. Revenue policy is in
[WORKFLOWS.md §3.1](WORKFLOWS.md#31-revenue-receivables-and-cash-policy) *(F9)*.

| Metric | Definition |
|--------|------------|
| Gross sales | Σ `total_rial` of orders COMPLETED in range (by `completed_at`) |
| Sales refunds | Σ REFUND `amount_rial` (by refund `created_at`) whose order is COMPLETED |
| **Revenue (net)** | Gross sales − Sales refunds |
| Orders | count of COMPLETED orders |
| Average order value | Gross sales ÷ Orders (integer rials, rounded) |
| **COGS** | Σ `cogs_rial` of lines of orders COMPLETED in range (refunds do not reverse COGS — goods were consumed) |
| **Gross profit** | Revenue − COGS |
| Inventory losses | Σ −`value_delta_rial` of `ADJUSTMENT_OUT` and `WASTE` in range |
| Inventory gains | Σ `value_delta_rial` of `ADJUSTMENT_IN` with reason `COUNT_CORRECTION` or `RETURN_TO_STOCK` (`OPENING_BALANCE` has no P&L effect) |
| **Expenses** | Σ non-void `expenses.amount_rial` by `expense_date` |
| **Estimated operating result** | Gross profit − Inventory losses + Inventory gains − Expenses |
| Purchases (info only) | Σ non-void `purchase_entries.total_cost_rial` — not subtracted (becomes COGS when sold) |
| Cash / card collected | Σ non-void PAYMENT − Σ REFUND by method, by `created_at` |
| Prepayment refunds (info) | Σ REFUND whose order is CANCELLED — cash flow only, never revenue |
| Receivables (point in time) | Σ (`total_rial − paid_rial`) of COMPLETED orders with `refunded_rial = 0` |
| Estimated product margin (info, F8) | Per product: revenue − (`cogs_rial` for TRACKED, Σ `estimated_cost_rial` for UNTRACKED); lines without estimate shown as "no estimate". Never added to the operating result. |
| Estimated waste (info, F7) | Σ `estimated_cost_rial` of UNTRACKED wasted quantities |

### 7.1 Accounting limitations (shown in the report UI footer)
1. Revenue is recognised on completion, not on payment; unpaid completed orders count as revenue and appear as receivables.
2. UNTRACKED items have zero actual COGS; their ingredient costs appear under Expenses. Estimated margins are management estimates only.
3. `OPENING_BALANCE` adjustments add stock at entered cost with no P&L effect.
4. No tax, depreciation, accruals, owner draws, write-offs, or bank reconciliation. This is a management estimate, **not** net profit or a tax report.
5. Expenses are recorded on the date entered, not amortised.
