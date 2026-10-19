# Database — Rokh Tia Café

PostgreSQL ≥ 14 (local dev: 17). Migrations: goose, `backend/db/migrations`, forward-only.
Diagram: [ERD.md](ERD.md).

## 1. Conventions

- Primary keys: `bigint GENERATED ALWAYS AS IDENTITY` (sessions use the token hash).
- Money: `bigint` **rials**, column suffix `_rial`. No `numeric`/`float` for stored money.
  Selling prices are multiples of 10 rials (whole tomans).
- Quantities: `integer`.
- Enumerations: `text` + `CHECK (... IN (...))` (easy to extend with a migration; sqlc maps to string types).
- Timestamps: `timestamptz`, default `now()`. Business dates: `date` in Asia/Tehran.
- Financial and inventory history is **never deleted**: corrections are voids/reversals.
  Enforced by triggers (§6).
- Every mutating admin action writes an `audit_logs` row in the same transaction.

## 2. Tables (14)

The brief proposed 13 tables. **One addition is proposed: `sessions`**, required for
server-side session revocation, idle timeout, and per-session CSRF tokens
(see Decision D3 in [IMPLEMENTATION-PLAN.md](IMPLEMENTATION-PLAN.md#4-critical-decisions)).
No other tables are added; idempotency keys live on the rows they protect.

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
App rule: the last active ADMIN cannot be deactivated or demoted.

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
  id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  category_id         bigint NOT NULL REFERENCES categories(id) ON DELETE RESTRICT,
  name                text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
  description         text CHECK (char_length(description) <= 300),
  price_rial          bigint NOT NULL CHECK (price_rial > 0 AND price_rial % 10 = 0),
  inventory_mode      text NOT NULL CHECK (inventory_mode IN ('TRACKED','UNTRACKED')),
  low_stock_threshold integer CHECK (low_stock_threshold >= 0),
  is_active           boolean NOT NULL DEFAULT true,   -- false = hidden from menu (soft delete)
  is_available        boolean NOT NULL DEFAULT true,   -- false = shown as sold out
  image_path          text,                            -- relative path under UPLOAD_DIR
  sort_order          integer NOT NULL DEFAULT 0,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (category_id, name),
  CHECK (inventory_mode = 'TRACKED' OR low_stock_threshold IS NULL)
);
CREATE INDEX products_category_idx ON products (category_id, sort_order);
```
App rules: products are never deleted. Switching `TRACKED → UNTRACKED` is allowed only
when `on_hand = 0 AND reserved = 0`. Switching to `TRACKED` creates the balance row.

### 2.5 inventory_balances
One row per TRACKED product (created with the product or on mode switch).
```sql
CREATE TABLE inventory_balances (
  product_id            bigint PRIMARY KEY REFERENCES products(id) ON DELETE RESTRICT,
  on_hand               integer NOT NULL DEFAULT 0 CHECK (on_hand >= 0),
  reserved              integer NOT NULL DEFAULT 0 CHECK (reserved >= 0),
  inventory_value_rial  bigint  NOT NULL DEFAULT 0 CHECK (inventory_value_rial >= 0),
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
  CHECK (voided_at IS NULL OR char_length(void_reason) > 0)
);
CREATE INDEX purchase_entries_product_idx ON purchase_entries (product_id, purchased_on DESC);
```
Only TRACKED products can be purchased here. Ingredients for UNTRACKED drinks are **expenses**.

### 2.7 inventory_movements
Append-only ledger of every `on_hand` change. Reservations are *not* movements (they are
derivable from orders and do not change physical stock).
```sql
CREATE TABLE inventory_movements (
  id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  product_id        bigint NOT NULL REFERENCES products(id),
  movement_type     text NOT NULL CHECK (movement_type IN
                      ('PURCHASE','PURCHASE_VOID','SALE','ADJUSTMENT_IN','ADJUSTMENT_OUT')),
  quantity_delta    integer NOT NULL CHECK (quantity_delta <> 0),
  value_delta_rial  bigint  NOT NULL,
  on_hand_after     integer NOT NULL CHECK (on_hand_after >= 0),
  value_after_rial  bigint  NOT NULL CHECK (value_after_rial >= 0),
  order_id          bigint REFERENCES orders(id),
  purchase_entry_id bigint REFERENCES purchase_entries(id),
  reason            text CHECK (char_length(reason) <= 300),
  actor_user_id     bigint REFERENCES users(id),
  idempotency_key   uuid UNIQUE,                       -- set for manual adjustments
  created_at        timestamptz NOT NULL DEFAULT now(),
  CHECK (
    (movement_type IN ('PURCHASE','ADJUSTMENT_IN') AND quantity_delta > 0 AND value_delta_rial >= 0) OR
    (movement_type IN ('SALE','PURCHASE_VOID','ADJUSTMENT_OUT') AND quantity_delta < 0 AND value_delta_rial <= 0)
  ),
  CHECK ((movement_type = 'SALE') = (order_id IS NOT NULL)),
  CHECK ((movement_type IN ('PURCHASE','PURCHASE_VOID')) = (purchase_entry_id IS NOT NULL)),
  CHECK (movement_type NOT IN ('ADJUSTMENT_IN','ADJUSTMENT_OUT') OR char_length(reason) > 0)
);
CREATE INDEX inventory_movements_product_idx ON inventory_movements (product_id, id DESC);
CREATE UNIQUE INDEX inventory_movements_sale_once  ON inventory_movements (order_id, product_id)
  WHERE movement_type = 'SALE';                       -- no double deduction per order line
CREATE UNIQUE INDEX inventory_movements_purchase_once ON inventory_movements (purchase_entry_id, movement_type)
  WHERE purchase_entry_id IS NOT NULL;                -- one PURCHASE and at most one PURCHASE_VOID
```

### 2.8 orders
```sql
CREATE TABLE orders (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  business_date    date    NOT NULL,                  -- Asia/Tehran date at creation
  display_number   integer NOT NULL CHECK (display_number > 0),
  tracking_token   text    NOT NULL UNIQUE CHECK (char_length(tracking_token) >= 22),
  idempotency_key  uuid    NOT NULL UNIQUE,
  request_hash     bytea   NOT NULL,                  -- SHA-256 of canonical request body
  customer_label   text CHECK (char_length(customer_label) <= 40),
  customer_note    text CHECK (char_length(customer_note) <= 200),
  status           text NOT NULL DEFAULT 'PENDING' CHECK (status IN
                     ('PENDING','PREPARING','READY','COMPLETED','CANCELLED')),
  payment_status   text NOT NULL DEFAULT 'UNPAID' CHECK (payment_status IN ('UNPAID','PAID','REFUNDED')),
  total_rial       bigint NOT NULL CHECK (total_rial > 0),
  expires_at       timestamptz NOT NULL,              -- only meaningful while PENDING
  cancel_reason    text CHECK (cancel_reason IN ('CUSTOMER','STAFF','EXPIRED')),
  cancel_note      text CHECK (char_length(cancel_note) <= 200),
  created_at       timestamptz NOT NULL DEFAULT now(),
  accepted_at      timestamptz,
  ready_at         timestamptz,
  completed_at     timestamptz,
  cancelled_at     timestamptz,
  accepted_by      bigint REFERENCES users(id),
  completed_by     bigint REFERENCES users(id),
  cancelled_by     bigint REFERENCES users(id),       -- NULL for CUSTOMER/EXPIRED
  updated_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_date, display_number),
  CHECK ((status = 'CANCELLED') = (cancelled_at IS NOT NULL)),
  CHECK ((status = 'CANCELLED') = (cancel_reason IS NOT NULL)),
  CHECK ((status = 'COMPLETED') = (completed_at IS NOT NULL)),
  CHECK (status NOT IN ('PREPARING','READY','COMPLETED') OR accepted_at IS NOT NULL),
  CHECK (status NOT IN ('READY','COMPLETED') OR ready_at IS NOT NULL),
  CHECK (NOT (status = 'CANCELLED' AND payment_status = 'PAID'))
);
CREATE INDEX orders_open_idx ON orders (status, created_at) WHERE status IN ('PENDING','PREPARING','READY');
CREATE INDEX orders_completed_idx ON orders (completed_at) WHERE status = 'COMPLETED';
CREATE INDEX orders_expiry_idx ON orders (expires_at) WHERE status = 'PENDING';
```

### 2.9 order_items
```sql
CREATE TABLE order_items (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  order_id         bigint NOT NULL REFERENCES orders(id),
  product_id       bigint NOT NULL REFERENCES products(id),
  product_name     text   NOT NULL,                   -- snapshot at creation
  inventory_mode   text   NOT NULL CHECK (inventory_mode IN ('TRACKED','UNTRACKED')),  -- snapshot
  unit_price_rial  bigint NOT NULL CHECK (unit_price_rial > 0),                       -- snapshot
  quantity         integer NOT NULL CHECK (quantity BETWEEN 1 AND 20),
  line_total_rial  bigint NOT NULL,
  cogs_rial        bigint CHECK (cogs_rial >= 0),     -- set exactly once, at completion
  UNIQUE (order_id, product_id),
  CHECK (line_total_rial = unit_price_rial * quantity)
);
```
App-enforced (and tested): `orders.total_rial = Σ order_items.line_total_rial`;
`cogs_rial IS NOT NULL` ⇔ parent order COMPLETED; max 15 lines per order.

### 2.10 payments
```sql
CREATE TABLE payments (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  order_id        bigint NOT NULL REFERENCES orders(id),
  kind            text NOT NULL CHECK (kind IN ('PAYMENT','REFUND')),
  method          text NOT NULL CHECK (method IN ('CASH','CARD_TERMINAL')),
  amount_rial     bigint NOT NULL CHECK (amount_rial > 0),
  note            text CHECK (char_length(note) <= 200),
  idempotency_key uuid NOT NULL UNIQUE,
  created_by      bigint NOT NULL REFERENCES users(id),
  created_at      timestamptz NOT NULL DEFAULT now(),
  voided_at       timestamptz,
  voided_by       bigint REFERENCES users(id),
  void_reason     text,
  CHECK ((voided_at IS NULL) = (voided_by IS NULL)),
  CHECK (voided_at IS NULL OR char_length(void_reason) > 0)
);
CREATE INDEX payments_order_idx ON payments (order_id);
CREATE INDEX payments_created_idx ON payments (created_at);
```
`orders.payment_status` is a cached projection recomputed in the same transaction as any
payment change (rules in [WORKFLOWS.md §3](WORKFLOWS.md#3-payments)).

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
  CHECK (voided_at IS NULL OR char_length(void_reason) > 0)
);
CREATE INDEX expenses_date_idx ON expenses (expense_date) WHERE voided_at IS NULL;
```

