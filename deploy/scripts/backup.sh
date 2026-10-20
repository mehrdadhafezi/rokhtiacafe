#!/usr/bin/env bash
# Backup (deploy/README.md §8). Run as root, daily from root's crontab:
#   /opt/rokh/scripts/backup.sh
#
# Writes one verified set per run to BACKUP_DIR (the separately mounted backup disk):
#   rokh-YYYYmmdd-HHMMSS/db.dump          pg_dump custom format, taken as the schema owner
#   rokh-YYYYmmdd-HHMMSS/uploads.tar.gz   product images (UPLOAD_DIR)
#   rokh-YYYYmmdd-HHMMSS/INFO             versions and sizes, no secrets
#   rokh-YYYYmmdd-HHMMSS/SHA256SUMS       checksums of the three files above
# The set is assembled in a hidden .partial directory and renamed into place only after every
# check passed, so a visible rokh-* directory is always complete.
#
# Exit codes: 0 ok · 1 refused (configuration or preconditions; nothing written)
#             2 backup failed (nothing published) · 3 backup ok, but retention needs attention
# Settings: deploy/env/ops.env.example. Copy every set off the server (deploy/README.md §8.4).
set -euo pipefail
umask 077
. "$(dirname "$0")/lib.sh"

EXIT_FAIL=1
load_ops

backup_dir="$(opt BACKUP_DIR "")"
require_mount="$(opt BACKUP_REQUIRE_MOUNT "")"
allow_root_fs="$(opt BACKUP_ALLOW_ROOT_FS no)"
min_free_mb="$(opt BACKUP_MIN_FREE_MB 2048)"
keep_days="$(opt BACKUP_KEEP_DAYS 14)"
keep_min="$(opt BACKUP_KEEP_MIN 7)"

