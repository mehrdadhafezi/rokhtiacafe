#!/usr/bin/env bash
# Deployment test suite (T14-08): CLI, configuration, release checksums, backup, retention and
# restore, against a DISPOSABLE PostgreSQL cluster only. Never point it at a real database.
#
# Usually started by deploy/test/in-docker.sh (host client tools, fresh cluster inside a throwaway
# container) or deploy/test/compose.sh (PostgreSQL from deploy/postgres/compose.yaml, docker client).
#
# Required environment:
#   ROKH_RELEASE_DIR   directory with rokh-linux-amd64 (release build, frontend embedded), .sha256, BUILDINFO.txt
#   TEST_PG_HOST, TEST_PG_PORT   where the disposable server listens (as seen from this machine)
#   TEST_PG_DISPOSABLE=yes       explicit confirmation that the cluster is throwaway
#   PG_CLIENT=host|docker  (+ PG_CONTAINER for docker)
#   host:   TEST_PG_SUPERPASS  superuser "postgres" password
#   docker: the container lets `docker exec -u postgres` run psql (peer authentication)
# Optional: TEST_BACKUP_MOUNT (a real mount point for BACKUP_DIR), TEST_RUN_AS (unprivileged user
# for the rokh binary when running as root).
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
scripts="$repo/deploy/scripts"

[ "${TEST_PG_DISPOSABLE:-}" = yes ] || { echo "refusing: set TEST_PG_DISPOSABLE=yes for a throwaway cluster" >&2; exit 1; }
: "${ROKH_RELEASE_DIR:?}" "${TEST_PG_HOST:?}" "${TEST_PG_PORT:?}" "${PG_CLIENT:?}"
export PG_CLIENT PG_CONTAINER="${PG_CONTAINER:-}"

OWNER_PW="owner$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')"
APP_PW="app$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')"
LIVE_DB=rokhtiacafe

T="$(mktemp -d)"
chmod 755 "$T"
mkdir -p "$T/bin" "$T/etc" "$T/var/uploads/products" "$T/out" "$T/shim"
cp "$ROKH_RELEASE_DIR/rokh-linux-amd64" "$T/bin/rokh"
chmod 755 "$T/bin/rokh"
BIN="$T/bin/rokh"

passed=0 failed=0
ok() { echo "PASS  $*"; passed=$((passed + 1)); }
bad() { echo "FAIL  $*"; failed=$((failed + 1)); }
# expect CODE NAME CMD... — run CMD, require its exit code; output goes to $T/out/last.
expect() {
  local want=$1 name=$2
  shift 2
  "$@" > "$T/out/last" 2>&1
  local got=$?
  cat "$T/out/last" >> "$T/out/all"
  if [ "$got" = "$want" ]; then ok "$name"; else
    bad "$name (exit $got, want $want)"
    sed 's/^/      /' "$T/out/last" | tail -n 15
  fi
}
out_has() { # out_has NAME PATTERN
  if grep -Eq -- "$2" "$T/out/last"; then ok "$1"; else bad "$1 (output lacks /$2/)"; sed 's/^/      /' "$T/out/last" | tail -n 8; fi
}
check() { # check NAME CONDITION...
  local name=$1
  shift
  if "$@"; then ok "$name"; else bad "$name"; fi
}

admin_sql() { # admin_sql DB SQL — superuser query on the disposable cluster
  if [ "$PG_CLIENT" = docker ]; then
    docker exec -i -u postgres "$PG_CONTAINER" psql -X -At -v ON_ERROR_STOP=1 -d "$1" -c "$2"
  else
    PGPASSWORD="$TEST_PG_SUPERPASS" psql -X -At -v ON_ERROR_STOP=1 -h "$TEST_PG_HOST" -p "$TEST_PG_PORT" -U postgres -d "$1" -c "$2"
  fi
}
admin_file() { # admin_file DB FILE [psql -v args...]
  local db=$1 f=$2
  shift 2
  if [ "$PG_CLIENT" = docker ]; then
    docker exec -i -u postgres "$PG_CONTAINER" psql -X -q -d "$db" "$@" < "$f"
  else
    PGPASSWORD="$TEST_PG_SUPERPASS" psql -X -q -h "$TEST_PG_HOST" -p "$TEST_PG_PORT" -U postgres -d "$db" "$@" < "$f"
  fi
}
counts() { # counts DB — "table count" for every rokh table
  admin_sql "$1" "SELECT table_name || ' ' || (xpath('/row/c/text()', query_to_xml(format('SELECT count(*) AS c FROM rokh.%I', table_name), false, true, '')))[1]
                  FROM information_schema.tables WHERE table_schema = 'rokh' AND table_type = 'BASE TABLE' ORDER BY 1"
}

