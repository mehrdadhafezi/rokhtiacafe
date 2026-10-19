-- Payments/refunds and operational expenses (docs/DATABASE.md §2.10–2.11).

-- +goose Up
CREATE TABLE payments (
  id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  order_id             bigint NOT NULL REFERENCES orders(id),
  kind                 text NOT NULL CHECK (kind IN ('PAYMENT','REFUND')),
  method               text NOT NULL CHECK (method IN ('CASH','CARD_TERMINAL')),
  amount_rial          bigint NOT NULL CHECK (amount_rial > 0),
  refund_of_payment_id bigint UNIQUE REFERENCES payments(id),
  note                 text CHECK (char_length(note) <= 200),
  idempotency_key      uuid UNIQUE,
  created_by           bigint NOT NULL REFERENCES users(id),
  created_at           timestamptz NOT NULL DEFAULT now(),
  voided_at            timestamptz,
  voided_by            bigint REFERENCES users(id),
  void_reason          text,
  CONSTRAINT payments_refund_link      CHECK ((kind = 'REFUND') = (refund_of_payment_id IS NOT NULL)),
  CONSTRAINT payments_idempotency_key  CHECK (kind = 'REFUND' OR idempotency_key IS NOT NULL),
  CONSTRAINT payments_refund_not_voidable CHECK (kind = 'PAYMENT' OR voided_at IS NULL),
  CHECK ((voided_at IS NULL) = (voided_by IS NULL)),
  CONSTRAINT payments_void_reason CHECK (
    (voided_at IS NULL AND void_reason IS NULL) OR
    (voided_at IS NOT NULL AND void_reason IS NOT NULL AND btrim(void_reason) <> ''))
);
CREATE INDEX payments_order_idx ON payments (order_id);
CREATE INDEX payments_created_idx ON payments (created_at);

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
  CONSTRAINT expenses_void_reason CHECK (
    (voided_at IS NULL AND void_reason IS NULL) OR
    (voided_at IS NOT NULL AND void_reason IS NOT NULL AND btrim(void_reason) <> ''))
);
CREATE INDEX expenses_date_idx ON expenses (expense_date) WHERE voided_at IS NULL;
