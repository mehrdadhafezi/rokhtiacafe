-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, csrf_token, expires_at, ip, user_agent)
VALUES (@token_hash, @user_id, @csrf_token, @expires_at, @ip, @user_agent);

-- name: GetSessionWithUser :one
-- Every request re-reads the user, so deactivation and role changes apply immediately.
SELECT s.token_hash, s.csrf_token, s.last_seen_at, s.expires_at,
       u.id AS user_id, u.username, u.display_name, u.role, u.is_active
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now() WHERE token_hash = $1;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteUserSessionsExcept :exec
DELETE FROM sessions WHERE user_id = @user_id AND token_hash <> @keep_token_hash;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < now() OR last_seen_at < @idle_cutoff;
