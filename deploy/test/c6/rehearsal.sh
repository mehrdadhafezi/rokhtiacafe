#!/usr/bin/env bash
# C6 rehearsal, inside the throwaway container started by in-docker.sh. Do not run elsewhere.
#
#  0 isolation     the cluster is fresh, local to this container, listening on 127.0.0.1 only
#  1 legacy        previous binary: migrations 00001–00007, staff, catalog, stock, guest orders in
#                  every money state, an expense; rokh check; snapshot S0
#  2 backup        deploy/scripts/backup.sh; SHA256SUMS and pg_restore --list verified
#  3 restore       restore-target.sql + restore.sh with the C1–C5 binary (pg_restore, migrate up =
#                  00008, 0 pending, rokh check) into a NEW database; S1 equals S0 row for row,
#                  customer tables present and empty, grants and triggers as designed
#  4 C1–C5         customer accounts, ownership, idempotency, CSRF, reports, exports; S2 keeps
#                  every S1 row
#  5 rollback      previous binary on the migrated database: legacy flows work, customer data
#                  frozen; S3 keeps every S2 row
#  6 roll-forward  C1–C5 binary again: accounts, history and reports intact; S4 keeps every S3 row
#  7 source        the original database is unchanged by all of the above
set -uo pipefail
[ -f /.dockerenv ] || { echo "rehearsal.sh must run inside the test container" >&2; exit 1; }
export TZ=UTC
LOG=/evidence/rehearsal.log
: > "$LOG"
exec > >(tee -a "$LOG") 2>&1

passed=0 failed=0
ok() { echo "PASS  $*"; passed=$((passed + 1)); }
bad() { echo "FAIL  $*"; failed=$((failed + 1)); }
check() {
  local name=$1
  shift
  if "$@"; then ok "$name"; else bad "$name"; fi
}
# run NAME CMD... — require exit 0; PASS/FAIL lines printed by the command are counted too.
run() {
  local name=$1
  shift
  local out rc
  out="$("$@" 2>&1)"
  rc=$?
  printf '%s\n' "$out" | sed 's/^/      /'
  passed=$((passed + $(printf '%s\n' "$out" | grep -c '^PASS ')))
  failed=$((failed + $(printf '%s\n' "$out" | grep -c '^FAIL ')))
  if [ "$rc" = 0 ]; then ok "$name"; else bad "$name (exit $rc)"; fi
  return "$rc"
}
summary() { echo "== result: $passed passed, $failed failed"; }
fatal() {
  bad "$* (stopping)"
  summary
  exit 1
}

rand() { od -An -N12 -tx1 /dev/urandom | tr -d ' \n'; }
useradd --system --no-create-home --shell /usr/sbin/nologin rokhtest
T="$(mktemp -d)"
chmod 755 "$T"
mkdir -p "$T/bin" "$T/etc" "$T/var/uploads/products" "$T/out"
install -m 755 /new/rokh-linux-amd64 "$T/bin/rokh-new"
install -m 755 /old/rokh-linux-amd64 "$T/bin/rokh-old"
install -m 755 /tools/c6 "$T/bin/c6"
chown -R rokhtest "$T/var/uploads"
NEW="$T/bin/rokh-new"
OLD="$T/bin/rokh-old"
C6="$T/bin/c6"

echo "== 0. isolation"
super="s$(rand)"
OWNER_PW="o$(rand)"
APP_PW="a$(rand)"
install -d -o postgres -m 700 /tmp/pgdata
printf '%s\n' "$super" > /tmp/pgpass && chown postgres /tmp/pgpass
gosu postgres initdb -D /tmp/pgdata -U postgres --pwfile=/tmp/pgpass --auth-local=peer --auth-host=scram-sha-256 > /dev/null
rm -f /tmp/pgpass
gosu postgres pg_ctl -D /tmp/pgdata -l /tmp/pg.log -o "-c listen_addresses=127.0.0.1 -p 5432" -w start > /dev/null
sql() { PGPASSWORD="$super" psql -X -At -v ON_ERROR_STOP=1 -h 127.0.0.1 -U postgres -d "$1" -c "$2"; }
check "inside a throwaway container" test -f /.dockerenv
check "fresh cluster created by this run (only the postgres database)" test "$(sql postgres "SELECT string_agg(datname, ',') FROM pg_database WHERE NOT datistemplate")" = postgres
check "server listens on 127.0.0.1 only" test "$(sql postgres 'SHOW listen_addresses')" = 127.0.0.1
PGPASSWORD="$super" psql -X -q -h 127.0.0.1 -U postgres -d postgres -v owner_password="$OWNER_PW" -v app_password="$APP_PW" \
  -v db_name=rokhtiacafe -f /repo/deploy/local/postgres/roles.sql > /dev/null || fatal "bootstrap roles"
