-- Web Push subscriptions and the leased notification outbox (docs/WORKFLOWS.md §4).

-- name: UpsertPushSubscription :one
-- One row per endpoint; re-subscribing the same browser (or another admin on it) rebinds it.
INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, user_agent)
VALUES (@user_id, @endpoint, @p256dh, @auth, @user_agent)
ON CONFLICT (endpoint) DO UPDATE
SET user_id = EXCLUDED.user_id, p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth,
    user_agent = EXCLUDED.user_agent, failure_count = 0
RETURNING *;

-- name: DeleteUserPushSubscription :execrows
DELETE FROM push_subscriptions WHERE endpoint = @endpoint AND user_id = @user_id;

-- name: ListUserPushSubscriptions :many
SELECT id FROM push_subscriptions WHERE user_id = @user_id ORDER BY id;

-- name: ExpireStaleNotificationClaims :execrows
-- A claim whose lease ran out after the last allowed attempt cannot be retried: fail it.
UPDATE notification_events
SET status = 'FAILED', claim_token = NULL, lease_until = NULL, last_error = 'lease expired after the last attempt'
WHERE status = 'SENDING' AND lease_until < now() AND attempts >= @max_attempts::int;

-- name: ClaimNotificationEvents :many
-- Short transaction: lease due events (and claims of crashed workers) to this worker.
UPDATE notification_events
SET status = 'SENDING', claim_token = @claim_token, lease_until = now() + @lease::interval, attempts = attempts + 1
WHERE id IN (
  SELECT ne.id FROM notification_events ne
  WHERE ((ne.status = 'PENDING' AND ne.next_attempt_at <= now())
      OR (ne.status = 'SENDING' AND ne.lease_until < now()))
    AND ne.attempts < @max_attempts::int
  ORDER BY ne.id
  LIMIT @batch
  FOR UPDATE SKIP LOCKED)
RETURNING id, event_type, dedup_key, subscription_id, product_id, payload, attempts, created_at;

-- name: GetPushSubscription :one
SELECT * FROM push_subscriptions WHERE id = $1;

-- name: MarkNotificationSent :execrows
UPDATE notification_events
SET status = 'SENT', sent_at = now(), claim_token = NULL, lease_until = NULL, last_error = NULL
WHERE id = @id AND status = 'SENDING' AND claim_token = @claim_token;

-- name: MarkNotificationRetry :execrows
UPDATE notification_events
SET status = 'PENDING', next_attempt_at = @next_attempt_at, claim_token = NULL, lease_until = NULL, last_error = @last_error
WHERE id = @id AND status = 'SENDING' AND claim_token = @claim_token;

-- name: MarkNotificationFinal :execrows
-- FAILED or GONE.
UPDATE notification_events
SET status = @status, claim_token = NULL, lease_until = NULL, last_error = @last_error
WHERE id = @id AND status = 'SENDING' AND claim_token = @claim_token;

-- name: MarkPushSubscriptionSuccess :exec
UPDATE push_subscriptions SET last_success_at = now(), failure_count = 0 WHERE id = $1;

-- name: IncrementPushSubscriptionFailures :one
UPDATE push_subscriptions SET failure_count = failure_count + 1 WHERE id = $1 RETURNING failure_count;

-- name: DeletePushSubscription :exec
DELETE FROM push_subscriptions WHERE id = $1;

-- name: DeleteOldNotificationEvents :execrows
DELETE FROM notification_events
WHERE status IN ('SENT', 'FAILED', 'GONE') AND created_at < @before;