echo "== setup (cluster $TEST_PG_HOST:$TEST_PG_PORT, client $PG_CLIENT)"
admin_file postgres "$repo/deploy/local/postgres/roles.sql" -v owner_password="$OWNER_PW" -v app_password="$APP_PW" -v db_name="$LIVE_DB" > /dev/null ||
  { echo "cannot bootstrap roles" >&2; exit 1; }
# Roles may already exist (compose initialised them): make the passwords match this run.
admin_sql postgres "ALTER ROLE rokh_owner PASSWORD '$OWNER_PW'; ALTER ROLE rokh_app PASSWORD '$APP_PW'" > /dev/null

dburl() { echo "postgres://$1:$2@$TEST_PG_HOST:$TEST_PG_PORT/$3?sslmode=disable"; }
# rokh.env generated from the committed template: only placeholders and test paths are replaced.
secret="$("$BIN" secret gen)"
sed -e "s#^DATABASE_URL=.*#DATABASE_URL=$(dburl rokh_app "$APP_PW" "$LIVE_DB")#" \
  -e "s#^APP_SECRET_KEY=.*#APP_SECRET_KEY=$secret#" \
  -e "s#^UPLOAD_DIR=.*#UPLOAD_DIR=$T/var/uploads#" \
  "$repo/deploy/env/production.env.example" > "$T/etc/rokh.env"
sed -e "s#^MIGRATION_DATABASE_URL=.*#MIGRATION_DATABASE_URL=$(dburl rokh_owner "$OWNER_PW" "$LIVE_DB")#" \
  "$repo/deploy/env/migrate.env.example" > "$T/etc/migrate.env"
chmod 640 "$T/etc/rokh.env"
chmod 600 "$T/etc/migrate.env"
if grep -h CHANGE_ME "$T/etc/rokh.env" "$T/etc/migrate.env" | grep -qv '^VAPID_SUBJECT='; then
  echo "a CHANGE_ME placeholder was not replaced" >&2
  exit 1
fi

if [ -n "${TEST_BACKUP_MOUNT:-}" ]; then
  BK="$TEST_BACKUP_MOUNT/rokh-test"
  mkdir -m 700 "$BK"
  REQUIRE_MOUNT="$TEST_BACKUP_MOUNT"
  ALLOW_ROOT=no
else
  BK="$T/backup"
  mkdir -m 700 "$BK"
  REQUIRE_MOUNT=""
  ALLOW_ROOT=yes
fi
RUN_AS="${TEST_RUN_AS:-}"
if [ -n "$RUN_AS" ]; then chown -R "$RUN_AS" "$T/var/uploads"; fi
cat > "$T/etc/ops.env" << EOF
ROKH_BIN=$BIN
ROKH_ENV_FILE=$T/etc/rokh.env
ROKH_MIGRATE_ENV_FILE=$T/etc/migrate.env
ROKH_RUN_AS=$RUN_AS
PG_CLIENT=$PG_CLIENT
PG_CONTAINER=$PG_CONTAINER
BACKUP_DIR=$BK
BACKUP_REQUIRE_MOUNT=$REQUIRE_MOUNT
BACKUP_ALLOW_ROOT_FS=$ALLOW_ROOT
BACKUP_MIN_FREE_MB=1
BACKUP_KEEP_DAYS=14
BACKUP_KEEP_MIN=3
EOF
export ROKH_OPS_ENV="$T/etc/ops.env"

