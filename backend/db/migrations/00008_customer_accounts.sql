-- Customer identities are separate from staff users. Forward-only, additive migration.
-- No existing orders are claimed or modified. Unauthenticated orders keep customer_id NULL.
-- IMPORTANT: deploy the customer-aware Go binary only after its migration has been tested.
--
-- +goose Up
CREATE TABLE customers (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  first_name       text NOT NULL CHECK (char_length(btrim(first_name)) BETWEEN 1 AND 60),
  last_name        text NOT NULL CHECK (char_length(btrim(last_name)) BETWEEN 1 AND 60),
  phone            text NOT NULL UNIQUE CHECK (phone ~ '^09[0-9]{9}$'),
  password_hash    text NOT NULL CHECK (char_length(password_hash) BETWEEN 40 AND 300),
  phone_verified_at timestamptz, -- NULL until a verified SMS/OTP flow is introduced.
  is_active        boolean NOT NULL DEFAULT true,
  last_login_at    timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER customers_updated_at
  BEFORE UPDATE ON customers FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Deliberately use a distinct cookie/session namespace from staff users.
-- Only a SHA-256 session-token hash is stored; never persist plaintext cookies.
CREATE TABLE customer_sessions (
  token_hash       bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
  customer_id      bigint NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  csrf_token       text NOT NULL CHECK (char_length(csrf_token) BETWEEN 20 AND 256),
  created_at       timestamptz NOT NULL DEFAULT now(),
  last_seen_at     timestamptz NOT NULL DEFAULT now(),
  expires_at       timestamptz NOT NULL,
  ip               inet,
  user_agent       text CHECK (char_length(user_agent) <= 512),
  CHECK (expires_at > created_at)
);
CREATE INDEX customer_sessions_customer_idx ON customer_sessions (customer_id);
CREATE INDEX customer_sessions_expiry_idx ON customer_sessions (expires_at);

-- Session invalidation and expiry cleanup are legitimate deletes; all other
-- customer and financial records must be retained via explicitly reviewed policies.
GRANT DELETE ON customer_sessions TO rokh_app;

-- Login/security activity, not raw request logs. No passwords, tokens or bodies.
CREATE TABLE customer_events (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  customer_id      bigint NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  event_type       text NOT NULL CHECK (event_type IN
    ('REGISTERED','LOGIN_SUCCEEDED','LOGOUT','PASSWORD_CHANGED',
     'PROFILE_UPDATED','ORDER_CREATED','SESSIONS_REVOKED')),
  order_id         bigint REFERENCES orders(id) ON DELETE RESTRICT,
  ip               inet,
  created_at       timestamptz NOT NULL DEFAULT now(),
  CHECK (order_id IS NULL OR event_type = 'ORDER_CREATED')
);
CREATE INDEX customer_events_customer_idx ON customer_events (customer_id, id DESC);
CREATE INDEX customer_events_created_idx ON customer_events (created_at DESC);

CREATE TRIGGER customer_events_no_update
  BEFORE UPDATE ON customer_events FOR EACH ROW EXECUTE FUNCTION forbid_update();
CREATE TRIGGER customer_events_no_delete
  BEFORE DELETE ON customer_events FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER customer_events_no_truncate
  BEFORE TRUNCATE ON customer_events FOR EACH STATEMENT EXECUTE FUNCTION forbid_delete();

-- Link authenticated customer orders without altering the protected orders table.
-- The app inserts one link in the same transaction as order creation.
-- Historic and anonymous orders have no row in this table.
CREATE TABLE customer_order_links (
  order_id       bigint PRIMARY KEY REFERENCES orders(id) ON DELETE RESTRICT,
  customer_id    bigint NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX customer_order_links_customer_idx
  ON customer_order_links (customer_id, order_id DESC);

-- Attribution is append-only: never transfer a customer's purchase.
CREATE TRIGGER customer_order_links_no_update
  BEFORE UPDATE ON customer_order_links FOR EACH ROW EXECUTE FUNCTION forbid_update();
CREATE TRIGGER customer_order_links_no_delete
  BEFORE DELETE ON customer_order_links FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER customer_order_links_no_truncate
  BEFORE TRUNCATE ON customer_order_links FOR EACH STATEMENT EXECUTE FUNCTION forbid_delete();