### 2.12 push_subscriptions
```sql
CREATE TABLE push_subscriptions (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id         bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  endpoint        text NOT NULL UNIQUE CHECK (endpoint LIKE 'https://%'),
  p256dh          text NOT NULL,
  auth            text NOT NULL,
  user_agent      text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  last_success_at timestamptz,
  failure_count   integer NOT NULL DEFAULT 0
);
```

### 2.13 notification_events
Outbox: one row per (logical event × subscription).
```sql
CREATE TABLE notification_events (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  event_type      text NOT NULL CHECK (event_type IN ('LOW_STOCK','TEST')),
  dedup_key       text NOT NULL,                  -- e.g. LOW_STOCK:42:1729338000
  subscription_id bigint REFERENCES push_subscriptions(id) ON DELETE SET NULL,
  product_id      bigint REFERENCES products(id),
  payload         jsonb NOT NULL,
  status          text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','SENT','FAILED','GONE')),
  attempts        integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_error      text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  sent_at         timestamptz,
  UNIQUE (dedup_key, subscription_id)
);
CREATE INDEX notification_events_due_idx ON notification_events (next_attempt_at) WHERE status = 'PENDING';
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
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_entity_idx ON audit_logs (entity_type, entity_id, id DESC);
CREATE INDEX audit_logs_created_idx ON audit_logs (created_at DESC);
```