# Host mode: shims record the arguments of every PostgreSQL client call (password leak check).
if [ "$PG_CLIENT" = host ]; then
  for tool in pg_dump pg_restore psql; do
    real="$(command -v "$tool")"
    printf '#!/bin/sh\necho "%s $*" >> "%s/argv.log"\nexec "%s" "$@"\n' "$tool" "$T/out" "$real" > "$T/shim/$tool"
    chmod 755 "$T/shim/$tool"
  done
  export PATH="$T/shim:$PATH"
fi

echo "== CLI compatibility"
expect 0 "rokh version" "$BIN" version
expect 0 "rokh secret gen" "$BIN" secret gen
check "secret gen decodes to 32 bytes" test "$(base64 -d < "$T/out/last" | wc -c)" = 32
expect 0 "rokh vapid gen" "$BIN" vapid gen
out_has "vapid gen prints both keys" '^VAPID_PUBLIC_KEY=.{80,}$'
out_has "vapid gen prints the private key" '^VAPID_PRIVATE_KEY=.{40,}$'
expect 1 "rokh migrate (bad subcommand) fails" env MIGRATION_DATABASE_URL=x "$BIN" migrate down
expect 1 "rokh config (no subcommand) fails" "$BIN" config

echo "== schema and seed data"
expect 0 "migrate.sh up" "$scripts/migrate.sh" up
expect 0 "migrate.sh status" "$scripts/migrate.sh" status
out_has "no pending migrations" ', 0 pending$'
expect 1 "migrate.sh rejects other subcommands" "$scripts/migrate.sh" down
app_url="$(dburl rokh_app "$APP_PW" "$LIVE_DB")"
printf 'deploy-test-pw-1\n' | (cd / && DATABASE_URL="$app_url" "$BIN" user create -username owner -display-name 'مالک' -role ADMIN) > /dev/null
admin_sql "$LIVE_DB" "INSERT INTO rokh.categories (name) VALUES ('چای'), ('قهوه');
  INSERT INTO rokh.expenses (category, amount_rial, expense_date, description, idempotency_key, created_by)
  SELECT 'RENT', 150000000, '2026-10-01', 'اجاره مهر', gen_random_uuid(), id FROM rokh.users LIMIT 1" > /dev/null
head -c 2048 /dev/urandom > "$T/var/uploads/products/0123456789abcdef0123456789abcdef.jpg"
head -c 4096 /dev/urandom > "$T/var/uploads/products/fedcba9876543210fedcba9876543210.jpg"
touch "$T/var/uploads/products/.upload-123"
[ -z "$RUN_AS" ] || chown -R "$RUN_AS" "$T/var/uploads"

echo "== configuration"
expect 0 "preflight passes with the template settings" "$scripts/preflight.sh"
out_has "preflight: config check ready" 'ready to serve'
out_has "preflight: listens on 127.0.0.1:18080" 'listen +127.0.0.1:18080'
cfg_fail() { # cfg_fail NAME PATTERN KEY=VALUE — config check with one setting changed must fail
  local name=$1 pat=$2 kv=$3
  (
    while IFS= read -r k; do unset "$k"; done < <(grep -Eo '^[A-Za-z_][A-Za-z0-9_]*=' "$T/etc/rokh.env" | tr -d =)
    export "${kv?}"
    . "$scripts/lib.sh"
    env_export_file "$T/etc/rokh.env"
    cd / && "$BIN" config check
  ) > "$T/out/last" 2>&1
  if [ $? -ne 0 ] && grep -Eq -- "$pat" "$T/out/last"; then ok "config check rejects $name"; else
    bad "config check rejects $name"
    sed 's/^/      /' "$T/out/last" | tail -n 6
  fi
}
cfg_fail "missing APP_SECRET_KEY" 'APP_SECRET_KEY is required' APP_SECRET_KEY=
cfg_fail "short APP_SECRET_KEY" 'at least 32 bytes' APP_SECRET_KEY=c2hvcnQ=
cfg_fail "public HTTP_ADDR" 'loopback' HTTP_ADDR=0.0.0.0:18080
cfg_fail "missing TRUSTED_PROXIES" 'TRUSTED_PROXIES' TRUSTED_PROXIES=
cfg_fail "malformed TRUSTED_PROXIES" 'invalid entry' TRUSTED_PROXIES=not-an-ip
cfg_fail "relative UPLOAD_DIR" 'absolute' UPLOAD_DIR=uploads
cfg_fail "owner role at runtime" 'DML-only' "DATABASE_URL=$(dburl rokh_owner "$OWNER_PW" "$LIVE_DB")"
cfg_fail "superuser at runtime" 'DML-only' "DATABASE_URL=$(dburl postgres x "$LIVE_DB")"
cfg_fail "plain-http APP_BASE_URL" 'https APP_BASE_URL' APP_BASE_URL=http://rokh.tiacafe.ir
cfg_fail "missing DATABASE_URL" 'DATABASE_URL is required' DATABASE_URL=
cfg_fail "wrong database password" 'database' "DATABASE_URL=$(dburl rokh_app wrong "$LIVE_DB")"
cfg_fail "half-configured VAPID" 'VAPID_PUBLIC_KEY' VAPID_PRIVATE_KEY=abc
cfg_fail "missing upload directory" 'UPLOAD_DIR does not exist' UPLOAD_DIR=/nonexistent/uploads

