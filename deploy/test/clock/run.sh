#!/usr/bin/env bash
# C6 cross-day refund gate: start a throwaway PostgreSQL with a controllable clock (libfaketime),
# run the clock tests against it, remove everything:
#   deploy/test/clock/run.sh            # PostgreSQL 16 (release target)
#   PG_MAJOR=17 deploy/test/clock/run.sh
# The server listens on 127.0.0.1 only, with random passwords; nothing else is touched.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../../.." && pwd)"
major="${PG_MAJOR:-16}"
image="rokh-clock-postgres:$major"
name="rokh-clock-$major-$$"
clockdir="$(mktemp -d)"
printf '+0\n' > "$clockdir/faketime"
chmod 755 "$clockdir"
chmod 644 "$clockdir/faketime"
native() { if command -v cygpath > /dev/null; then cygpath -w "$1"; else printf '%s' "$1"; fi; }
command -v cygpath > /dev/null && export MSYS_NO_PATHCONV=1 # Git Bash on Windows
cleanup() {
  docker rm -f "$name" > /dev/null 2>&1 || true
  rm -rf "$clockdir"
}
trap cleanup EXIT

rand() { od -An -N12 -tx1 /dev/urandom | tr -d ' \n'; }
super="s$(rand)" owner="o$(rand)" app="a$(rand)"
docker build -q --build-arg "PG_MAJOR=$major" -t "$image" "$(native "$here")" > /dev/null
docker run -d --name "$name" -e POSTGRES_PASSWORD="$super" -v "$(native "$clockdir"):/clock:ro" \
  -p 127.0.0.1::5432 "$image" > /dev/null
for _ in $(seq 60); do
  docker exec "$name" pg_isready -U postgres -h 127.0.0.1 -q 2> /dev/null && break
  sleep 1
done
docker exec -i "$name" psql -X -q -v ON_ERROR_STOP=1 -U postgres -d postgres \
  -v owner_password="$owner" -v app_password="$app" -v db_name=rokhtiacafe < "$repo/deploy/local/postgres/roles.sql"
port="$(docker port "$name" 5432/tcp | head -n1 | sed 's/.*://')"
echo "PostgreSQL $(docker exec "$name" psql -XAt -U postgres -c 'SHOW server_version') with libfaketime on 127.0.0.1:$port"

export CLOCK_TEST_DATABASE_ADMIN_URL="postgres://postgres:$super@127.0.0.1:$port/postgres?sslmode=disable"
export CLOCK_MIGRATION_DATABASE_URL="postgres://rokh_owner:$owner@127.0.0.1:$port/rokhtiacafe?sslmode=disable"
export CLOCK_DATABASE_URL="postgres://rokh_app:$app@127.0.0.1:$port/rokhtiacafe?sslmode=disable"
CLOCK_FAKETIME_FILE="$(native "$clockdir/faketime")"
export CLOCK_FAKETIME_FILE
export REQUIRE_CLOCK_DB_TESTS=1
cd "$repo/backend"
go test ./internal/api -run '^TestCrossDay' -count=1 -v "$@"