## 3. Invariants

| ID | Invariant | Enforced by |
|----|-----------|-------------|
| INV-1 | `0 ≤ reserved ≤ on_hand` for every tracked product | CHECK |
| INV-2 | No negative stock; `inventory_value_rial ≥ 0`; zero stock ⇒ zero value | CHECK |
| INV-3 | `reserved` = Σ qty of TRACKED lines in orders with status PENDING/PREPARING/READY | Conditional UPDATEs in same tx as status change; reconciliation test + `rokh check inventory` command |
| INV-4 | `on_hand` = Σ `quantity_delta` of movements; `inventory_value_rial` = Σ `value_delta_rial` | Ledger row written in same tx as balance update; reconciliation test |
| INV-5 | A completed order deducts each line exactly once | Conditional `UPDATE … WHERE status='READY'` + unique index `inventory_movements_sale_once` |
| INV-6 | Order prices are server-computed and frozen at creation | Handler ignores client prices; `order_items` snapshot columns; no UPDATE path for them |
| INV-7 | `orders.total_rial = Σ line_total_rial`; `line_total = unit_price × qty` | CHECK (line) + tx logic + test (order) |
| INV-8 | COGS is set once at completion; never for non-completed orders | Set only inside `Complete` tx; test |
| INV-9 | Only legal status transitions (see WORKFLOWS) | Conditional UPDATE on expected from-status |
| INV-10 | A PAID order cannot be CANCELLED | CHECK + service rule |
| INV-11 | `payment_status` = f(non-void payments, refunds, total) | Recomputed in same tx; test |
| INV-12 | Financial/inventory/audit rows are never deleted; only void columns may change | Triggers (§6) + no DELETE queries |
| INV-13 | Same idempotency key + same body ⇒ same result; different body ⇒ 409 | UNIQUE key + `request_hash` comparison |
| INV-14 | Daily order numbers are unique per business date | UNIQUE + advisory lock |
| INV-15 | At most one active low-stock episode per product; one notification per subscription per episode | `low_stock_alerted_at` + UNIQUE `(dedup_key, subscription_id)` |
| INV-16 | At least one active ADMIN exists | Service rule (checked under row lock) |