cp "$T/etc/migrate.env" "$T/etc/migrate.env.bak"
chmod 644 "$T/etc/migrate.env"
expect 1 "preflight rejects a world-readable migrate.env" "$scripts/preflight.sh"
chmod 600 "$T/etc/migrate.env"
cp "$T/etc/rokh.env" "$T/etc/rokh.env.bak"
grep '^MIGRATION_DATABASE_URL=' "$T/etc/migrate.env" >> "$T/etc/rokh.env"
expect 1 "preflight rejects owner credentials in rokh.env" "$scripts/preflight.sh"
out_has "preflight names the problem" 'MIGRATION_DATABASE_URL is in'
cp "$T/etc/rokh.env.bak" "$T/etc/rokh.env"

echo "== release checksums"
expect 0 "verify-release accepts the build" "$scripts/verify-release.sh" "$ROKH_RELEASE_DIR"
sum="$(cut -d' ' -f1 "$ROKH_RELEASE_DIR/rokh-linux-amd64.sha256")"
expect 0 "verify-release accepts the expected SHA-256" "$scripts/verify-release.sh" "$ROKH_RELEASE_DIR" "$sum"
expect 1 "verify-release rejects a different expected SHA-256" "$scripts/verify-release.sh" "$ROKH_RELEASE_DIR" "$(printf '0%.0s' $(seq 64))"
mkdir "$T/rel"
cp "$ROKH_RELEASE_DIR"/rokh-linux-amd64 "$ROKH_RELEASE_DIR"/rokh-linux-amd64.sha256 "$ROKH_RELEASE_DIR"/BUILDINFO.txt "$T/rel/"
printf 'x' >> "$T/rel/rokh-linux-amd64"
expect 1 "verify-release rejects a modified binary" "$scripts/verify-release.sh" "$T/rel"
cp "$ROKH_RELEASE_DIR/rokh-linux-amd64" "$T/rel/"
echo "garbage" > "$T/rel/rokh-linux-amd64.sha256"
expect 1 "verify-release rejects a malformed checksum file" "$scripts/verify-release.sh" "$T/rel"

echo "== backup"
expect 0 "backup succeeds" "$scripts/backup.sh"
mapfile -t sets < <(find "$BK" -mindepth 1 -maxdepth 1 -type d -name 'rokh-*' -printf '%f\n')
check "exactly one set published" test "${#sets[@]}" = 1
SET="$BK/${sets[0]:-none}"
check "set has dump, uploads, INFO, SHA256SUMS" test -s "$SET/db.dump" -a -s "$SET/uploads.tar.gz" -a -s "$SET/INFO" -a -s "$SET/SHA256SUMS"
check "set checksums verify" sh -c "cd '$SET' && sha256sum --quiet -c SHA256SUMS"
check "set directory is private (700)" test "$(stat -c %a "$SET")" = 700
check "set files are private (600)" test "$(stat -c %a "$SET"/* | sort -u)" = 600
check "no partial directory left" test -z "$(find "$BK" -maxdepth 1 -name '.*.partial')"
check "upload temp files are not archived" sh -c "! tar -tzf '$SET/uploads.tar.gz' | grep -q upload-123"
check "both images archived" test "$(tar -tzf "$SET/uploads.tar.gz" | grep -c '\.jpg$')" = 2
check "INFO names PostgreSQL 16+" grep -Eq '^server: +PostgreSQL (1[6-9]|[2-9][0-9])' "$SET/INFO"

