#!/usr/bin/env bash
# Deployment preflight (deploy/README.md §3, §7): checks the settings files, runs
# `rokh config check` with exactly the service's settings, and confirms no migration is pending.
# Read-only. Run as root:
#   /opt/rokh/scripts/preflight.sh
# Prints PASS/FAIL per check; exits 1 if anything failed. Never prints secret values.
set -euo pipefail
. "$(dirname "$0")/lib.sh"

load_ops
fails=0
pass() { echo "PASS  $*"; }
fail() {
  echo "FAIL  $*"
  fails=$((fails + 1))
}
mode_of() { stat -c %a "$1"; }

# 1. Settings files exist and are not readable by other users.
for f in "$ROKH_ENV_FILE" "$ROKH_MIGRATE_ENV_FILE"; do
  if [ ! -f "$f" ]; then
    fail "$f is missing"
  elif [ $(($(mode_of "$f") % 10)) -ne 0 ]; then
    fail "$f is accessible to other users (mode $(mode_of "$f"); want 640 or 600)"
  else
    pass "$f exists, mode $(mode_of "$f")"
  fi
done
if [ -f "$ROKH_MIGRATE_ENV_FILE" ] && [ $(($(mode_of "$ROKH_MIGRATE_ENV_FILE") / 10 % 10)) -ne 0 ]; then
  fail "$ROKH_MIGRATE_ENV_FILE is group-accessible (want 600: owner credentials)"
fi
[ "$fails" -eq 0 ] || {
  echo "$fails check(s) failed"
  exit 1
}

# 2. Owner credentials stay out of the runtime settings.
if [ -n "$(env_get "$ROKH_ENV_FILE" MIGRATION_DATABASE_URL)" ]; then
  fail "MIGRATION_DATABASE_URL is in $ROKH_ENV_FILE: keep it in $ROKH_MIGRATE_ENV_FILE only"
else
  pass "no owner credentials in $ROKH_ENV_FILE"
fi
parse_pg_url "$(env_get "$ROKH_ENV_FILE" DATABASE_URL)" APP "$ROKH_ENV_FILE"
parse_pg_url "$(env_get "$ROKH_MIGRATE_ENV_FILE" MIGRATION_DATABASE_URL)" OWN "$ROKH_MIGRATE_ENV_FILE"
if [ "$APP_USER" = "$OWN_USER" ] || [ "$APP_USER" = postgres ]; then
  fail "DATABASE_URL uses role $APP_USER: the service must run as the DML-only app role"
else
  pass "service role $APP_USER, migration role $OWN_USER"
fi
if [ "$APP_HOST:$APP_PORT/$APP_DB" = "$OWN_HOST:$OWN_PORT/$OWN_DB" ]; then
  pass "both settings files point at $APP_HOST:$APP_PORT/$APP_DB"
else
  fail "DATABASE_URL and MIGRATION_DATABASE_URL point at different databases"
fi

# 2b. The PostgreSQL compose settings (deploy/README.md §4.1): the published port is the one the
# database URLs use, and the image is pinned by digest. Compose has no defaults for either.
compose_env="$(opt ROKH_COMPOSE_ENV_FILE /opt/rokh/postgres/.env)"
if [ ! -f "$compose_env" ]; then
  fail "$compose_env is missing (ROKH_PG_PORT, ROKH_PG_IMAGE; template deploy/env/compose.env.example)"
elif [ -n "$(find "$compose_env" -maxdepth 0 -perm -o+w)" ]; then
  fail "$compose_env is world-writable (want root:root 600)"
else
  pg_port="$(env_get "$compose_env" ROKH_PG_PORT)"
  pg_image="$(env_get "$compose_env" ROKH_PG_IMAGE)"
  if ! [[ $pg_port =~ ^[1-9][0-9]{3,4}$ ]] || [ "$pg_port" -gt 65535 ]; then
    fail "ROKH_PG_PORT in $compose_env must be a port number 1024-65535, got '$pg_port'"
  elif [ "$APP_HOST:$APP_PORT" != "127.0.0.1:$pg_port" ]; then
    fail "DATABASE_URL points at $APP_HOST:$APP_PORT, but compose publishes 127.0.0.1:$pg_port ($compose_env)"
  else
    pass "PostgreSQL published on 127.0.0.1:$pg_port, as the database URLs expect"
  fi
  if [[ $pg_image =~ ^postgres:16[A-Za-z0-9._-]*@sha256:[0-9a-f]{64}$ ]]; then
    pass "PostgreSQL image pinned by digest"
  else
    fail "ROKH_PG_IMAGE in $compose_env is not pinned (want postgres:16-…@sha256:<64 hex>)"
  fi
fi

# 3. rokh loads ./.env and ../.env from its working directory: none may exist where it runs.
if [ -e /.env ]; then fail "stray /.env would be loaded by rokh"; else pass "no stray /.env"; fi

# 4. rokh config check with exactly the service's settings (a clean environment).
if out="$(
  while IFS= read -r k; do unset "$k"; done < <(grep -Eo '^[[:space:]]*[A-Za-z_][A-Za-z0-9_]*=' "$ROKH_ENV_FILE" | tr -d ' =')
  unset MIGRATION_DATABASE_URL
  env_export_file "$ROKH_ENV_FILE"
  rokh_cmd config check 2>&1
)"; then
  pass "rokh config check: ready to serve"
else
  fail "rokh config check:"
fi
sed 's/^/      /' <<< "$out"

# 5. Schema up to date.
if st="$(MIGRATION_DATABASE_URL="$(env_get "$ROKH_MIGRATE_ENV_FILE" MIGRATION_DATABASE_URL)" rokh_cmd migrate status 2>&1)"; then
  if grep -q ', 0 pending$' <<< "$st"; then
    pass "migrations: $(tail -n 1 <<< "$st")"
  else
    fail "migrations: $(tail -n 1 <<< "$st") (run migrate.sh up)"
  fi
else
  fail "rokh migrate status failed: $(tail -n 1 <<< "$st")"
fi

if [ "$fails" -gt 0 ]; then
  echo "$fails check(s) failed"
  exit 1
fi
echo "preflight ok"
