#!/usr/bin/env bash
# Daily backup (deploy/README.md §8). Run from ~/rokh as the app user:
#   ./scripts/backup.sh
# Writes backups/rokh-<stamp>.dump (pg_dump custom format, as the schema owner) and
# backups/uploads-<stamp>.tar.gz; keeps 14 days. Copy the files off the server, encrypted.
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
test -f .env.migrate || { echo "missing .env.migrate" >&2; exit 1; }
split_url "$(grep -E '^MIGRATION_DATABASE_URL=' .env.migrate | cut -d= -f2-)"
url="$CONN"
upload_dir="$(grep -E '^UPLOAD_DIR=' .env | cut -d= -f2-)"
stamp="$(date +%Y%m%d-%H%M)"
mkdir -p backups

pg_dump --format=custom --no-password --dbname="$url" --file="backups/rokh-$stamp.dump.partial"
mv "backups/rokh-$stamp.dump.partial" "backups/rokh-$stamp.dump"
if [ -n "$upload_dir" ] && [ -d "$upload_dir" ]; then
  tar -czf "backups/uploads-$stamp.tar.gz" -C "$upload_dir" .
fi

# A dump that cannot be listed is not a backup.
pg_restore --list "backups/rokh-$stamp.dump" > /dev/null

find backups -maxdepth 1 -type f \( -name 'rokh-*.dump' -o -name 'uploads-*.tar.gz' \) -mtime +14 -delete
echo "backup ok: backups/rokh-$stamp.dump ($(du -h "backups/rokh-$stamp.dump" | cut -f1))"