LIVE=rokhtiacafe
RESTORED=rokhtiacafe_restore_c6
su_url() { echo "postgres://postgres:$super@127.0.0.1:5432/$1?sslmode=disable"; }
own_url() { echo "postgres://rokh_owner:$OWNER_PW@127.0.0.1:5432/$1?sslmode=disable"; }
app_url() { echo "postgres://rokh_app:$APP_PW@127.0.0.1:5432/$1?sslmode=disable"; }
run "isolation: identity of the source database" "$C6" whoami -db "$(su_url "$LIVE")"
for b in new old; do
  echo "      $b binary: sha256 $(sha256sum "$T/bin/rokh-$b" | cut -d' ' -f1) $(grep -E '^(version|commit):' "/$b/BUILDINFO.txt" 2> /dev/null | tr -s ' ' | tr '\n' ' ')"
done

# Operations files from the committed templates (for backup.sh and restore.sh), test paths only.
SECRET="$("$NEW" secret gen)"
sed -e "s#^DATABASE_URL=.*#DATABASE_URL=$(app_url "$LIVE")#" -e "s#^APP_SECRET_KEY=.*#APP_SECRET_KEY=$SECRET#" \
  -e "s#^UPLOAD_DIR=.*#UPLOAD_DIR=$T/var/uploads#" /repo/deploy/env/production.env.example > "$T/etc/rokh.env"
sed -e "s#^MIGRATION_DATABASE_URL=.*#MIGRATION_DATABASE_URL=$(own_url "$LIVE")#" /repo/deploy/env/migrate.env.example > "$T/etc/migrate.env"
sed -e "s#^ROKH_PG_PORT=.*#ROKH_PG_PORT=5432#" /repo/deploy/env/compose.env.example > "$T/etc/compose.env"
chmod 640 "$T/etc/rokh.env"
chmod 600 "$T/etc/migrate.env" "$T/etc/compose.env"
BK=/backup/rokh-c6
mkdir -m 700 "$BK"
ops() { # ops BINARY FILE
  {
    echo "ROKH_BIN=$1"
    echo "ROKH_ENV_FILE=$T/etc/rokh.env"
    echo "ROKH_MIGRATE_ENV_FILE=$T/etc/migrate.env"
    echo "ROKH_COMPOSE_ENV_FILE=$T/etc/compose.env"
    echo "ROKH_RUN_AS=rokhtest"
    echo "PG_CLIENT=host"
    echo "PG_CONTAINER="
    echo "BACKUP_DIR=$BK"
    echo "BACKUP_REQUIRE_MOUNT=/backup"
    echo "BACKUP_ALLOW_ROOT_FS=no"
    echo "BACKUP_MIN_FREE_MB=1"
    echo "BACKUP_KEEP_DAYS=14"
    echo "BACKUP_KEEP_MIN=3"
  } > "$2"
}
ops "$OLD" "$T/etc/ops-old.env"
ops "$NEW" "$T/etc/ops-new.env"

# serve BIN DB PORT LABEL — the binary as the unprivileged user, test configuration (plain HTTP on
# loopback, so cookies are not Secure). Every binary gets the same APP_SECRET_KEY, as in a rollback.
PID=""
serve() {
  runuser -u rokhtest -- env -i PATH=/usr/bin:/bin APP_ENV=test HTTP_ADDR="127.0.0.1:$3" \
    APP_BASE_URL="http://127.0.0.1:$3" DATABASE_URL="$(app_url "$2")" APP_TIMEZONE=Asia/Tehran \
    COOKIE_SECURE=false ORDERING_HOURS=00:00-24:00 ORDER_PENDING_TTL=24h UPLOAD_DIR="$T/var/uploads" \
    APP_SECRET_KEY="$SECRET" LOG_FORMAT=json LOG_LEVEL=warn "$1" serve > "$T/out/serve-$4.log" 2>&1 &
  PID=$!
}
stop() {
  kill "$PID" 2> /dev/null
  wait "$PID" 2> /dev/null
  if grep -q '"level":"ERROR"' "$T/out/serve-$1.log"; then
    bad "$1: the server logged errors"
    grep '"level":"ERROR"' "$T/out/serve-$1.log" | head -n 5 | sed 's/^/      /'
  else
    ok "$1: server stopped, no errors logged"
  fi
}
rokh_check() { runuser -u rokhtest -- env DATABASE_URL="$(app_url "$2")" "$1" check; }
phase() { # phase NAME PORT
  C6_ADMIN_USER=c6admin C6_ADMIN_PASSWORD="$ADMIN_PW" "$C6" phase "$1" -url "http://127.0.0.1:$2" -state "$T/state.json"
}
snap() { run "snapshot $2" "$C6" snapshot -db "$(su_url "$1")" -out "/evidence/$2.json"; }
status_is() { # status_is BINARY DB "N migrations, 0 pending"
  env MIGRATION_DATABASE_URL="$(own_url "$2")" "$1" migrate status | grep -q "^$3"
}

