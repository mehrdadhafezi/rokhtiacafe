-- Web Push subscriptions, the notification outbox, and the audit log (docs/DATABASE.md §2.12–2.14).

-- +goose Up
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

CREATE TABLE notification_events (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  event_type      text NOT NULL CHECK (event_type IN ('LOW_STOCK','TEST')),
  dedup_key       text NOT NULL,
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
  CONSTRAINT notification_events_lease CHECK ((status = 'SENDING') = (claim_token IS NOT NULL AND lease_until IS NOT NULL)),
  CONSTRAINT notification_events_sent_at CHECK ((status = 'SENT') = (sent_at IS NOT NULL))
);
CREATE INDEX notification_events_due_idx ON notification_events (next_attempt_at) WHERE status = 'PENDING';
CREATE INDEX notification_events_lease_idx ON notification_events (lease_until) WHERE status = 'SENDING';

CREATE TABLE audit_logs (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  actor_user_id bigint REFERENCES users(id),
  actor_kind    text NOT NULL CHECK (actor_kind IN ('USER','CUSTOMER','SYSTEM')),
  action        text NOT NULL,
  entity_type   text NOT NULL,
  entity_id     text NOT NULL,
  details       jsonb NOT NULL DEFAULT '{}',
  ip            inet,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT audit_logs_actor CHECK ((actor_kind = 'USER') = (actor_user_id IS NOT NULL))
);
CREATE INDEX audit_logs_entity_idx ON audit_logs (entity_type, entity_id, id DESC);
CREATE INDEX audit_logs_created_idx ON audit_logs (created_at DESC);

-- Subscriptions are removed when a push service reports them gone; outbox rows are cleaned up after 30 days.
GRANT DELETE ON push_subscriptions, notification_events TO rokh_app;
-- The audit log is append-only for the runtime role.
REVOKE UPDATE ON audit_logs FROM rokh_app;
