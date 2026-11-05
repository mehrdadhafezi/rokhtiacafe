#!/usr/bin/env bash
# R1-B request matrix (docs/R1-R2-RUNTIME-EVIDENCE.md §R1-B). OWNER-APPROVED RUNTIME USE ONLY.
#
# Sends forged and malformed X-Forwarded-For cases to backend/tools/xffprobe running where rokh
# would listen, through the CDN and straight to the origin, and prints the probe's JSON verdicts
# (classified entries, no real addresses):
#   CDN_URL=https://staging.example/r1-probe ORIGIN_URL=https://<origin-ip>/r1-probe \
#   ORIGIN_HOST=staging.example deploy/r1/probe-matrix.sh
# Forged values come from the documentation ranges only. No cookies, tokens or credentials are
# sent. The production hostname is refused unless R1_OWNER_APPROVAL names the written approval.
set -euo pipefail
: "${CDN_URL:?}" "${ORIGIN_URL:?}" "${ORIGIN_HOST:?}"
for u in "$CDN_URL" "$ORIGIN_URL" "$ORIGIN_HOST"; do
  case "$u" in
    *rokh.tiacafe.ir* | *62.204.61.18*)
      [ -n "${R1_OWNER_APPROVAL:-}" ] || { echo "refusing the production origin/host without R1_OWNER_APPROVAL" >&2; exit 1; } ;;
  esac
done
F1=198.51.100.77
F2=198.51.100.78
F6=2001:db8:f::77

send() { # send NAME TARGET [XFF line]...
  local name=$1 target=$2
  shift 2
  local args=(-sS --max-time 15 -H "X-R1-Case: $name" -H "Cache-Control: no-cache")
  local sent=()
  for line in "$@"; do
    args+=(-H "X-Forwarded-For: $line")
    sent+=("$line")
  done
  args+=(-H "X-R1-Sent: $(IFS=,; echo "${sent[*]}" | tr -d ' ')")
  if [ "$target" = origin ]; then
    # Straight to the origin, bypassing the CDN (TLS name = the site, address = the origin).
    curl "${args[@]}" -H "Host: $ORIGIN_HOST" -k "$ORIGIN_URL" || echo "{\"case\":\"$name\",\"error\":\"request failed\"}"
  else
    curl "${args[@]}" "$CDN_URL" || echo "{\"case\":\"$name\",\"error\":\"request failed\"}"
  fi
  echo
}

for target in cdn origin; do
  send "$target/no-header" "$target"
  send "$target/forged-one" "$target" "$F1"
  send "$target/forged-two-entries" "$target" "$F1, $F2"
  send "$target/forged-two-lines" "$target" "$F1" "$F2"
  send "$target/forged-ipv6" "$target" "$F6"
  send "$target/forged-bracketed-port" "$target" "[$F6]:443, $F1:8080"
  send "$target/malformed" "$target" "not-an-ip, $F1, 999.1.1.1"
  send "$target/empty-line" "$target" "" "$F1"
  send "$target/long-chain" "$target" "$(for i in $(seq 1 20); do printf '198.51.100.%s, ' "$i"; done)$F1"
  # Alternative client-IP headers must never reach rokh (deploy/litespeed include unsets them).
  curl -sS --max-time 15 -H "X-R1-Case: $target/alt-headers" -H "X-Real-IP: $F1" -H "MC_REAL_IP: $F1" \
    -H "Forwarded: for=$F1" -H "X-R1-Sent: " $( [ "$target" = origin ] && printf -- '-k -H Host:%s %s' "$ORIGIN_HOST" "$ORIGIN_URL" || printf '%s' "$CDN_URL") || true
  echo
done