## 4. Order money & snapshot rules

- **Selling price** is snapshotted into `order_items.unit_price_rial` at **creation**
  (the price the customer saw and accepted). Later product price changes never affect
  existing orders, pending or not.
- **Product name and inventory mode** are snapshotted at creation.
- **Cost** is snapshotted at **completion** (`order_items.cogs_rial`), because that is
  when physical stock leaves and the moving average applies.
- PENDING/PREPARING/READY orders carry no COGS. CANCELLED orders never get COGS.

## 5. Cost model

### 5.1 TRACKED products — moving weighted average (value-based)
The balance stores total **inventory value** rather than an average unit cost, which
avoids rounding drift:

- **Purchase** of `q` at `c`: `on_hand += q`, `value += q × c`.
- **Consumption** of `q` (sale, adjustment out): `cost = round_half_up(value × q / on_hand)`;
  `on_hand -= q`, `value -= cost`. When `q = on_hand`, `cost = value` exactly, so value
  returns to 0 with the stock. Computed in SQL with `numeric` intermediates to avoid overflow.
- **Adjustment in** of `q`: admin supplies `unit_cost_rial` (UI pre-fills the current
  average, or the last purchase cost if stock is 0). `value += q × unit_cost`.
- **Purchase void**: allowed only if `on_hand − reserved ≥ q_purchase`. Removes `q` at
  `min(q × c, value)` (or all value if `q = on_hand`). Recorded as `PURCHASE_VOID`.
- Average unit cost shown in UI = `value / on_hand` (display only).

### 5.2 UNTRACKED products (made-to-order drinks)
No balance, no reservation, `cogs_rial = 0`. Ingredient purchases (beans, milk, syrups,
cups) are recorded as **expenses** (`INGREDIENTS`, `SUPPLIES`). Consequence: gross profit
is accurate for tracked items only, and the **estimated operating result** captures
ingredient costs through expenses. No double counting because ingredient spend never
enters COGS.

## 6. Protective triggers
Created in the initial migration:

- `forbid_delete()` BEFORE DELETE on `orders`, `order_items`, `payments`, `expenses`,
  `purchase_entries`, `inventory_movements`, `audit_logs` → raise exception.
- `forbid_update()` BEFORE UPDATE on `inventory_movements`, `audit_logs`, `order_items`
  except the single transition `cogs_rial NULL → value` on `order_items`.
- `void_only_update()` BEFORE UPDATE on `payments`, `expenses`, `purchase_entries`:
  only `voided_at`, `voided_by`, `void_reason` may change, and only from NULL.
- `set_updated_at()` on tables with `updated_at`.

The application DB role owns no superuser rights; triggers cannot be bypassed by app code.

## 7. Reporting queries (definitions)

All grouped by Asia/Tehran business date of the relevant timestamp.

| Metric | Definition |
|--------|------------|
| Gross sales | Σ `total_rial` of orders COMPLETED in range (by `completed_at`) |
| Refunds | Σ non-void REFUND `amount_rial` in range (by refund `created_at`) where order is COMPLETED |
| **Revenue (net)** | Gross sales − Refunds |
| Orders | count of COMPLETED orders |
| Average order value | Gross sales ÷ Orders (integer rials, rounded) |
| **COGS** | Σ `cogs_rial` of lines of orders COMPLETED in range (refunds do not reverse COGS — goods were consumed) |
| **Gross profit** | Revenue − COGS |
| Inventory losses | Σ −`value_delta_rial` of `ADJUSTMENT_OUT` in range |
| **Expenses** | Σ non-void `expenses.amount_rial` by `expense_date` |
| **Estimated operating result** | Gross profit − Inventory losses − Expenses |
| Purchases (info only) | Σ non-void `purchase_entries.total_cost_rial` — cash spent on stock; **not** subtracted (it becomes COGS when sold) |
| Cash / card collected | Σ non-void PAYMENT − Σ non-void REFUND by method, by `created_at` |
| Unpaid completed | Orders COMPLETED with `payment_status = 'UNPAID'` (receivable) |

### 7.1 Accounting limitations (shown in the report UI footer)
1. Revenue is recognised on completion, not on payment; unpaid completed orders still count.
2. UNTRACKED items have zero COGS; their ingredient costs appear under Expenses, so their gross margin is overstated and the operating result is the meaningful figure.
3. `ADJUSTMENT_IN` adds stock at entered cost and has no P&L effect.
4. No tax, depreciation, accruals, owner draws, or bank reconciliation. This is a management estimate, **not** net profit or a tax report.
5. Expenses are recorded on the date entered, not amortised.
