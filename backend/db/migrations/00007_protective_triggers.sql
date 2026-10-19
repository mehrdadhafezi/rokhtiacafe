-- Protective triggers (docs/DATABASE.md §6.2). They run for every role, including the owner,
-- and the runtime role cannot disable them because it does not own the tables.
--
-- Custom SQLSTATEs (mapped by the application):
--   RK001  delete forbidden            RK002  update forbidden / immutable column
--   RK003  illegal order status transition

-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION forbid_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '%: rows are never deleted; use the documented correction instead', TG_TABLE_NAME
    USING ERRCODE = 'RK001';
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION forbid_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '%: rows are append-only', TG_TABLE_NAME USING ERRCODE = 'RK002';
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION set_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END $$;
-- +goose StatementEnd

-- Payments, expenses and purchases: only the void columns may change, and only once.
-- +goose StatementBegin
CREATE FUNCTION void_only_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  void_cols constant text[] := ARRAY['voided_at', 'voided_by', 'void_reason'];
BEGIN
  IF (to_jsonb(NEW) - void_cols) IS DISTINCT FROM (to_jsonb(OLD) - void_cols) THEN
    RAISE EXCEPTION '%: only voided_at, voided_by and void_reason may change', TG_TABLE_NAME
      USING ERRCODE = 'RK002';
  END IF;
  IF OLD.voided_at IS NOT NULL THEN
    RAISE EXCEPTION '%: row % is already voided', TG_TABLE_NAME, OLD.id USING ERRCODE = 'RK002';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd

-- Order lines: snapshots are immutable; cost and waste are each set exactly once.
-- +goose StatementBegin
CREATE FUNCTION order_items_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  settable constant text[] := ARRAY['cogs_rial', 'estimated_cost_rial', 'wasted_quantity'];
BEGIN
  IF (to_jsonb(NEW) - settable) IS DISTINCT FROM (to_jsonb(OLD) - settable) THEN
    RAISE EXCEPTION 'order_items: snapshot columns are immutable (item %)', OLD.id USING ERRCODE = 'RK002';
  END IF;
  IF OLD.cogs_rial IS NOT NULL AND NEW.cogs_rial IS DISTINCT FROM OLD.cogs_rial THEN
    RAISE EXCEPTION 'order_items: cogs_rial is already set (item %)', OLD.id USING ERRCODE = 'RK002';
  END IF;
  IF OLD.estimated_cost_rial IS NOT NULL AND NEW.estimated_cost_rial IS DISTINCT FROM OLD.estimated_cost_rial THEN
    RAISE EXCEPTION 'order_items: estimated_cost_rial is already set (item %)', OLD.id USING ERRCODE = 'RK002';
  END IF;
  IF OLD.wasted_quantity <> 0 AND NEW.wasted_quantity <> OLD.wasted_quantity THEN
    RAISE EXCEPTION 'order_items: wasted_quantity is already set (item %)', OLD.id USING ERRCODE = 'RK002';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd

-- Orders: creation data is immutable, lifecycle timestamps are set once, status follows the
-- legal transition table, and terminal orders only accept the money changes WORKFLOWS §3 allows.
-- +goose StatementBegin
CREATE FUNCTION orders_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  -- updated_at is maintained by set_updated_at; payment_status is generated after BEFORE triggers.
  always_ignored constant text[] := ARRAY['updated_at', 'payment_status'];
  immutable_cols constant text[] := ARRAY['id', 'business_date', 'display_number', 'tracking_token_hash',
    'idempotency_key', 'request_hash', 'client_key', 'customer_label', 'customer_note', 'total_rial',
    'expires_at', 'created_at'];
  col text;
  old_j jsonb := to_jsonb(OLD);
  new_j jsonb := to_jsonb(NEW);
