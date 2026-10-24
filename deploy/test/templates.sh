#!/usr/bin/env bash
# Static integrity checks for the deployment templates (T14-08). No database, no network:
#   deploy/test/templates.sh
# Uses shellcheck, docker compose and systemd-analyze when they are installed (CI has them).
set -uo pipefail
repo="$(cd "$(dirname "$0")/../.." && pwd)"
d="$repo/deploy"
fails=0
ok() { echo "PASS  $*"; }
bad() { echo "FAIL  $*"; fails=$((fails + 1)); }
check() {
  local name=$1
  shift
  if "$@"; then ok "$name"; else bad "$name"; fi
}
val() { grep -E "^$2=" "$1" | head -n 1 | cut -d= -f2-; }

env_prod="$d/env/production.env.example"
env_mig="$d/env/migrate.env.example"
env_ops="$d/env/ops.env.example"
env_pg="$d/env/postgres.env.example"
env_cmp="$d/env/compose.env.example"
init="$d/local/postgres/10-rokh-init.sh"
compose="$d/postgres/compose.yaml"
unit="$d/systemd/rokh.service"
ssl="$d/litespeed/rokh-proxy.ssl.conf.example"
std="$d/litespeed/rokh-redirect.std.conf.example"

echo "== scripts"
for f in "$d"/scripts/*.sh "$d"/test/*.sh "$d"/local/postgres/*.sh; do
  check "bash -n $(basename "$f")" bash -n "$f"
done
for f in "$d"/scripts/*.sh "$d"/test/*.sh; do
  [ "$(basename "$f")" = lib.sh ] && continue
  check "$(basename "$f") is executable in git" sh -c "git -C '$repo' ls-files -s '$f' | grep -q '^100755'"
done
if command -v shellcheck > /dev/null; then
  check "shellcheck (warnings and above)" shellcheck -x -S warning -P "$d/scripts" "$d"/scripts/*.sh "$d"/test/*.sh
else
  echo "SKIP  shellcheck not installed"
fi

echo "== one address, one port"
addr="$(val "$env_prod" HTTP_ADDR)"
check "HTTP_ADDR is 127.0.0.1:18080" test "$addr" = 127.0.0.1:18080
check "LiteSpeed proxies to HTTP_ADDR" test "$(grep -Eo 'http://[0-9.:]+/' "$ssl" | sort -u)" = "http://$addr/"
check "LiteSpeed ProxyPass and ProxyPassReverse both present" test "$(grep -Ec '^ProxyPass(Reverse)? "/" "http://'"$addr"'/"' "$ssl")" = 2
check "LiteSpeed excludes /.well-known/ before proxying /" \
  test "$(grep -n 'ProxyPass "/.well-known/" "!"' "$ssl" | cut -d: -f1)" -lt "$(grep -n '^ProxyPass "/" ' "$ssl" | cut -d: -f1)"
check "LiteSpeed preserves the Host header" grep -q '^ProxyPreserveHost On' "$ssl"
check "LiteSpeed include sets no security headers" sh -c "! grep -Eiq '^[[:space:]]*Header .*(Content-Security|Strict-Transport|X-Frame|X-Content-Type)' '$ssl'"
check "plain HTTP is redirected, never proxied" sh -c "grep -q 'R=301' '$std' && ! grep -q ProxyPass '$std'"
check "plain HTTP keeps /.well-known/" grep -q '!\^/\\.well-known/' "$std"
check "Caddy fallback proxies to HTTP_ADDR" grep -q "reverse_proxy $addr\$" "$d/caddy/Caddyfile"
pg_port="$(val "$env_cmp" ROKH_PG_PORT)"
check "compose has no default PostgreSQL port" sh -c "grep -q 'ROKH_PG_PORT:?' '$compose' && ! grep -q 'ROKH_PG_PORT:-' '$compose'"
check "compose has no default PostgreSQL image" sh -c "grep -q 'ROKH_PG_IMAGE:?' '$compose' && ! grep -q 'ROKH_PG_IMAGE:-' '$compose'"
check "compose env template sets a numeric port" sh -c "echo '$pg_port' | grep -Eq '^[1-9][0-9]{3,4}\$'"
check "compose env template pins postgres:16-bookworm by digest" grep -Eq '^ROKH_PG_IMAGE=postgres:16-bookworm@sha256:CHANGE_ME$' "$env_cmp"
check "ops template points preflight at /opt/rokh/postgres/.env" test "$(val "$env_ops" ROKH_COMPOSE_ENV_FILE)" = /opt/rokh/postgres/.env
check "compose publishes PostgreSQL on 127.0.0.1 only" sh -c "grep -A2 '^    ports:' '$compose' | grep -E '^ +- ' | grep -vq '\"127\.0\.0\.1:'; [ \$? -ne 0 ]"
check "DATABASE_URL uses the compose port $pg_port" sh -c "val() { grep -E \"^\$2=\" \"\$1\" | cut -d= -f2-; }; val '$env_prod' DATABASE_URL | grep -q '@127.0.0.1:$pg_port/rokhtiacafe?'"
check "MIGRATION_DATABASE_URL uses the same server and database" \
  test "$(val "$env_mig" MIGRATION_DATABASE_URL | sed -E 's#^[^@]*@##')" = "$(val "$env_prod" DATABASE_URL | sed -E 's#^[^@]*@##')"
check "runtime role is rokh_app" sh -c "grep -q '^DATABASE_URL=postgres://rokh_app:' '$env_prod'"
check "migration role is rokh_owner" sh -c "grep -q '^MIGRATION_DATABASE_URL=postgres://rokh_owner:' '$env_mig'"
check "no owner credentials in the runtime template" sh -c "! grep -q MIGRATION_DATABASE_URL= '$env_prod'"
check "APP_BASE_URL is https://rokh.tiacafe.ir" test "$(val "$env_prod" APP_BASE_URL)" = https://rokh.tiacafe.ir
check "UPLOAD_DIR is absolute" sh -c "val() { grep -E \"^\$2=\" \"\$1\" | cut -d= -f2-; }; val '$env_prod' UPLOAD_DIR | grep -q '^/'"
check "BACKUP_DIR is on /backup" test "$(val "$env_ops" BACKUP_DIR)" = /backup/rokh
check "BACKUP_REQUIRE_MOUNT is /backup" test "$(val "$env_ops" BACKUP_REQUIRE_MOUNT)" = /backup
check "BACKUP_MIN_FREE_MB keeps the documented /backup reserve (>= 84480)" test "$(val "$env_ops" BACKUP_MIN_FREE_MB)" -ge 84480

echo "== database bootstrap"
check "init script passes no password on the command line" sh -c "! grep -Eq -- '-v +[a-z_]*=' '$init'"
check "init script reads the values with \\getenv" sh -c "grep -q 'getenv owner_password ROKH_OWNER_PASSWORD' '$init' && grep -q 'getenv app_password ROKH_APP_PASSWORD' '$init' && grep -q 'getenv db_name ROKH_DB_NAME' '$init'"

echo "== backup scheduling"
readme="$d/README.md"
check "no cron line pipes a backup script" sh -c "! grep -Eq '^[0-9*][0-9*,/ -]* .*backup[a-z-]*\.sh.*\|' '$readme'"
check "cron example runs backup-cron.sh" grep -Eq '^30 3 \* \* \* /opt/rokh/scripts/backup-cron\.sh$' "$readme"
check "cron example runs backup-status.sh" grep -Eq '^0  9 \* \* \* /opt/rokh/scripts/backup-status\.sh$' "$readme"
check "cron example sets MAILTO without inline comment or spaces" grep -Eq '^MAILTO=[^[:space:]#]+$' "$readme"
check "cron MAILTO line has no trailing comment" sh -c "! grep -E '^MAILTO=' '$readme' | grep -Eq '[[:space:]]#|#'"
check "monitoring section names both scripts" sh -c "sed -n '/^## 11\./,/^## 12\./p' '$readme' | grep -q backup-cron.sh && sed -n '/^## 11\./,/^## 12\./p' '$readme' | grep -q backup-status.sh"
check "backup.sh is not scheduled directly anywhere" sh -c "! grep -rEq '^[0-9*][0-9*,/ -]* .*/backup\.sh' '$d' '$repo/docs'"