variant=0
bk_ops() { # bk_ops KEY=VALUE... — run backup.sh with an ops file where these keys are replaced
  variant=$((variant + 1))
  local f="$T/etc/ops.v$variant" kv
  cp "$T/etc/ops.env" "$f"
  for kv in "$@"; do
    sed -i "/^${kv%%=*}=/d" "$f"
    [ "${kv#*=}" = "<unset>" ] || echo "$kv" >> "$f"
  done
  ROKH_OPS_ENV="$f" "$scripts/backup.sh"
}
expect 1 "backup refuses without BACKUP_DIR" bk_ops "BACKUP_DIR=<unset>"
expect 1 "backup refuses a missing BACKUP_DIR" bk_ops BACKUP_DIR="$BK/missing"
check "missing BACKUP_DIR was not created" test ! -e "$BK/missing"
expect 1 "backup refuses a relative BACKUP_DIR" bk_ops BACKUP_DIR=backup
expect 1 "backup refuses the wrong mount point" bk_ops BACKUP_REQUIRE_MOUNT=/nonexistent-mount
out_has "mount refusal is explained" 'is the backup disk mounted'
mkdir -m 700 "$T/rootfs-backup"
if [ "$(df -P "$T/rootfs-backup" | awk 'NR==2{print $6}')" = / ]; then
  expect 1 "backup refuses the root filesystem" bk_ops BACKUP_DIR="$T/rootfs-backup" "BACKUP_REQUIRE_MOUNT=<unset>" BACKUP_ALLOW_ROOT_FS=no
  out_has "root filesystem refusal is explained" 'root filesystem'
else
  echo "SKIP  backup refuses the root filesystem (temp dir is not on /)"
fi
mkdir -m 777 "$T/open-backup"
expect 1 "backup refuses a world-writable BACKUP_DIR" bk_ops BACKUP_DIR="$T/open-backup" "BACKUP_REQUIRE_MOUNT=<unset>" BACKUP_ALLOW_ROOT_FS=yes
expect 1 "backup refuses when free space is short" bk_ops BACKUP_MIN_FREE_MB=999999999
out_has "free-space refusal is explained" 'MB free'
expect 1 "backup refuses invalid retention" bk_ops BACKUP_KEEP_DAYS=0
sed "s#rokh_owner:$OWNER_PW@#rokh_owner:wrong@#" "$T/etc/migrate.env" > "$T/etc/migrate.bad"
chmod 600 "$T/etc/migrate.bad"
expect 2 "backup fails on a database error" bk_ops ROKH_MIGRATE_ENV_FILE="$T/etc/migrate.bad"
check "failed backup published nothing" test "$(find "$BK" -mindepth 1 -maxdepth 1 -type d | wc -l)" = 1
check "failed backup left no partial directory" test -z "$(find "$BK" -maxdepth 1 -name '.*.partial')"
sed "s#^UPLOAD_DIR=.*#UPLOAD_DIR=$T/var/nope#" "$T/etc/rokh.env" > "$T/etc/rokh.noupload"
expect 1 "backup refuses a missing UPLOAD_DIR" bk_ops ROKH_ENV_FILE="$T/etc/rokh.noupload"
(
  exec 9> "$BK/.rokh-backup.lock"
  flock 9
  sleep 5
) &
sleep 1
expect 1 "backup refuses while another backup runs" "$scripts/backup.sh"
out_has "lock refusal is explained" 'another backup is running'
wait

