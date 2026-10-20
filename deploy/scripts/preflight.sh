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

# 3. rokh loads ./.env and ../.env from its working directory: none may exist where it runs.
for f in /.env; do
  if [ -e "$f" ]; then fail "stray $f would be loaded by rokh"; else pass "no stray $f"; fi
done

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
