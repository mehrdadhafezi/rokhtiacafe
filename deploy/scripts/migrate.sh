#!/usr/bin/env bash
# Run `rokh migrate up|status` as the schema owner (deploy/README.md §4, §7). Run as root:
#   /opt/rokh/scripts/migrate.sh status
#   /opt/rokh/scripts/migrate.sh up
# MIGRATION_DATABASE_URL is read from ROKH_MIGRATE_ENV_FILE and handed to rokh through the
# environment only (never on the command line). The binary itself runs as ROKH_RUN_AS.
set -euo pipefail
umask 077
. "$(dirname "$0")/lib.sh"

[ $# -eq 1 ] && { [ "$1" = up ] || [ "$1" = status ]; } || die "usage: migrate.sh up|status"
load_ops
url="$(env_get "$ROKH_MIGRATE_ENV_FILE" MIGRATION_DATABASE_URL)"
[ -n "$url" ] || die "MIGRATION_DATABASE_URL missing in $ROKH_MIGRATE_ENV_FILE"
MIGRATION_DATABASE_URL="$url" rokh_cmd migrate "$1"