echo "== systemd unit matches the layout"
check "unit runs as a dedicated non-root user" sh -c "grep -q '^User=rokh$' '$unit' && ! grep -q '^User=root' '$unit'"
check "unit ExecStart is ROKH_BIN" grep -q "^ExecStart=$(val "$env_ops" ROKH_BIN) serve\$" "$unit"
check "unit EnvironmentFile is ROKH_ENV_FILE" grep -q "^EnvironmentFile=$(val "$env_ops" ROKH_ENV_FILE)\$" "$unit"
check "unit can write UPLOAD_DIR only" test "$(grep '^ReadWritePaths=' "$unit" | cut -d= -f2)" = "$(val "$env_prod" UPLOAD_DIR)"
check "unit has no host PostgreSQL dependency" sh -c "! grep -Eq '^(After|Requires|Wants)=.*postgresql' '$unit'"
check "unit stop timeout exceeds SHUTDOWN_TIMEOUT" test "$(grep '^TimeoutStopSec=' "$unit" | cut -d= -f2)" -gt "$(val "$env_prod" SHUTDOWN_TIMEOUT | tr -d s)"
for k in NoNewPrivileges ProtectSystem ProtectHome PrivateTmp CapabilityBoundingSet; do
  check "unit sets $k" grep -q "^$k=" "$unit"
done
# systemd-analyze verify also loads the host's own units and reports their problems (e.g. a CI
# runner image whose units use removed options). Only a diagnostic that names another unit file in
# a system unit directory, as "/dir/name.unit:LINE: ...", is foreign; every other line, including
# any line about rokh.service or without a file prefix, counts against the Rokh unit.
foreign_re='^/(usr/lib|lib|etc|run)/systemd/system/[^/:]+:[0-9]+: '
foreign_unit_diag() { # foreign_unit_diag LINE OWN_UNIT_PATH
  [[ $1 =~ $foreign_re ]] && [[ $1 != "$2"* ]]
}
not_foreign() { ! foreign_unit_diag "$@"; }
own="/tmp/x/rokh.service"
check "verify filter: a runner unit's diagnostic is foreign" \
  foreign_unit_diag "/usr/lib/systemd/system/walinuxagent.service:21: Support for option CPUAccounting= has been removed and it is ignored" "$own"
