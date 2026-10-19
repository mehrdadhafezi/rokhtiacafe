#!/usr/bin/env bash
# Supervisor loop for hosts without systemd (cPanel), started from cron:
#   @reboot cd ~/rokh && ./scripts/run.sh >> logs/rokh.log 2>&1
# Restarts `rokh serve` after it exits; restart a release with: pkill -TERM -f 'bin/rokh serve'.
# Use only where the hosting assessment confirmed long-running user processes are allowed.
set -uo pipefail

cd "$(dirname "$0")/.."
lock=".run.lock"
exec 9> "$lock"
flock -n 9 || { echo "run.sh already running" >&2; exit 0; }

delay=2
while true; do
  started=$(date +%s)
  bin/rokh serve
  code=$?
  # Back off when it keeps failing fast (bad config, database down), up to one minute.
  if [ $(( $(date +%s) - started )) -lt 30 ]; then
    delay=$(( delay * 2 > 60 ? 60 : delay * 2 ))
  else
    delay=2
  fi
  echo "$(date -Is) rokh exited with code $code; restarting in ${delay}s" >&2
  sleep "$delay"
done
