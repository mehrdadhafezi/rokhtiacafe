#!/usr/bin/env bash
# Scheduled backup (deploy/README.md §8.1). The only supported way to run backup.sh from cron:
#   MAILTO=<owner address>
#   30 3 * * * /opt/rokh/scripts/backup-cron.sh
#
# backup.sh is never piped: its output goes to a temporary file and its exit code is captured
# directly, so it cannot be masked by a logger or by /bin/sh pipeline semantics. Then:
#   - the result is recorded in BACKUP_STATUS_DIR/last-run (always) and last-success (exit 0 or 3);
#   - the output goes to syslog/journal (tag rokh-backup; priority err unless the backup succeeded);
#   - on any non-zero result a summary is printed to stdout, which cron mails to MAILTO;
#   - this script exits with backup.sh's own exit code.
# On success it prints nothing (no mail). backup-status.sh is the fail-closed check of the record.
#
# Exit codes are backup.sh's: 0 ok · 1 refused · 2 failed · 3 ok but retention needs attention.
# 4 = this wrapper could not record the result (treated as a failure by backup-status.sh).
set -uo pipefail
umask 077
. "$(dirname "$0")/lib.sh"

OPS_FILE="${ROKH_OPS_ENV:-/etc/rokh/ops.env}"
[ -e "$OPS_FILE" ] || OPS_FILE=""
status_dir="$(opt BACKUP_STATUS_DIR /var/lib/rokh-backup)"
backup_cmd="$(opt BACKUP_COMMAND "$(dirname "$0")/backup.sh")"
logger_cmd="$(opt BACKUP_LOGGER logger)"

started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
out="$(mktemp)" || {
  echo "ALERT rokh backup: cannot create a temporary file; backup NOT run"
  exit 4
}
"$backup_cmd" > "$out" 2>&1 < /dev/null
code=$?
finished="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
case $code in
  0) result=ok ;;
  1) result=refused ;;
  2) result=failed ;;
  3) result=retention-warning ;;
  *) result=failed ;;
esac

priority=user.err
[ "$code" = 0 ] && priority=user.info
"$logger_cmd" -t rokh-backup -p "$priority" -f "$out" > /dev/null 2>&1 || true
"$logger_cmd" -t rokh-backup -p "$priority" "result=$result exit=$code" > /dev/null 2>&1 || true

recorded=yes
if [ -d "$status_dir" ] && [ -w "$status_dir" ]; then
  printf 'result=%s\nexit=%s\nstarted=%s\nfinished=%s\n' "$result" "$code" "$started" "$finished" \
    > "$status_dir/last-run.tmp" && mv -f -- "$status_dir/last-run.tmp" "$status_dir/last-run" || recorded=no
  if [ "$code" = 0 ] || [ "$code" = 3 ]; then
    printf '%s\n' "$finished" > "$status_dir/last-success.tmp" &&
      mv -f -- "$status_dir/last-success.tmp" "$status_dir/last-success" || recorded=no
  fi
else
  recorded=no
fi

if [ "$code" != 0 ] || [ "$recorded" != yes ]; then
  echo "ALERT rokh backup on $(hostname): result=$result exit=$code (started $started)"
  [ "$recorded" = yes ] || echo "ALERT the result could not be recorded in $status_dir"
  case $result in
    refused) echo "Nothing was written: fix the precondition below, then run backup-cron.sh by hand." ;;
    failed) echo "No new backup set was published. Fix the cause below, then run backup-cron.sh by hand." ;;
    retention-warning) echo "The backup succeeded; retention kept something it could not safely remove." ;;
  esac
  echo "--- last lines of backup.sh output ---"
  tail -n 20 "$out"
  echo "See deploy/README.md §8 and §11."
fi
rm -f -- "$out"
[ "$recorded" = yes ] || [ "$code" != 0 ] || exit 4
exit "$code"