echo "== retention"
mkset() { # mkset NAME [extra file] — a fake complete set with an old timestamp
  mkdir -m 700 "$BK/$1"
  for f in db.dump uploads.tar.gz INFO SHA256SUMS; do echo old > "$BK/$1/$f"; done
  [ -z "${2:-}" ] || echo keep > "$BK/$1/$2"
}
for d in 01 02 03 04 05 06; do mkset "rokh-202001${d}-030000"; done
mkset rokh-20200107-030000 notes.txt
recent="rokh-$(date -d '-2 days' +%Y%m%d)-030000"
mkset "$recent"
mkdir "$BK/rokh-notes" && echo x > "$BK/rokh-notes/f"
mkdir "$BK/rokh-20200101-000000-copy" && echo x > "$BK/rokh-20200101-000000-copy/db.dump"
echo x > "$BK/other-app.dump"
echo x > "$BK/rokh-20200101-000000.dump"
first_set="$(basename "$SET")"
sleep 1
expect 3 "retention reports the set it had to keep" "$scripts/backup.sh"
newest() { find "$BK" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | grep -E '^rokh-[0-9]{8}-[0-9]{6}$' | sort | tail -n 1; }
second_set="$(newest)"
echo "      remaining: $(find "$BK" -mindepth 1 -maxdepth 1 -printf '%f\n' | sort | tr '\n' ' ')"
check "retention kept the new set and the previous one" test -d "$BK/$second_set" -a -d "$BK/$first_set" -a "$second_set" != "$first_set"
check "retention kept sets within BACKUP_KEEP_DAYS" test -d "$BK/$recent"
check "retention removed old complete sets" test ! -e "$BK/rokh-20200101-030000" -a ! -e "$BK/rokh-20200106-030000"
check "retention kept an old set with unexpected files" test -f "$BK/rokh-20200107-030000/notes.txt" -a -f "$BK/rokh-20200107-030000/db.dump"
check "retention ignored non-matching names" test -f "$BK/rokh-notes/f" -a -f "$BK/rokh-20200101-000000-copy/db.dump" -a -f "$BK/other-app.dump" -a -f "$BK/rokh-20200101-000000.dump"
# Keep-minimum: once the recent set is older than KEEP_DAYS, the newest KEEP_MIN still survive.
sleep 1
expect 3 "retention with KEEP_DAYS=1 keeps the newest KEEP_MIN" bk_ops BACKUP_KEEP_DAYS=1 BACKUP_KEEP_MIN=3
third_set="$(newest)"
check "KEEP_MIN: the three newest sets remain" test -d "$BK/$third_set" -a -d "$BK/$second_set" -a -d "$BK/$first_set"
check "KEEP_MIN: an older set beyond KEEP_DAYS was removed" test ! -e "$BK/$recent"

echo "== restore"
R1=rokhtiacafe_restore_t1
R2=rokhtiacafe_restore_t2
expect 0 "restore-target.sql creates $R1" admin_file postgres "$scripts/restore-target.sql" -v db_name="$R1"
expect 0 "restore-target.sql creates $R2" admin_file postgres "$scripts/restore-target.sql" -v db_name="$R2"
expect 3 "restore-target.sql refuses an existing database" admin_file postgres "$scripts/restore-target.sql" -v db_name="$R1"
expect 3 "restore-target.sql refuses a name without _restore_" admin_file postgres "$scripts/restore-target.sql" -v db_name=rokhtiacafe_copy
expect 3 "restore-target.sql refuses an unsafe name" admin_file postgres "$scripts/restore-target.sql" -v 'db_name=x_restore_"; DROP'

