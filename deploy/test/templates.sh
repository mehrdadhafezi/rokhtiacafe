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
pg_port="$(grep -Eo '127\.0\.0\.1:\$\{ROKH_PG_PORT:-[0-9]+\}:5432' "$compose" | grep -Eo ':-[0-9]+' | tr -d ':-')"
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
if command -v systemd-analyze > /dev/null && [ -d /run/systemd/system ]; then
  tmpu="$(mktemp -d)"
  sed -e 's#^ExecStart=.*#ExecStart=/bin/true#' -e '/^EnvironmentFile=/d' -e 's#^User=rokh#User=daemon#' \
    -e 's#^Group=rokh#Group=daemon#' -e 's#^WorkingDirectory=.*#WorkingDirectory=/#' "$unit" > "$tmpu/rokh.service"
  out="$(systemd-analyze verify "$tmpu/rokh.service" 2>&1)"
  if [ -z "$out" ]; then ok "systemd-analyze verify (paths substituted)"; else bad "systemd-analyze verify: $out"; fi
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
  cp "$env_pg" "$tmpe"
  check "docker compose config is valid" env ROKH_PG_ENV_FILE="$tmpe" docker compose -f "$compose" config --quiet
  rm -f "$tmpe"
else
  echo "SKIP  docker compose not available"
fi
check "compose uses PostgreSQL 16" grep -q 'postgres:16' "$compose"
check "compose has a health check" grep -q 'healthcheck:' "$compose"
check "compose restarts the container" grep -q 'restart: unless-stopped' "$compose"
check "compose caps container logs" grep -q 'max-size:' "$compose"

echo
if [ "$fails" -eq 0 ]; then echo "template checks ok"; else echo "$fails template check(s) failed"; exit 1; fi
