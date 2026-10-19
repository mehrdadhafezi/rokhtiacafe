-- name: Ping :one
-- Readiness probe: proves the runtime role can run a query.
SELECT 1::int4 AS ok;
