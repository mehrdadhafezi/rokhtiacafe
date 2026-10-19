-- Purchases, orders, order lines and the inventory ledger (docs/DATABASE.md §2.6–2.9).

-- +goose Up
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
  CONSTRAINT purchase_entries_void_reason CHECK (
    (voided_at IS NULL AND void_reason IS NULL) OR
    (voided_at IS NOT NULL AND void_reason IS NOT NULL AND btrim(void_reason) <> ''))
);
CREATE INDEX purchase_entries_product_idx ON purchase_entries (product_id, purchased_on DESC);

CREATE TABLE orders (
  id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  business_date       date    NOT NULL,
  display_number      integer NOT NULL CHECK (display_number > 0),
  tracking_token_hash bytea   NOT NULL UNIQUE CHECK (length(tracking_token_hash) = 32),
  idempotency_key     uuid    NOT NULL UNIQUE,
  request_hash        bytea   NOT NULL CHECK (length(request_hash) = 32),
  client_key          bytea   NOT NULL CHECK (length(client_key) = 32),
  customer_label      text CHECK (char_length(customer_label) <= 40),
  customer_note       text CHECK (char_length(customer_note) <= 200),
  status              text NOT NULL DEFAULT 'PENDING' CHECK (status IN
                        ('PENDING','PREPARING','READY','COMPLETED','CANCELLED')),
  total_rial          bigint NOT NULL CHECK (total_rial > 0),
  paid_rial           bigint NOT NULL DEFAULT 0,
  refunded_rial       bigint NOT NULL DEFAULT 0,
  payment_status      text GENERATED ALWAYS AS (
                        CASE WHEN refunded_rial > 0      THEN 'REFUNDED'
                             WHEN paid_rial = total_rial THEN 'PAID'
                             ELSE 'UNPAID' END) STORED,
  expires_at          timestamptz NOT NULL,
  cancel_reason       text CHECK (cancel_reason IN ('CUSTOMER','STAFF','EXPIRED')),
  cancel_note         text CHECK (char_length(cancel_note) <= 200),
  created_at          timestamptz NOT NULL DEFAULT now(),
  accepted_at         timestamptz,
  ready_at            timestamptz,
  completed_at        timestamptz,
  cancelled_at        timestamptz,
  accepted_by         bigint REFERENCES users(id),
  completed_by        bigint REFERENCES users(id),
  cancelled_by        bigint REFERENCES users(id),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_date, display_number),
  CONSTRAINT orders_expires_after_created  CHECK (expires_at > created_at),
  CONSTRAINT orders_cancelled_timestamp    CHECK ((status = 'CANCELLED') = (cancelled_at IS NOT NULL)),
  CONSTRAINT orders_cancelled_reason       CHECK ((status = 'CANCELLED') = (cancel_reason IS NOT NULL)),
  CONSTRAINT orders_completed_timestamp    CHECK ((status = 'COMPLETED') = (completed_at IS NOT NULL)),
  CONSTRAINT orders_accepted_timestamp     CHECK (status NOT IN ('PREPARING','READY','COMPLETED') OR accepted_at IS NOT NULL),
  CONSTRAINT orders_ready_timestamp        CHECK (status NOT IN ('READY','COMPLETED') OR ready_at IS NOT NULL),
  CONSTRAINT orders_accepted_before_deadline CHECK (accepted_at IS NULL OR accepted_at < expires_at),
  CONSTRAINT orders_expired_never_accepted CHECK (cancel_reason IS DISTINCT FROM 'EXPIRED' OR accepted_at IS NULL),
  CONSTRAINT orders_paid_range             CHECK (paid_rial >= 0 AND paid_rial <= total_rial),
  CONSTRAINT orders_refunds_full           CHECK (refunded_rial = 0 OR refunded_rial = paid_rial),
  CONSTRAINT orders_no_money_when_pending  CHECK (status <> 'PENDING' OR paid_rial = 0),
  CONSTRAINT orders_no_money_when_cancelled CHECK (status <> 'CANCELLED' OR paid_rial = refunded_rial)
);
CREATE INDEX orders_open_idx ON orders (status, created_at) WHERE status IN ('PENDING','PREPARING','READY');
CREATE INDEX orders_completed_idx ON orders (completed_at) WHERE status = 'COMPLETED';
CREATE INDEX orders_expiry_idx ON orders (expires_at) WHERE status = 'PENDING';
CREATE INDEX orders_client_pending_idx ON orders (client_key) WHERE status = 'PENDING';