echo "== 1. legacy database (previous binary)"
run "previous binary: migrate up" env MIGRATION_DATABASE_URL="$(own_url "$LIVE")" "$OLD" migrate up
check "previous binary: 7 migrations, 0 pending" status_is "$OLD" "$LIVE" "7 migrations, 0 pending"
check "no customer tables before 00008" test "$(sql "$LIVE" "SELECT count(*) FROM pg_tables WHERE schemaname = 'rokh' AND tablename LIKE 'customer%'")" = 0
ADMIN_PW="$(rand)"
printf '%s\n' "$ADMIN_PW" | runuser -u rokhtest -- env DATABASE_URL="$(app_url "$LIVE")" "$OLD" user create \
  -username c6admin -display-name "C6 Admin" -role ADMIN > /dev/null || fatal "create the admin user"
serve "$OLD" "$LIVE" 18081 legacy
run "seed legacy data through the previous binary" phase seed-legacy 18081 || { stop legacy; fatal "legacy seed"; }
stop legacy
run "previous binary: rokh check on the legacy database" rokh_check "$OLD" "$LIVE"
snap "$LIVE" S0-legacy || fatal "legacy snapshot"

echo "== 2. backup"
run "backup.sh (previous binary deployed)" env ROKH_OPS_ENV="$T/etc/ops-old.env" /repo/deploy/scripts/backup.sh || fatal "backup"
SET="$(find "$BK" -mindepth 1 -maxdepth 1 -type d -name 'rokh-*' ! -name '*.partial' | sort | tail -n 1)"
check "a backup set exists" test -n "$SET"
check "SHA256SUMS verifies" sh -c "cd '$SET' && sha256sum --quiet --strict -c SHA256SUMS"
check "pg_restore --list reads the dump" sh -c "pg_restore --list '$SET/db.dump' | grep -q 'TABLE DATA rokh orders'"
echo "      dump sha256 $(sha256sum "$SET/db.dump" | cut -d' ' -f1), $(pg_restore --list "$SET/db.dump" | grep -c 'TABLE DATA') table-data entries"

echo "== 3. restore into a new database + forward migration 00008 (C1–C5 binary)"
check "restore target differs from the source" test "$RESTORED" != "$LIVE"
run "restore-target.sql creates $RESTORED" env PGPASSWORD="$super" psql -X -q -h 127.0.0.1 -U postgres -d postgres \
  -v db_name="$RESTORED" -f /repo/deploy/scripts/restore-target.sql || fatal "restore target"
run "restore.sh: pg_restore, migrate up, 0 pending, rokh check" env ROKH_OPS_ENV="$T/etc/ops-new.env" RESTORE_DB="$RESTORED" \
  RESTORE_UPLOADS_DIR="$T/var/uploads.c6" /repo/deploy/scripts/restore.sh "$SET" || fatal "restore"
comment="$(sql postgres "SELECT coalesce(shobj_description(oid, 'pg_database'), '') FROM pg_database WHERE datname = '$RESTORED'")"
check "restore marked VALIDATED" test "${comment#rokh restore VALIDATED:}" != "$comment"
check "C1–C5 binary: 8 migrations, 0 pending" status_is "$NEW" "$RESTORED" "8 migrations, 0 pending"
run "C1–C5 binary: rokh check on the migrated database" rokh_check "$NEW" "$RESTORED"
snap "$RESTORED" S1-restored-migrated || fatal "restored snapshot"
run "every legacy row identical after restore + 00008; customer tables new and empty" \
  "$C6" compare -label restore -mode restore -before /evidence/S0-legacy.json -after /evidence/S1-restored-migrated.json \
  -new customers,customer_sessions,customer_events,customer_order_links
q() { sql "$RESTORED" "$1"; }
check "customer tables owned by rokh_owner" test "$(q "SELECT string_agg(DISTINCT tableowner, ',') FROM pg_tables WHERE schemaname = 'rokh' AND tablename LIKE 'customer%'")" = rokh_owner
check "rokh_app may DELETE only customer_sessions" test \
  "$(q "SELECT string_agg(t || '=' || has_table_privilege('rokh_app', 'rokh.' || t, 'DELETE'), ',' ORDER BY t COLLATE \"C\") FROM unnest(ARRAY['customer_events','customer_order_links','customer_sessions','customers']) t")" = \
  "customer_events=false,customer_order_links=false,customer_sessions=true,customers=false"