# --- Preconditions: refuse rather than write somewhere unexpected -------------------------------
[ -n "$backup_dir" ] || die "BACKUP_DIR is not set (see deploy/env/ops.env.example)"
case $backup_dir in /*) ;; *) die "BACKUP_DIR must be an absolute path: $backup_dir" ;; esac
[ -d "$backup_dir" ] || die "BACKUP_DIR does not exist: $backup_dir (create it once on the backup disk; this script never creates it)"
[ -w "$backup_dir" ] || die "BACKUP_DIR is not writable: $backup_dir"
if [ -n "$(find "$backup_dir" -maxdepth 0 -perm -o+w)" ]; then
  die "BACKUP_DIR is world-writable: $backup_dir (chmod 700)"
fi
mnt="$(mount_of "$backup_dir")"
if [ -n "$require_mount" ] && [ "$mnt" != "$require_mount" ]; then
  die "BACKUP_DIR $backup_dir is on '$mnt', not on $require_mount: is the backup disk mounted?"
fi
if [ "$mnt" = / ] && [ "$allow_root_fs" != yes ]; then
  die "BACKUP_DIR $backup_dir is on the root filesystem; set BACKUP_REQUIRE_MOUNT to the backup disk"
fi
is_uint "$min_free_mb" || die "BACKUP_MIN_FREE_MB must be a whole number"
is_uint "$keep_days" && [ "$keep_days" -ge 1 ] || die "BACKUP_KEEP_DAYS must be a whole number >= 1"
is_uint "$keep_min" && [ "$keep_min" -ge 1 ] || die "BACKUP_KEEP_MIN must be a whole number >= 1"
free="$(free_mb "$backup_dir")"
[ "$free" -ge "$min_free_mb" ] || die "only ${free} MB free on $mnt (BACKUP_MIN_FREE_MB=$min_free_mb)"

owner_url="$(env_get "$ROKH_MIGRATE_ENV_FILE" MIGRATION_DATABASE_URL)"
[ -n "$owner_url" ] || die "MIGRATION_DATABASE_URL missing in $ROKH_MIGRATE_ENV_FILE"
parse_pg_url "$owner_url" DB "$ROKH_MIGRATE_ENV_FILE"
upload_dir="$(env_get "$ROKH_ENV_FILE" UPLOAD_DIR)"
case $upload_dir in /*) ;; *) die "UPLOAD_DIR in $ROKH_ENV_FILE must be an absolute path" ;; esac
[ -d "$upload_dir" ] || die "UPLOAD_DIR does not exist: $upload_dir"

exec 9> "$backup_dir/.rokh-backup.lock"
flock -n 9 || die "another backup is running (lock $backup_dir/.rokh-backup.lock)"

# --- Backup -------------------------------------------------------------------------------------
EXIT_FAIL=2
stamp="$(date +%Y%m%d-%H%M%S)"
name="rokh-$stamp"
final="$backup_dir/$name"
work="$backup_dir/.$name.partial"
[ ! -e "$final" ] && [ ! -e "$work" ] || die "$final already exists"
mkdir "$work"
published=no
cleanup() {
  if [ "$published" != yes ] && [ -d "$work" ]; then
    rm -f -- "$work/db.dump" "$work/db.list" "$work/uploads.tar.gz" "$work/INFO" "$work/SHA256SUMS"
    rmdir -- "$work" 2> /dev/null || log "warning: could not remove $work"
  fi
}
trap cleanup EXIT

log "backup $name: database $DB_DB as $DB_USER ($PG_CLIENT client)"
pg_exec "$DB_USER" "$DB_PASS" "$DB_DB" "$DB_HOST" "$DB_PORT" "$DB_SSLMODE" -- \
  pg_dump --format=custom --compress=6 --no-password > "$work/db.dump" ||
  die "pg_dump failed (is PostgreSQL running and MIGRATION_DATABASE_URL correct?)"

# A dump is only a backup if it can be read back and contains the schema.
pg_offline pg_restore --list < "$work/db.dump" > "$work/db.list" || die "dump is unreadable (pg_restore --list failed)"
grep -Eq '[0-9]+ TABLE rokh orders ' "$work/db.list" || die "dump does not contain table rokh.orders"
grep -Eq '[0-9]+ TABLE rokh goose_db_version ' "$work/db.list" || die "dump does not contain the migration table"
server_version="$(pg_exec "$DB_USER" "$DB_PASS" "$DB_DB" "$DB_HOST" "$DB_PORT" "$DB_SSLMODE" -- \
  psql --no-password -XAtc 'SHOW server_version')" || die "cannot query the server version"
dump_version="$(pg_offline pg_dump --version)"
rm -f -- "$work/db.list"

log "backup $name: uploads from $upload_dir"
tar -C "$upload_dir" --exclude='.upload-*' --exclude='.rokh-write-test-*' --numeric-owner \
  -czf "$work/uploads.tar.gz" . || die "archiving $upload_dir failed"
upload_files="$(tar -tzf "$work/uploads.tar.gz" | grep -vc '/$')" || upload_files=0

{
  echo "set:            $name"
  echo "created:        $(date -Is)"
  echo "host:           $(hostname)"
  echo "database:       $DB_DB"
  echo "server:         PostgreSQL $server_version"
  echo "pg_dump:        $dump_version"
  echo "rokh:           $(rokh_cmd version 2> /dev/null || echo unknown)"
  echo "db.dump:        $(stat -c %s "$work/db.dump") bytes"
  echo "uploads files:  $upload_files"
} > "$work/INFO"

(cd "$work" && sha256sum db.dump uploads.tar.gz INFO > SHA256SUMS && sha256sum --quiet --strict -c SHA256SUMS) ||
  die "checksum verification failed"
sync
mv -T -- "$work" "$final" || die "could not publish $final"
published=yes
sync
log "backup ok: $final ($(du -sh "$final" | cut -f1), ${upload_files} upload files)"

# --- Retention -----------------------------------------------------------------------------------
# Candidates are directories (not symlinks) directly in BACKUP_DIR named exactly rokh-YYYYmmdd-HHMMSS.
# A set is "complete" when it holds exactly db.dump, uploads.tar.gz, INFO and SHA256SUMS as regular
# files and nothing else. Only complete sets count towards KEEP_MIN, and only complete sets older
# than KEEP_DAYS beyond the newest KEEP_MIN are removed. Incomplete sets, sets with extra files,
# interrupted .partial runs and every other name are never touched; they are reported (exit 3).
# These local sets do not replace the off-site copies (deploy/README.md §8.4).
EXIT_FAIL=3
status=0
complete() { # complete DIR — exactly the four expected regular files
  local d=$1 f
  for f in db.dump uploads.tar.gz INFO SHA256SUMS; do
    [ -f "$d/$f" ] && [ ! -L "$d/$f" ] || return 1
  done
  [ -z "$(find "$d" -mindepth 1 \( ! -type f -o \( ! -name db.dump ! -name uploads.tar.gz ! -name INFO ! -name SHA256SUMS \) \) -print -quit)" ]
}
for stale in "$backup_dir"/.rokh-*.partial; do
  [ -e "$stale" ] || continue
  log "warning: unfinished set from an interrupted run kept: $stale (inspect, then remove by hand)"
  status=3
done
cutoff="$(date -d "-$keep_days days" +%Y%m%d-%H%M%S)"
mapfile -t sets < <(find "$backup_dir" -mindepth 1 -maxdepth 1 -type d -name 'rokh-*' -printf '%f\n' |
  grep -E '^rokh-[0-9]{8}-[0-9]{6}$' | sort -r)
kept=0
for s in "${sets[@]}"; do
  d="$backup_dir/$s"
  if ! complete "$d"; then
    log "warning: retention kept $s: not a complete set (missing or unexpected files; inspect by hand)"
    status=3
    continue
  fi
  if [ "$kept" -lt "$keep_min" ] || ! [[ ${s#rokh-} < $cutoff ]]; then
    kept=$((kept + 1))
    continue
  fi
  rm -f -- "$d/db.dump" "$d/uploads.tar.gz" "$d/INFO" "$d/SHA256SUMS"
  if rmdir -- "$d"; then log "retention: removed $s"; else status=3; fi
done
log "retention: $kept complete set(s) kept locally (keep at least $keep_min, $keep_days days)"
exit "$status"
