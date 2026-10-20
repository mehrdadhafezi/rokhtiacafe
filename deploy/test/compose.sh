#!/usr/bin/env bash
# Start a disposable copy of deploy/postgres/compose.yaml (own project, container, volume, network
# and port), check it, run the suite with PG_CLIENT=docker, then remove exactly that copy:
#   deploy/test/compose.sh release/
# Needs Docker with Compose v2. Production resources (rokh-postgres, rokh-pgdata) are never used.
set -euo pipefail
repo="$(cd "$(dirname "$0")/../.." && pwd)"
release="$(cd "${1:?usage: compose.sh <release dir>}" && pwd)"
id="dt$$"
port="${TEST_PG_PORT:-25432}"
tmp="$(mktemp -d)"
mkdir "$tmp/initdb"
cp "$repo/deploy/postgres/compose.yaml" "$tmp/"
cp "$repo/deploy/local/postgres/10-rokh-init.sh" "$repo/deploy/local/postgres/roles.sql" "$tmp/initdb/"
rand() { od -An -N12 -tx1 /dev/urandom | tr -d ' \n'; }
sed -e "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$(rand)/" \
  -e "s/^ROKH_OWNER_PASSWORD=.*/ROKH_OWNER_PASSWORD=$(rand)/" \
  -e "s/^ROKH_APP_PASSWORD=.*/ROKH_APP_PASSWORD=$(rand)/" \
  "$repo/deploy/env/postgres.env.example" > "$tmp/postgres.env"
chmod 600 "$tmp/postgres.env"

native() { if command -v cygpath > /dev/null; then cygpath -w "$1"; else printf '%s' "$1"; fi; }
command -v cygpath > /dev/null && export MSYS_NO_PATHCONV=1 # Git Bash on Windows
export ROKH_PG_CONTAINER="rokh-$id-pg" ROKH_PG_VOLUME="rokh-$id-pgdata" ROKH_PG_NETWORK="rokh-$id-net" \
  ROKH_PG_PORT="$port" ROKH_PG_ENV_FILE="$(native "$tmp/postgres.env")"
dc() { docker compose -p "rokh-$id" -f "$(native "$tmp/compose.yaml")" "$@"; }
cleanup() {
  dc down --volumes --timeout 30 > /dev/null 2>&1 || true
  rm -f "$tmp/compose.yaml" "$tmp/postgres.env" "$tmp/initdb/10-rokh-init.sh" "$tmp/initdb/roles.sql"
  rmdir "$tmp/initdb" "$tmp" 2> /dev/null || true
}
trap cleanup EXIT

dc config --quiet
dc up --detach --wait --wait-timeout 120
fail=0
check() {
  if eval "$2"; then echo "PASS  $1"; else echo "FAIL  $1"; fail=1; fi
}
q() { docker exec -u postgres "$ROKH_PG_CONTAINER" psql -XAt -d rokhtiacafe -c "$1"; }
check "compose: container is healthy" '[ "$(docker inspect -f "{{.State.Health.Status}}" "$ROKH_PG_CONTAINER")" = healthy ]'
check "compose: PostgreSQL 16" '[ "$(q "SHOW server_version_num" | cut -c1-2)" = 16 ]'
check "compose: port published on 127.0.0.1 only" '[ "$(docker port "$ROKH_PG_CONTAINER" 5432/tcp)" = "127.0.0.1:$port" ]'
check "compose: restart policy unless-stopped" '[ "$(docker inspect -f "{{.HostConfig.RestartPolicy.Name}}" "$ROKH_PG_CONTAINER")" = unless-stopped ]'
check "compose: dedicated network only" '[ "$(docker inspect -f "{{range \$k, \$v := .NetworkSettings.Networks}}{{\$k}} {{end}}" "$ROKH_PG_CONTAINER")" = "$ROKH_PG_NETWORK " ]'
check "compose: log rotation configured" '[ "$(docker inspect -f "{{index .HostConfig.LogConfig.Config \"max-size\"}}" "$ROKH_PG_CONTAINER")" = 10m ]'
check "compose: database owned by rokh_owner" '[ "$(q "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = current_database()")" = rokh_owner ]'
roles_sql="SELECT count(*) FILTER (WHERE NOT (rolsuper OR rolcreatedb OR rolcreaterole)) || '/' || count(*)
           FROM pg_roles WHERE rolname IN ('rokh_owner', 'rokh_app')"
check "compose: roles are not superuser, createdb or createrole" '[ "$(q "$roles_sql")" = 2/2 ]'
check "compose: password auth is scram-sha-256" '[ "$(q "SHOW password_encryption")" = scram-sha-256 ]'
check "compose: no trust authentication" '! q "SELECT auth_method FROM pg_hba_file_rules" | grep -qx trust'

if [ "$(uname -s)" != Linux ]; then
  echo "SKIP  suite with PG_CLIENT=docker: rokh-linux-amd64 cannot run on $(uname -s) (runs in CI)"
  exit "$fail"
fi
TEST_PG_DISPOSABLE=yes PG_CLIENT=docker PG_CONTAINER="$ROKH_PG_CONTAINER" TEST_PG_HOST=127.0.0.1 \
  TEST_PG_PORT="$port" ROKH_RELEASE_DIR="$release" bash "$repo/deploy/test/run.sh" || fail=1
exit "$fail"
