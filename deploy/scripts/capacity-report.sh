#!/usr/bin/env bash
# Disk-capacity report for the release gate (docs/HOSTING-ASSESSMENT.md §6, G1). Read-only: it
# creates, changes and deletes nothing. Run as root once a day for the baseline period:
#   /opt/rokh/scripts/capacity-report.sh | tee -a /root/rokh-capacity.log
# It reports facts only; the Go/No-Go decision is made by the owner and the host (G1).
set -uo pipefail

echo "== capacity report $(date -Is) on $(hostname)"
for p in / /backup /var/lib/docker /var/log /opt /var/lib/rokh; do
  [ -e "$p" ] || continue
  df -P -h "$p" | awk -v p="$p" 'NR == 2 { printf "%-18s mount %-14s size %6s used %6s free %6s (%s used)\n", p, $6, $2, $3, $4, $5 }'
  df -P -i "$p" | awk -v p="$p" 'NR == 2 { printf "%-18s inodes used %s\n", p, $5 }'
done
if command -v journalctl > /dev/null; then
  echo "journal:           $(journalctl --disk-usage 2> /dev/null | sed 's/^Archived and active journals take up //')"
fi
if command -v docker > /dev/null && docker info > /dev/null 2>&1; then
  echo "== docker"
  docker system df 2> /dev/null
  docker volume inspect rokh-pgdata > /dev/null 2>&1 &&
    echo "rokh-pgdata:       $(du -sh "$(docker volume inspect -f '{{.Mountpoint}}' rokh-pgdata)" 2> /dev/null | cut -f1)"
fi
for d in /backup/rokh /var/lib/rokh/uploads /opt/rokh; do
  [ -d "$d" ] && echo "$(printf '%-18s' "$d") $(du -sh "$d" 2> /dev/null | cut -f1)"
done
exit 0