BEGIN
  FOREACH col IN ARRAY immutable_cols LOOP
    IF new_j -> col IS DISTINCT FROM old_j -> col THEN
      RAISE EXCEPTION 'orders: % is immutable (order %)', col, OLD.id USING ERRCODE = 'RK002';
    END IF;
  END LOOP;

  FOREACH col IN ARRAY ARRAY['accepted_at', 'accepted_by', 'ready_at', 'completed_at', 'completed_by',
                              'cancelled_at', 'cancelled_by', 'cancel_reason', 'cancel_note'] LOOP
    IF old_j ->> col IS NOT NULL AND new_j -> col IS DISTINCT FROM old_j -> col THEN
      RAISE EXCEPTION 'orders: % is already set (order %)', col, OLD.id USING ERRCODE = 'RK002';
    END IF;
  END LOOP;

  IF NEW.status IS DISTINCT FROM OLD.status AND NOT (
       (OLD.status = 'PENDING'   AND NEW.status IN ('PREPARING', 'CANCELLED')) OR
       (OLD.status = 'PREPARING' AND NEW.status IN ('READY', 'CANCELLED')) OR
       (OLD.status = 'READY'     AND NEW.status IN ('COMPLETED', 'CANCELLED'))) THEN
    RAISE EXCEPTION 'orders: illegal status transition % -> % (order %)', OLD.status, NEW.status, OLD.id
      USING ERRCODE = 'RK003';
  END IF;

  IF OLD.status = 'CANCELLED' AND (new_j - always_ignored) IS DISTINCT FROM (old_j - always_ignored) THEN
    RAISE EXCEPTION 'orders: cancelled order % is final', OLD.id USING ERRCODE = 'RK002';
  END IF;
  IF OLD.status = 'COMPLETED'
     AND (new_j - always_ignored - ARRAY['paid_rial', 'refunded_rial'])
         IS DISTINCT FROM (old_j - always_ignored - ARRAY['paid_rial', 'refunded_rial']) THEN
    RAISE EXCEPTION 'orders: completed order % only accepts payment changes', OLD.id USING ERRCODE = 'RK002';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd

-- Deletes are forbidden on financial, inventory, catalog-history and audit tables.
CREATE TRIGGER orders_no_delete              BEFORE DELETE ON orders              FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER order_items_no_delete         BEFORE DELETE ON order_items         FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER payments_no_delete            BEFORE DELETE ON payments            FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER expenses_no_delete            BEFORE DELETE ON expenses            FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER purchase_entries_no_delete    BEFORE DELETE ON purchase_entries    FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER inventory_movements_no_delete BEFORE DELETE ON inventory_movements FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER audit_logs_no_delete          BEFORE DELETE ON audit_logs          FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER products_no_delete            BEFORE DELETE ON products            FOR EACH ROW EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER inventory_balances_no_delete  BEFORE DELETE ON inventory_balances  FOR EACH ROW EXECUTE FUNCTION forbid_delete();

-- TRUNCATE bypasses row triggers, so block it with statement triggers as well.
CREATE TRIGGER orders_no_truncate              BEFORE TRUNCATE ON orders              EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER order_items_no_truncate         BEFORE TRUNCATE ON order_items         EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER payments_no_truncate            BEFORE TRUNCATE ON payments            EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER expenses_no_truncate            BEFORE TRUNCATE ON expenses            EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER purchase_entries_no_truncate    BEFORE TRUNCATE ON purchase_entries    EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER inventory_movements_no_truncate BEFORE TRUNCATE ON inventory_movements EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER audit_logs_no_truncate          BEFORE TRUNCATE ON audit_logs          EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER products_no_truncate            BEFORE TRUNCATE ON products            EXECUTE FUNCTION forbid_delete();
CREATE TRIGGER inventory_balances_no_truncate  BEFORE TRUNCATE ON inventory_balances  EXECUTE FUNCTION forbid_delete();

-- Append-only tables.
CREATE TRIGGER inventory_movements_no_update BEFORE UPDATE ON inventory_movements FOR EACH ROW EXECUTE FUNCTION forbid_update();
CREATE TRIGGER audit_logs_no_update          BEFORE UPDATE ON audit_logs          FOR EACH ROW EXECUTE FUNCTION forbid_update();

-- Void-once tables.
CREATE TRIGGER payments_void_only         BEFORE UPDATE ON payments         FOR EACH ROW EXECUTE FUNCTION void_only_update();
CREATE TRIGGER expenses_void_only         BEFORE UPDATE ON expenses         FOR EACH ROW EXECUTE FUNCTION void_only_update();
CREATE TRIGGER purchase_entries_void_only BEFORE UPDATE ON purchase_entries FOR EACH ROW EXECUTE FUNCTION void_only_update();

CREATE TRIGGER order_items_guard BEFORE UPDATE ON order_items FOR EACH ROW EXECUTE FUNCTION order_items_guard();
CREATE TRIGGER orders_guard      BEFORE UPDATE ON orders      FOR EACH ROW EXECUTE FUNCTION orders_guard();

CREATE TRIGGER users_updated_at              BEFORE UPDATE ON users              FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER categories_updated_at         BEFORE UPDATE ON categories         FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER products_updated_at           BEFORE UPDATE ON products           FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER inventory_balances_updated_at BEFORE UPDATE ON inventory_balances FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER orders_updated_at             BEFORE UPDATE ON orders             FOR EACH ROW EXECUTE FUNCTION set_updated_at();
