#!/usr/bin/env bash
# Fail-closed backup check (deploy/README.md §8.1, §11). Run daily from root's crontab after the
# backup window, e.g. `0 9 * * * /opt/rokh/scripts/backup-status.sh` (with MAILTO set), and by hand:
#   /opt/rokh/scripts/backup-status.sh -v
# Silent and exit 0 only when the last run succeeded AND the last success is recent. Anything
# else — no record, unreadable record, failed/refused run, stale success — prints an ALERT (cron
# mails it) and exits non-zero:
#   0 ok · 1 not ok (missing, failed, refused, stale or unrecorded) · 3 ok but retention warning
# Settings: BACKUP_STATUS_DIR, BACKUP_MAX_AGE_HOURS (default 26) in ops.env.
set -uo pipefail
. "$(dirname "$0")/lib.sh"

OPS_FILE="${ROKH_OPS_ENV:-/etc/rokh/ops.env}"
[ -e "$OPS_FILE" ] || OPS_FILE=""
status_dir="$(opt BACKUP_STATUS_DIR /var/lib/rokh-backup)"
max_age="$(opt BACKUP_MAX_AGE_HOURS 26)"
verbose=no
[ "${1:-}" = -v ] && verbose=yes

alert() {
  echo "ALERT rokh backup on $(hostname): $*"
  echo "See deploy/README.md §8 and §11 (backup log: journalctl -t rokh-backup)."
  exit 1
}
is_uint "$max_age" || alert "BACKUP_MAX_AGE_HOURS is not a whole number"
[ -r "$status_dir/last-run" ] || alert "no backup has been recorded ($status_dir/last-run missing)"
result="$(env_get "$status_dir/last-run" result)"
code="$(env_get "$status_dir/last-run" exit)"
finished="$(env_get "$status_dir/last-run" finished)"
case $result in
  ok | retention-warning) ;;
  refused | failed) alert "last backup run $result (exit $code) at $finished" ;;
  *) alert "unreadable backup record in $status_dir/last-run" ;;
esac
[ -r "$status_dir/last-success" ] || alert "no successful backup recorded"
last="$(head -n 1 "$status_dir/last-success")"
last_s="$(date -u -d "$last" +%s 2> /dev/null)" || alert "unreadable last-success time '$last'"
age_h=$((($(date -u +%s) - last_s) / 3600))
[ "$age_h" -lt "$max_age" ] || alert "last successful backup is ${age_h} h old (limit ${max_age} h, at $last)"

if [ "$result" = retention-warning ]; then
  echo "WARNING rokh backup on $(hostname): backup ok at $last, but retention kept a set it could not"
  echo "safely remove (exit 3). Inspect BACKUP_DIR; see deploy/README.md §8.3."
  exit 3
fi
[ "$verbose" = no ] || echo "backup ok: last success $last (${age_h} h ago)"
exit 0
