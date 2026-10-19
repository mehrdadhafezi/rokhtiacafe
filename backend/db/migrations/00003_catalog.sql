-- Categories, products and per-product stock balances (docs/DATABASE.md §2.3–2.5).

-- +goose Up
CREATE TABLE categories (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name       text NOT NULL UNIQUE CHECK (char_length(name) BETWEEN 1 AND 60),
  sort_order integer NOT NULL DEFAULT 0,
  is_active  boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE products (
  id                       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  category_id              bigint NOT NULL REFERENCES categories(id) ON DELETE RESTRICT,
  name                     text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
  description              text CHECK (char_length(description) <= 300),
  price_rial               bigint NOT NULL CHECK (price_rial > 0 AND price_rial % 10 = 0),
  inventory_mode           text NOT NULL CHECK (inventory_mode IN ('TRACKED','UNTRACKED')),
  low_stock_threshold      integer CHECK (low_stock_threshold >= 0),
  estimated_unit_cost_rial bigint CHECK (estimated_unit_cost_rial >= 0),
  is_active                boolean NOT NULL DEFAULT true,
  is_available             boolean NOT NULL DEFAULT true,
  image_path               text,
  sort_order               integer NOT NULL DEFAULT 0,
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now(),
  UNIQUE (category_id, name),
  CONSTRAINT products_threshold_tracked_only CHECK (inventory_mode = 'TRACKED'   OR low_stock_threshold IS NULL),
  CONSTRAINT products_estimate_untracked_only CHECK (inventory_mode = 'UNTRACKED' OR estimated_unit_cost_rial IS NULL)
);
CREATE INDEX products_category_idx ON products (category_id, sort_order);

CREATE TABLE inventory_balances (
  product_id            bigint PRIMARY KEY REFERENCES products(id) ON DELETE RESTRICT,
  on_hand               integer NOT NULL DEFAULT 0 CHECK (on_hand >= 0),
  reserved              integer NOT NULL DEFAULT 0 CHECK (reserved >= 0),
  inventory_value_rial  bigint  NOT NULL DEFAULT 0 CHECK (inventory_value_rial >= 0),
  last_unit_cost_rial   bigint  CHECK (last_unit_cost_rial >= 0),
  low_stock_alerted_at  timestamptz,
  updated_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT inventory_reserved_le_on_hand CHECK (reserved <= on_hand),
  CONSTRAINT inventory_zero_stock_zero_value CHECK (on_hand > 0 OR inventory_value_rial = 0)
);
