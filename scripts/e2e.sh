#!/usr/bin/env bash
# Browser acceptance tests (Playwright, frontend/e2e) against an ISOLATED backend:
#   TEST_DATABASE_ADMIN_URL=postgres://postgres:…@127.0.0.1:…/postgres \
#   MIGRATION_DATABASE_URL=postgres://rokh_owner:…@127.0.0.1:…/rokhtiacafe \
#   DATABASE_URL=postgres://rokh_app:…@127.0.0.1:…/rokhtiacafe  scripts/e2e.sh [playwright args]
# The URLs are those of a throwaway cluster prepared with deploy/local/postgres/roles.sql (the same
# variables as the backend integration tests); only loopback servers are accepted. A new database
# is created for the run and dropped afterwards. ROKH_BIN may name a binary with the embedded
# frontend (CI: the release build); otherwise one is built from this checkout. Staff passwords are
# random per run and never printed.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
: "${TEST_DATABASE_ADMIN_URL:?}" "${MIGRATION_DATABASE_URL:?}" "${DATABASE_URL:?}"
port="${E2E_PORT:-18090}"
tmp="$(mktemp -d)"
command -v cygpath > /dev/null && tmp="$(cygpath -m "$tmp")" # Go on Windows needs a native path
exe=""
case "$(uname -s)" in MINGW* | MSYS* | CYGWIN*) exe=.exe ;; esac
rand() { od -An -N12 -tx1 /dev/urandom | tr -d ' \n'; }

echo "==> tools"
(cd "$root/backend" && go build -o "$tmp/c6$exe" ./tools/c6)
if [ -z "${ROKH_BIN:-}" ]; then
  ui="$root/backend/web/ui"
  if [ ! -f "$ui/index.html" ]; then
    (cd "$root/frontend" && npm run build > /dev/null)
    find "$ui" -mindepth 1 ! -name .gitkeep -exec rm -rf {} +
    cp -R "$root/frontend/dist/." "$ui/"
    rm -rf "$ui/.vite"
  fi
  (cd "$root/backend" && go build -o "$tmp/rokh$exe" ./cmd/rokh)
  ROKH_BIN="$tmp/rokh$exe"
fi

created="$("$tmp/c6$exe" createdb -admin "$TEST_DATABASE_ADMIN_URL" -owner "$MIGRATION_DATABASE_URL" -app "$DATABASE_URL" -prefix rokh_e2e)"
eval "$created"
pid=""
cleanup() {
  [ -n "$pid" ] && kill "$pid" 2> /dev/null && wait "$pid" 2> /dev/null
  "$tmp/c6$exe" dropdb -admin "$TEST_DATABASE_ADMIN_URL" -name "$DB_NAME" -prefix rokh_e2e || true
  rm -rf "$tmp"
}
trap cleanup EXIT
"$tmp/c6$exe" whoami -db "$OWNER_URL"

echo "==> database $DB_NAME: migrate, staff users"
MIGRATION_DATABASE_URL="$OWNER_URL" "$ROKH_BIN" migrate up > /dev/null
export E2E_ADMIN_USER=e2eadmin E2E_OPERATOR_USER=e2eoperator
E2E_ADMIN_PASSWORD="e2e-$(rand)"
E2E_OPERATOR_PASSWORD="e2e-$(rand)"
export E2E_ADMIN_PASSWORD E2E_OPERATOR_PASSWORD
printf '%s\n' "$E2E_ADMIN_PASSWORD" | DATABASE_URL="$APP_URL" "$ROKH_BIN" user create -username "$E2E_ADMIN_USER" -display-name "E2E Admin" -role ADMIN > /dev/null
printf '%s\n' "$E2E_OPERATOR_PASSWORD" | DATABASE_URL="$APP_URL" "$ROKH_BIN" user create -username "$E2E_OPERATOR_USER" -display-name "E2E Operator" -role OPERATOR > /dev/null

echo "==> server on 127.0.0.1:$port"
mkdir -p "$tmp/uploads"
env APP_ENV=test HTTP_ADDR="127.0.0.1:$port" APP_BASE_URL="http://127.0.0.1:$port" DATABASE_URL="$APP_URL" \
  APP_TIMEZONE=Asia/Tehran COOKIE_SECURE=false ORDERING_HOURS=00:00-24:00 ORDER_PENDING_TTL=24h \
  UPLOAD_DIR="$tmp/uploads" APP_SECRET_KEY="$("$ROKH_BIN" secret gen)" LOG_LEVEL=warn \
  "$ROKH_BIN" serve > "$tmp/serve.log" 2>&1 &
pid=$!
for _ in $(seq 60); do
  curl -fsS "http://127.0.0.1:$port/readyz" > /dev/null 2>&1 && break
  sleep 0.5
done
curl -fsS "http://127.0.0.1:$port/readyz" > /dev/null || { cat "$tmp/serve.log"; exit 1; }

echo "==> playwright"
export E2E_BASE_URL="http://127.0.0.1:$port"
set +e
(cd "$root/frontend" && npx playwright test "$@")
rc=$?
set -e
if grep -qi 'level=ERROR' "$tmp/serve.log"; then
  echo "server errors during the run:"
  grep -i 'level=ERROR' "$tmp/serve.log" | head -n 20
  rc=1
fi
exit "$rc"