SET="$BK/$third_set"
UPL="$T/var/uploads.restore-t1"
expect 0 "restore into a prepared empty database" env RESTORE_DB="$R1" RESTORE_UPLOADS_DIR="$UPL" "$scripts/restore.sh" "$SET"
check "restored row counts match the live database" test "$(counts "$LIVE_DB")" = "$(counts "$R1")"
check "restored data is not empty" test "$(admin_sql "$R1" 'SELECT count(*) FROM rokh.categories')" = 2
check "Persian text survives" test "$(admin_sql "$R1" "SELECT description FROM rokh.expenses")" = 'اجاره مهر'
check "restored tables are owned by rokh_owner" test "$(admin_sql "$R1" "SELECT DISTINCT tableowner FROM pg_tables WHERE schemaname = 'rokh'")" = rokh_owner
check "rokh_app is still DML-only on orders" test "$(admin_sql "$R1" "SELECT has_table_privilege('rokh_app', 'rokh.orders', 'SELECT') AND NOT has_table_privilege('rokh_app', 'rokh.orders', 'DELETE') AND NOT has_table_privilege('rokh_app', 'rokh.orders', 'TRUNCATE')")" = t
check "protective triggers restored" test "$(admin_sql "$R1" "SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgrelid = 'rokh.orders'::regclass")" = "$(admin_sql "$LIVE_DB" "SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgrelid = 'rokh.orders'::regclass")"
check "uploads restored identically (minus temp files)" diff -r -x '.upload-*' "$T/var/uploads/products" "$UPL/products"
check "upload temp files were not restored" test ! -e "$UPL/products/.upload-123"
check "live uploads untouched" test -f "$T/var/uploads/products/.upload-123"
if [ -n "$RUN_AS" ]; then check "restored uploads belong to $RUN_AS" test "$(stat -c %U "$UPL/products")" = "$RUN_AS"; fi

expect 1 "restore refuses a non-empty target" env RESTORE_DB="$R1" "$scripts/restore.sh" "$SET"
out_has "non-empty refusal is explained" 'is not empty'
expect 1 "restore refuses the live database name" env RESTORE_DB="$LIVE_DB" "$scripts/restore.sh" "$SET"
sed "s#/$LIVE_DB?#/rokhtiacafe_restore_live?#" "$T/etc/migrate.env" > "$T/etc/migrate.live"
chmod 600 "$T/etc/migrate.live"
expect 1 "restore refuses the configured live database" env RESTORE_DB=rokhtiacafe_restore_live ROKH_MIGRATE_ENV_FILE="$T/etc/migrate.live" "$scripts/restore.sh" "$SET"
out_has "live refusal is explained" 'refusing to restore over'
expect 1 "restore refuses a database that does not exist" env RESTORE_DB=rokhtiacafe_restore_missing "$scripts/restore.sh" "$SET"
admin_sql postgres "CREATE DATABASE rokhtiacafe_restore_wrongowner" > /dev/null
admin_sql rokhtiacafe_restore_wrongowner "GRANT CONNECT ON DATABASE rokhtiacafe_restore_wrongowner TO rokh_owner" > /dev/null
expect 1 "restore refuses a target owned by another role" env RESTORE_DB=rokhtiacafe_restore_wrongowner "$scripts/restore.sh" "$SET"
out_has "owner refusal is explained" 'identity mismatch'
expect 1 "restore refuses an invalid target name" env 'RESTORE_DB=x_restore_; DROP' "$scripts/restore.sh" "$SET"
expect 1 "restore refuses without RESTORE_DB" env -u RESTORE_DB "$scripts/restore.sh" "$SET"
expect 1 "restore refuses a missing set" env RESTORE_DB="$R2" "$scripts/restore.sh" "$BK/rokh-20991231-000000"
expect 1 "restore refuses a directory that is not a set" env RESTORE_DB="$R2" "$scripts/restore.sh" "$BK/rokh-notes"

forge() { # forge NAME — copy the good set to a new directory under $T/sets
  mkdir -p "$T/sets"
  cp -a "$SET" "$T/sets/$1"
  echo "$T/sets/$1"
}
resum() { (cd "$1" && sha256sum db.dump uploads.tar.gz INFO > SHA256SUMS); }
S="$(forge rokh-20260101-000001)"
printf 'x' >> "$S/db.dump"
expect 1 "restore refuses a damaged dump (checksum)" env RESTORE_DB="$R2" "$scripts/restore.sh" "$S"
out_has "checksum refusal is explained" 'checksum mismatch'
S="$(forge rokh-20260101-000002)"
head -c 5000 /dev/urandom > "$S/db.dump" && resum "$S"
expect 1 "restore refuses a file that is not a dump" env RESTORE_DB="$R2" "$scripts/restore.sh" "$S"
S="$(forge rokh-20260101-000003)"
echo "0000  ../../etc/passwd" >> "$S/SHA256SUMS"
expect 1 "restore refuses a malformed SHA256SUMS" env RESTORE_DB="$R2" "$scripts/restore.sh" "$S"
S="$(forge rokh-20260101-000004)"
rm "$S/SHA256SUMS"
expect 1 "restore refuses a set without SHA256SUMS" env RESTORE_DB="$R2" "$scripts/restore.sh" "$S"