check "verify filter: a runner slice's diagnostic is foreign" \
  foreign_unit_diag "/usr/lib/systemd/system/system-xfs_scrub.slice:15: Support for option CPUAccounting= has been removed and it is ignored" "$own"
check "verify filter: a Rokh unit diagnostic is never foreign" not_foreign "$own:12: Unknown key name 'Foo' in section 'Service', ignoring." "$own"
check "verify filter: an unprefixed diagnostic is never foreign" not_foreign "rokh.service: Command /bin/true is not executable: No such file" "$own"
check "verify filter: a unit outside the system unit directories is never foreign" not_foreign "/home/x/evil.service:1: Unknown section" "$own"
if command -v systemd-analyze > /dev/null && [ -d /run/systemd/system ]; then
  tmpu="$(mktemp -d)"
  sed -e 's#^ExecStart=.*#ExecStart=/bin/true#' -e '/^EnvironmentFile=/d' -e 's#^User=rokh#User=daemon#' \
    -e 's#^Group=rokh#Group=daemon#' -e 's#^WorkingDirectory=.*#WorkingDirectory=/#' "$unit" > "$tmpu/rokh.service"
  out="$(systemd-analyze verify "$tmpu/rokh.service" 2>&1)"
  vrc=$?
  own_diag="" foreign_diag=""
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    if foreign_unit_diag "$line" "$tmpu/rokh.service"; then foreign_diag+="$line"$'\n'; else own_diag+="$line"$'\n'; fi
  done <<< "$out"
  if [ -n "$own_diag" ]; then
    bad "systemd-analyze verify: $own_diag"
  elif [ "$vrc" -ne 0 ] && [ -z "$foreign_diag" ]; then
    bad "systemd-analyze verify exited $vrc without output"
  else
    ok "systemd-analyze verify (paths substituted)"
  fi
  [ -z "$foreign_diag" ] || printf 'INFO  systemd-analyze reported other units on this host (not Rokh):\n%s' "$foreign_diag"
  rm -f "$tmpu/rokh.service" && rmdir "$tmpu"
else
  echo "SKIP  systemd-analyze not available"
fi

echo "== no secrets in templates"
check "env templates hold placeholders only" sh -c "! grep -hE '^[A-Z_]*(PASSWORD|SECRET|PRIVATE)[A-Z_]*=.+' '$env_prod' '$env_mig' '$env_ops' '$env_pg' | grep -vE '=(CHANGE_ME)?$'"
check "database URLs use CHANGE_ME passwords" sh -c "! grep -hEo 'postgres://[^:]+:[^@]*@' '$env_prod' '$env_mig' | grep -v ':CHANGE_ME@'"
check "APP_SECRET_KEY and VAPID_PRIVATE_KEY are empty" sh -c "grep -qx 'APP_SECRET_KEY=' '$env_prod' && grep -qx 'VAPID_PRIVATE_KEY=' '$env_prod'"

echo "== compose"
if docker compose version > /dev/null 2>&1; then
  tmpe="$(mktemp)"
  tmpc="$(mktemp)"
  cp "$env_pg" "$tmpe"
  sed "s#@sha256:CHANGE_ME#@sha256:$(printf '%064d' 0)#" "$env_cmp" > "$tmpc"
  check "docker compose config is valid" env -u ROKH_PG_PORT -u ROKH_PG_IMAGE ROKH_PG_ENV_FILE="$tmpe" \
    docker compose --env-file "$tmpc" -f "$compose" config --quiet
  check "docker compose refuses to run without ROKH_PG_PORT" sh -c "! env -u ROKH_PG_PORT ROKH_PG_IMAGE=postgres:16-bookworm ROKH_PG_ENV_FILE='$tmpe' docker compose -f '$compose' config --quiet > /dev/null 2>&1"
  check "docker compose refuses to run without ROKH_PG_IMAGE" sh -c "! env -u ROKH_PG_IMAGE ROKH_PG_PORT=15433 ROKH_PG_ENV_FILE='$tmpe' docker compose -f '$compose' config --quiet > /dev/null 2>&1"
  rm -f "$tmpe" "$tmpc"
else
  echo "SKIP  docker compose not available"
fi
check "compose uses PostgreSQL 16" grep -q '^ROKH_PG_IMAGE=postgres:16' "$env_cmp"
check "compose has a health check" grep -q 'healthcheck:' "$compose"
check "compose restarts the container" grep -q 'restart: unless-stopped' "$compose"
check "compose caps container logs" grep -q 'max-size:' "$compose"

echo
if [ "$fails" -eq 0 ]; then echo "template checks ok"; else echo "$fails template check(s) failed"; exit 1; fi
