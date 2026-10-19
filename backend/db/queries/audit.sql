-- name: InsertAuditLog :exec
INSERT INTO audit_logs (actor_user_id, actor_kind, action, entity_type, entity_id, details, ip)
VALUES (@actor_user_id, @actor_kind, @action, @entity_type, @entity_id, @details, @ip);

-- name: ListAuditLogs :many
SELECT * FROM audit_logs
WHERE (sqlc.narg('entity_type')::text IS NULL OR entity_type = sqlc.narg('entity_type'))
  AND (sqlc.narg('entity_id')::text IS NULL OR entity_id = sqlc.narg('entity_id'))
  AND (sqlc.narg('before_id')::int8 IS NULL OR id < sqlc.narg('before_id'))
ORDER BY id DESC
LIMIT @row_limit;