CREATE TABLE order_items (
  id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  order_id             bigint NOT NULL REFERENCES orders(id),
  product_id           bigint NOT NULL REFERENCES products(id),
  product_name         text   NOT NULL,
  inventory_mode       text   NOT NULL CHECK (inventory_mode IN ('TRACKED','UNTRACKED')),
  unit_price_rial      bigint NOT NULL CHECK (unit_price_rial > 0),
  quantity             integer NOT NULL CHECK (quantity BETWEEN 1 AND 20),
  line_total_rial      bigint NOT NULL,
  cogs_rial            bigint CHECK (cogs_rial >= 0),
  estimated_cost_rial  bigint CHECK (estimated_cost_rial >= 0),
  wasted_quantity      integer NOT NULL DEFAULT 0,
  UNIQUE (order_id, product_id),
  CONSTRAINT order_items_line_total CHECK (line_total_rial = unit_price_rial * quantity),
  CONSTRAINT order_items_waste_range CHECK (wasted_quantity BETWEEN 0 AND quantity),
  CONSTRAINT order_items_estimate_untracked_only CHECK (inventory_mode = 'UNTRACKED' OR estimated_cost_rial IS NULL),
  CONSTRAINT order_items_untracked_zero_cogs CHECK (inventory_mode = 'TRACKED' OR cogs_rial IS NULL OR cogs_rial = 0)
);
CREATE INDEX order_items_product_idx ON order_items (product_id);

CREATE TABLE inventory_movements (
  id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  product_id        bigint NOT NULL REFERENCES products(id),
  movement_type     text NOT NULL CHECK (movement_type IN
                      ('PURCHASE','PURCHASE_VOID','SALE','WASTE','ADJUSTMENT_IN','ADJUSTMENT_OUT')),
  reason_code       text CHECK (reason_code IN
                      ('OPENING_BALANCE','COUNT_CORRECTION','RETURN_TO_STOCK','DAMAGED','EXPIRED')),
  quantity_delta    integer NOT NULL CHECK (quantity_delta <> 0),
  value_delta_rial  bigint  NOT NULL,
  on_hand_after     integer NOT NULL CHECK (on_hand_after >= 0),
  value_after_rial  bigint  NOT NULL CHECK (value_after_rial >= 0),
  order_id          bigint REFERENCES orders(id),
  purchase_entry_id bigint REFERENCES purchase_entries(id),
  note              text CHECK (char_length(note) <= 300),
  actor_user_id     bigint REFERENCES users(id),
  idempotency_key   uuid UNIQUE,
  created_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT inventory_movements_direction CHECK (
    (movement_type IN ('PURCHASE','ADJUSTMENT_IN') AND quantity_delta > 0 AND value_delta_rial >= 0) OR
    (movement_type IN ('SALE','WASTE','PURCHASE_VOID','ADJUSTMENT_OUT') AND quantity_delta < 0 AND value_delta_rial <= 0)
  ),
  CONSTRAINT inventory_movements_order_link CHECK ((movement_type IN ('SALE','WASTE')) = (order_id IS NOT NULL)),
  CONSTRAINT inventory_movements_purchase_link CHECK ((movement_type IN ('PURCHASE','PURCHASE_VOID')) = (purchase_entry_id IS NOT NULL)),
  CONSTRAINT inventory_movements_reason_code CHECK (
    (movement_type = 'ADJUSTMENT_IN'  AND reason_code IS NOT NULL AND reason_code IN ('OPENING_BALANCE','COUNT_CORRECTION','RETURN_TO_STOCK')) OR
    (movement_type = 'ADJUSTMENT_OUT' AND reason_code IS NOT NULL AND reason_code IN ('COUNT_CORRECTION','DAMAGED','EXPIRED')) OR
    (movement_type NOT IN ('ADJUSTMENT_IN','ADJUSTMENT_OUT') AND reason_code IS NULL)
  ),
  CONSTRAINT inventory_movements_adjustment_note CHECK (
    movement_type NOT IN ('ADJUSTMENT_IN','ADJUSTMENT_OUT') OR (note IS NOT NULL AND btrim(note) <> ''))
);
CREATE INDEX inventory_movements_product_idx ON inventory_movements (product_id, id DESC);
CREATE INDEX inventory_movements_created_idx ON inventory_movements (created_at);
CREATE UNIQUE INDEX inventory_movements_order_once ON inventory_movements (order_id, product_id, movement_type)
  WHERE order_id IS NOT NULL;
CREATE UNIQUE INDEX inventory_movements_purchase_once ON inventory_movements (purchase_entry_id, movement_type)
  WHERE purchase_entry_id IS NOT NULL;

-- The ledger is append-only: the runtime role may only insert into it.
REVOKE UPDATE ON inventory_movements FROM rokh_app;