mkdir -p "$T/evil/a" "$T/evil/b"
echo pwned > "$T/evil/x"
S="$(forge rokh-20260101-000005)"
(cd "$T/evil/a" && tar -czPf "$S/uploads.tar.gz" ../x)
resum "$S"
expect 1 "restore refuses an archive with ../ entries" env RESTORE_DB="$R2" RESTORE_UPLOADS_DIR="$T/var/up-evil1" "$scripts/restore.sh" "$S"
check "nothing unpacked for the ../ archive" test ! -e "$T/var/up-evil1" -a ! -e "$T/var/x"
S="$(forge rokh-20260101-000006)"
ln -s /etc "$T/evil/b/link"
tar -czf "$S/uploads.tar.gz" -C "$T/evil/b" .
resum "$S"
expect 1 "restore refuses an archive with a symlink" env RESTORE_DB="$R2" RESTORE_UPLOADS_DIR="$T/var/up-evil2" "$scripts/restore.sh" "$S"
check "nothing unpacked for the symlink archive" test ! -e "$T/var/up-evil2"
S="$(forge rokh-20260101-000007)"
tar -czPf "$S/uploads.tar.gz" "$T/evil/x"
resum "$S"
expect 1 "restore refuses an archive with absolute paths" env RESTORE_DB="$R2" RESTORE_UPLOADS_DIR="$T/var/up-evil3" "$scripts/restore.sh" "$S"
mkdir "$T/var/existing" && echo mine > "$T/var/existing/keep"
expect 1 "restore refuses an existing RESTORE_UPLOADS_DIR" env RESTORE_DB="$R2" RESTORE_UPLOADS_DIR="$T/var/existing" "$scripts/restore.sh" "$SET"
check "existing directory untouched" test "$(cat "$T/var/existing/keep")" = mine
expect 1 "restore refuses the live UPLOAD_DIR" env RESTORE_DB="$R2" RESTORE_UPLOADS_DIR="$T/var/uploads" "$scripts/restore.sh" "$SET"
expect 1 "restore refuses a relative RESTORE_UPLOADS_DIR" env RESTORE_DB="$R2" RESTORE_UPLOADS_DIR=uploads.new "$scripts/restore.sh" "$SET"
check "refused restores left $R2 empty" test "$(admin_sql "$R2" "SELECT count(*) FROM pg_namespace WHERE nspname = 'rokh'")" = 0
check "live database unchanged by the restore tests" test "$(counts "$LIVE_DB")" = "$(counts "$R1")"
expect 0 "database-only restore (no RESTORE_UPLOADS_DIR)" env RESTORE_DB="$R2" "$scripts/restore.sh" "$SET"
out_has "uploads skip is reported" 'uploads not restored'

echo "== secrets"
check "passwords never in tool arguments" sh -c "[ ! -f '$T/out/argv.log' ] || ! grep -Fq -e '$OWNER_PW' -e '$APP_PW' '$T/out/argv.log'"
[ "$PG_CLIENT" = host ] && check "argument log was recorded" test -s "$T/out/argv.log"
check "passwords never printed by the scripts" sh -c "! grep -Fq -e '$OWNER_PW' -e '$APP_PW' '$T/out/all'"
check "INFO files contain no passwords" sh -c "! grep -rFq -e '$OWNER_PW' -e '$APP_PW' '$BK'/rokh-2026*/INFO"
check "APP_SECRET_KEY never printed" sh -c "! grep -Fq -e '$secret' '$T/out/all'"

echo
echo "deploy tests: $passed passed, $failed failed"
[ "$failed" -eq 0 ]
