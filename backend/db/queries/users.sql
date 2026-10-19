-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByIDForUpdate :one
SELECT * FROM users WHERE id = $1 FOR UPDATE;

-- name: ListUsers :many
SELECT * FROM users ORDER BY id;

-- name: CreateUser :one
INSERT INTO users (username, display_name, password_hash, role)
VALUES (@username, @display_name, @password_hash, @role)
RETURNING *;

-- name: UpdateUserProfile :one
UPDATE users SET display_name = @display_name, role = @role, is_active = @is_active
WHERE id = @id
RETURNING *;

-- name: SetUserPassword :exec
UPDATE users SET password_hash = @password_hash WHERE id = @id;

-- name: TouchUserLogin :exec
UPDATE users SET last_login_at = now() WHERE id = $1;

-- name: CountActiveAdmins :one
SELECT count(*)::int8 FROM users WHERE role = 'ADMIN' AND is_active;

-- name: AdvisoryXactLock :exec
-- Serializes a class of operations for the rest of the transaction (docs/DATABASE.md §3.1).
SELECT pg_advisory_xact_lock(@key::int8);
