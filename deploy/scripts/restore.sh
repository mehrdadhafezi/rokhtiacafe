#!/usr/bin/env bash
# Restore a backup into a NEW, EMPTY database (deploy/README.md §9). Never touches the live one.
#   RESTORE_DB=rokhtiacafe_restore ./scripts/restore.sh backups/rokh-<stamp>.dump [backups/uploads-<stamp>.tar.gz]
# The target database must exist, be empty and be owned by rokh_owner.
set -euo pipefail
umask 077

# Keep the password out of process arguments (visible to other users on shared hosts):
# libpq reads it from PGPASSWORD; the URL passed on the command line has none.
split_url() { # $1 = postgres URL → sets CONN (URL without password) and exports PGPASSWORD
  local pw
  pw="$(printf '%s' "$1" | sed -nE 's#^[^:]+://[^:@/]+:([^@]*)@.*#\1#p')"
  export PGPASSWORD="$(printf '%b' "${pw//%/\\x}")" # undo %XX URL encoding
  CONN="$(printf '%s' "$1" | sed -E 's#^([^:]+://[^:@/]+):[^@]*@#\1@#')"
}

cd "$(dirname "$0")/.."
dump="${1:?usage: RESTORE_DB=name scripts/restore.sh <dump> [uploads.tar.gz]}"
uploads="${2:-}"
target="${RESTORE_DB:?set RESTORE_DB to the new, empty database name}"

owner_url="$(grep -E '^MIGRATION_DATABASE_URL=' .env.migrate | cut -d= -f2-)"
live_db="$(printf '%s' "$owner_url" | sed -E 's#^[^/]*//[^/]*/([^?]*).*#\1#')"
if [ "$target" = "$live_db" ]; then
  echo "refusing to restore over the live database ($live_db)" >&2
  exit 1
fi
target_url="$(printf '%s' "$owner_url" | sed -E "s#^([^/]*//[^/]*/)[^?]*#\1$target#")"
app_url="$(grep -E '^DATABASE_URL=' .env | cut -d= -f2- | sed -E "s#^([^/]*//[^/]*/)[^?]*#\1$target#")"

split_url "$target_url"
target_conn="$CONN"
tables="$(psql --no-password --dbname="$target_conn" -Atc "SELECT count(*) FROM pg_tables WHERE schemaname = 'rokh'")"
if [ "$tables" != "0" ]; then
  echo "target database $target is not empty" >&2
  exit 1
fi

pg_restore --no-password --no-owner --role=rokh_owner --exit-on-error --dbname="$target_conn" "$dump"
MIGRATION_DATABASE_URL="$target_url" bin/rokh migrate up
DATABASE_URL="$app_url" bin/rokh check

if [ -n "$uploads" ]; then
  rm -rf uploads.restored && mkdir -p uploads.restored
  tar -xzf "$uploads" -C uploads.restored
  echo "uploads unpacked into uploads.restored/"
fi
echo "restore ok into database $target — see deploy/README.md §9 to switch over"
