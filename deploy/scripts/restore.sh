#!/usr/bin/env bash
# Restore a backup set into a NEW, EMPTY database (deploy/README.md §9). Never touches the live
# database or the live uploads directory. Run as root:
#   RESTORE_DB=rokhtiacafe_restore_20261020 \
#   RESTORE_UPLOADS_DIR=/var/lib/rokh/uploads.restore-20261020 \
#     /opt/rokh/scripts/restore.sh /backup/rokh/rokh-20261020-033000
#
# RESTORE_DB           required; created beforehand with deploy/scripts/restore-target.sql
# RESTORE_UPLOADS_DIR  optional; must not exist yet (it is created). Unset = database only.
#
# Order: verify the set (checksums, dump, archive) → verify the target (name, identity, owner,
# empty) → pg_restore in one transaction → migrate up → migrate status (0 pending) → rokh check
# → unpack uploads. Anything unexpected stops the run before the next step.
# Exit codes: 0 ok · 1 refused (bad input or unsafe target; nothing changed) · 2 restore failed.
set -euo pipefail
umask 077
. "$(dirname "$0")/lib.sh"

EXIT_FAIL=1
[ $# -eq 1 ] || die "usage: RESTORE_DB=name [RESTORE_UPLOADS_DIR=dir] restore.sh <backup set directory>"
set_dir="${1%/}"
target="${RESTORE_DB:-}"
uploads_dest="${RESTORE_UPLOADS_DIR:-}"
load_ops

scratch="$(mktemp -d)"
cleanup() {
  rm -f -- "$scratch/list" "$scratch/names" "$scratch/types"
  rmdir -- "$scratch" 2> /dev/null || true
}
trap cleanup EXIT

# --- 1. The backup set ----------------------------------------------------------------------------
[ -d "$set_dir" ] || die "backup set not found: $set_dir"
[[ $(basename -- "$set_dir") =~ ^rokh-[0-9]{8}-[0-9]{6}$ ]] ||
  die "not a backup set directory (expected rokh-YYYYmmdd-HHMMSS): $set_dir"
[ -f "$set_dir/SHA256SUMS" ] || die "missing $set_dir/SHA256SUMS"
grep -Evq '^[0-9a-f]{64}  (db\.dump|uploads\.tar\.gz|INFO)$' "$set_dir/SHA256SUMS" &&
  die "SHA256SUMS has unexpected lines"
grep -q '  db\.dump$' "$set_dir/SHA256SUMS" || die "SHA256SUMS does not cover db.dump"
(cd "$set_dir" && sha256sum --quiet --strict -c SHA256SUMS) || die "checksum mismatch in $set_dir: the set is damaged"

pg_offline pg_restore --list < "$set_dir/db.dump" > "$scratch/list" 2> /dev/null ||
  die "db.dump is not a readable pg_dump archive"
grep -Eq '[0-9]+ TABLE rokh orders ' "$scratch/list" || die "db.dump does not contain the Rokh schema"

archive=""
if [ -f "$set_dir/uploads.tar.gz" ] && [ -n "$uploads_dest" ]; then
  archive="$set_dir/uploads.tar.gz"
  grep -q '  uploads\.tar\.gz$' "$set_dir/SHA256SUMS" || die "SHA256SUMS does not cover uploads.tar.gz"
  # Only plain files and directories with safe relative names: no absolute paths, no "..",
  # no symlinks, hard links or device files.
  tar -tzf "$archive" > "$scratch/names" || die "uploads.tar.gz is not a readable archive"
  tar -tvzf "$archive" | cut -c1 > "$scratch/types" || die "uploads.tar.gz is not a readable archive"
  grep -vq '^[-d]$' "$scratch/types" && die "uploads.tar.gz contains links or special files"
  while IFS= read -r entry; do
    [ "$entry" = ./ ] && continue
    [[ $entry =~ ^\./([A-Za-z0-9._-]+/)*[A-Za-z0-9._-]+/?$ ]] || die "uploads.tar.gz has an unsafe entry: $entry"
    [[ /$entry/ != */../* ]] || die "uploads.tar.gz has an unsafe entry: $entry"
  done < "$scratch/names"
fi

# --- 2. The target ------------------------------------------------------------------------------
[ -n "$target" ] || die "set RESTORE_DB to the new, empty database (create it with restore-target.sql)"
valid_db_name "$target" || die "RESTORE_DB is not a valid database name: $target"
case $target in *_restore_*) ;; *) die "RESTORE_DB must contain _restore_ (e.g. rokhtiacafe_restore_20261020)" ;; esac

owner_url="$(env_get "$ROKH_MIGRATE_ENV_FILE" MIGRATION_DATABASE_URL)"
app_url="$(env_get "$ROKH_ENV_FILE" DATABASE_URL)"
[ -n "$owner_url" ] || die "MIGRATION_DATABASE_URL missing in $ROKH_MIGRATE_ENV_FILE"
[ -n "$app_url" ] || die "DATABASE_URL missing in $ROKH_ENV_FILE"
parse_pg_url "$owner_url" OWN "$ROKH_MIGRATE_ENV_FILE"
parse_pg_url "$app_url" APP "$ROKH_ENV_FILE"
[ "$OWN_HOST:$OWN_PORT" = "$APP_HOST:$APP_PORT" ] ||
  die "migrate.env and rokh.env point at different servers ($OWN_HOST:$OWN_PORT vs $APP_HOST:$APP_PORT)"
[[ $OWN_USER =~ ^[a-z_][a-z0-9_]*$ && $APP_USER =~ ^[a-z_][a-z0-9_]*$ ]] || die "unexpected database role names"
[ "$OWN_USER" != "$APP_USER" ] || die "migrate.env and rokh.env use the same role ($APP_USER)"
for live in "$OWN_DB" "$APP_DB" postgres template0 template1; do
  [ "$target" != "$live" ] || die "refusing to restore over database '$live'"
done

live_uploads="$(env_get "$ROKH_ENV_FILE" UPLOAD_DIR)"
if [ -n "$uploads_dest" ]; then
  [[ $uploads_dest =~ ^/[A-Za-z0-9._/-]+$ ]] || die "RESTORE_UPLOADS_DIR must be an absolute path of [A-Za-z0-9._/-]"
  uploads_dest="${uploads_dest%/}"
  uploads_partial="$uploads_dest.partial"
  [ "$uploads_dest" != "${live_uploads%/}" ] || die "RESTORE_UPLOADS_DIR is the live UPLOAD_DIR"
  [ ! -e "$uploads_dest" ] && [ ! -L "$uploads_dest" ] ||
    die "RESTORE_UPLOADS_DIR already exists: $uploads_dest (choose a new path; existing files are never replaced)"
  [ ! -e "$uploads_partial" ] && [ ! -L "$uploads_partial" ] ||
    die "$uploads_partial exists (left by a failed restore): inspect it, see deploy/README.md §9.4"
  [ -d "$(dirname -- "$uploads_dest")" ] || die "parent of RESTORE_UPLOADS_DIR does not exist"
  [ -f "$set_dir/uploads.tar.gz" ] || die "RESTORE_UPLOADS_DIR is set but the set has no uploads.tar.gz"
fi

q() { # q SQL — one value from the target, as the schema owner
  pg_exec "$OWN_USER" "$OWN_PASS" "$target" "$OWN_HOST" "$OWN_PORT" "$OWN_SSLMODE" -- \
    psql --no-password -XAt -v ON_ERROR_STOP=1 -c "$1"
}
identity="$(q "SELECT current_database() || '|' || pg_get_userbyid(datdba) FROM pg_database WHERE datname = current_database()" 2> /dev/null)" ||
  die "cannot connect to database '$target' as $OWN_USER: create it first with deploy/scripts/restore-target.sql"
[ "$identity" = "$target|$OWN_USER" ] ||
  die "target identity mismatch: expected database $target owned by $OWN_USER, got '$identity'"
objects="$(q "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
              WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_%'")"
schemas="$(q "SELECT count(*) FROM pg_namespace
              WHERE nspname NOT IN ('public', 'information_schema') AND nspname NOT LIKE 'pg\_%'")"
[ "$objects" = 0 ] && [ "$schemas" = 0 ] ||
  die "target database $target is not empty ($schemas extra schemas, $objects relations): use a newly created one"
app_connect="$(q "SELECT has_database_privilege('$APP_USER', current_database(), 'CONNECT')")"
[ "$app_connect" = t ] || die "$APP_USER cannot connect to $target: run restore-target.sql to prepare it"

# --- 3. Restore and verify ------------------------------------------------------------------------
# The target carries its own status as a database comment, so a partly restored database can never
# be mistaken for a validated recovery: IN PROGRESS → VALIDATED, or FAILED (set on any exit before
# validation). Only VALIDATED databases may be switched to (deploy/README.md §9.3).
EXIT_FAIL=2
set_name="$(basename -- "$set_dir")"
mark() { # mark TEXT — set the target's status comment (TEXT is built from validated values only)
  q "COMMENT ON DATABASE $target IS 'rokh restore $1 (set $set_name, $(date -u +%Y-%m-%dT%H:%M:%SZ))'" > /dev/null
}
step="start"
validated=no
on_exit() {
  local code=$?
  cleanup
  if [ "$validated" != yes ] && [ "$step" != start ]; then
    mark "FAILED at $step: NOT a validated recovery, drop it (deploy/README.md 9.4)" 2> /dev/null ||
      echo "warning: could not mark $target as FAILED" >&2
    echo "RESTORE FAILED at step '$step': database $target is NOT a validated recovery." >&2
    [ -z "${uploads_partial:-}" ] || [ ! -e "$uploads_partial" ] ||
      echo "Partial uploads were left in $uploads_partial (never used; see deploy/README.md §9.4)." >&2
  fi
  exit "$code"
}
trap on_exit EXIT

mark "IN PROGRESS: NOT a validated recovery" || die "cannot mark $target (comment as $OWN_USER)"
step="pg_restore"
log "restore $set_name into $target ($PG_CLIENT client)"
pg_exec "$OWN_USER" "$OWN_PASS" "$target" "$OWN_HOST" "$OWN_PORT" "$OWN_SSLMODE" -- \
  pg_restore --no-password --no-owner --role="$OWN_USER" --exit-on-error --single-transaction \
  --dbname="$target" < "$set_dir/db.dump" ||
  die "pg_restore failed; the transaction was rolled back, so $target holds no restored data"

target_owner_url="$(url_with_db "$owner_url" "$target")"
target_app_url="$(url_with_db "$app_url" "$target")"
step="migrate up"
MIGRATION_DATABASE_URL="$target_owner_url" rokh_cmd migrate up || die "rokh migrate up failed on $target"
step="migrate status"
status="$(MIGRATION_DATABASE_URL="$target_owner_url" rokh_cmd migrate status)" || die "rokh migrate status failed"
grep -q ', 0 pending$' <<< "$status" || die "migrations still pending on $target"
step="rokh check"
DATABASE_URL="$target_app_url" rokh_cmd check || die "rokh check found invariant violations in $target"

step="row counts"
log "row counts in $target:"
q "SELECT format('  %-22s %s', table_name, (xpath('/row/c/text()',
     query_to_xml(format('SELECT count(*) AS c FROM rokh.%I', table_name), false, true, '')))[1])
   FROM information_schema.tables WHERE table_schema = 'rokh' AND table_type = 'BASE TABLE' ORDER BY table_name" ||
  die "could not count rows in $target"

uploads_note="database only"
if [ -n "$archive" ]; then
  # Unpack beside the destination and rename only when complete: a visible RESTORE_UPLOADS_DIR is
  # always a full copy. A failure leaves the .partial directory for inspection; nothing is deleted.
  step="uploads"
  mkdir -m 750 -- "$uploads_partial" || die "cannot create $uploads_partial"
  tar -xzf "$archive" -C "$uploads_partial" --no-same-owner --no-same-permissions --no-overwrite-dir ||
    die "unpacking uploads failed"
  if [ -n "$ROKH_RUN_AS" ] && [ "$(id -u)" = 0 ]; then
    chown -R -- "$ROKH_RUN_AS:" "$uploads_partial" || die "cannot chown the restored uploads"
  fi
  mv -T -- "$uploads_partial" "$uploads_dest" || die "cannot move $uploads_partial into place"
  log "uploads unpacked into $uploads_dest"
  uploads_note="uploads in $uploads_dest"
elif [ -f "$set_dir/uploads.tar.gz" ]; then
  log "uploads not restored (set RESTORE_UPLOADS_DIR to unpack them)"
fi

step="mark validated"
mark "VALIDATED: 0 pending migrations, rokh check ok, $uploads_note" || die "cannot mark $target as validated"
validated=yes
log "RESTORE VALIDATED into database $target. To switch over, follow deploy/README.md §9.3 (owner approval)."