check "rokh_app has no TRUNCATE on customer tables" test "$(q "SELECT bool_or(has_table_privilege('rokh_app', 'rokh.' || t, 'TRUNCATE')) FROM unnest(ARRAY['customer_events','customer_order_links','customer_sessions','customers']) t")" = f
check "append-only triggers on customer_events and customer_order_links" test "$(q "SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgrelid IN ('rokh.customer_events'::regclass, 'rokh.customer_order_links'::regclass)")" = 6
check "links reference orders and customers with ON DELETE RESTRICT" test "$(q "SELECT count(*) FROM pg_constraint WHERE contype = 'f' AND confdeltype = 'r' AND conrelid = 'rokh.customer_order_links'::regclass")" = 2
check "customer phone is unique" test "$(q "SELECT count(*) FROM pg_constraint WHERE conrelid = 'rokh.customers'::regclass AND contype = 'u'")" -ge 1
check "legacy orders have no customer attribution" test "$(q 'SELECT count(*) FROM rokh.customer_order_links')" = 0

echo "== 4. C1–C5 binary on the restored, migrated database"
serve "$NEW" "$RESTORED" 18082 c1c5
run "C1–C5 acceptance: legacy data, accounts, isolation, idempotency, CSRF, reports, exports" phase verify-new 18082
stop c1c5
run "C1–C5 binary: rokh check" rokh_check "$NEW" "$RESTORED"
snap "$RESTORED" S2-c1c5
run "every S1 row unchanged by C1–C5 activity" "$C6" compare -label c1c5 -mode preserve -before /evidence/S1-restored-migrated.json \
  -after /evidence/S2-c1c5.json -volatile sessions,customer_sessions,users,customers,inventory_balances
check "customer links are immutable for rokh_app (RK002)" sh -c "! out=\$(PGPASSWORD='$APP_PW' psql -X -q -v ON_ERROR_STOP=1 -v VERBOSITY=verbose -h 127.0.0.1 -U rokh_app -d $RESTORED -c 'UPDATE rokh.customer_order_links SET customer_id = customer_id' 2>&1) && echo \"\$out\" | grep -q RK002"
check "customer events cannot be deleted by rokh_app" sh -c "! PGPASSWORD='$APP_PW' psql -X -q -v ON_ERROR_STOP=1 -h 127.0.0.1 -U rokh_app -d $RESTORED -c 'DELETE FROM rokh.customer_events' > /dev/null 2>&1"

echo "== 5. binary rollback: previous binary on the migrated database (no down-migration)"
serve "$OLD" "$RESTORED" 18083 rollback
run "previous binary: legacy flows, tracking, payments, stock; customer routes absent" phase verify-old 18083
stop rollback
run "previous binary: rokh check on the migrated database" rokh_check "$OLD" "$RESTORED"
run "C1–C5 binary: rokh check after the rollback window" rokh_check "$NEW" "$RESTORED"
check "migration status unchanged by the rollback (8 applied, 0 pending)" status_is "$NEW" "$RESTORED" "8 migrations, 0 pending"
snap "$RESTORED" S3-rollback
run "every S2 row unchanged; customer data frozen during the rollback" "$C6" compare -label rollback -mode preserve \
  -before /evidence/S2-c1c5.json -after /evidence/S3-rollback.json -volatile sessions,users,inventory_balances \
  -frozen customers,customer_sessions,customer_events,customer_order_links

echo "== 6. roll forward: C1–C5 binary again"
serve "$NEW" "$RESTORED" 18084 rollforward
run "C1–C5 binary: accounts, history, ownership and reports intact" phase verify-roll-forward 18084
stop rollforward
run "C1–C5 binary: rokh check after roll-forward" rokh_check "$NEW" "$RESTORED"
snap "$RESTORED" S4-rollforward
run "every S3 row unchanged by the roll-forward" "$C6" compare -label rollforward -mode preserve -before /evidence/S3-rollback.json \
  -after /evidence/S4-rollforward.json -volatile sessions,customer_sessions,users,customers,inventory_balances

echo "== 7. the source database is untouched"
snap "$LIVE" S5-source-after
run "source database identical to S0" "$C6" compare -label source -mode restore -before /evidence/S0-legacy.json \
  -after /evidence/S5-source-after.json

cp "$T/out/"serve-*.log /evidence/ 2> /dev/null
chmod -R a+rX /evidence 2> /dev/null
summary
[ "$failed" = 0 ]
