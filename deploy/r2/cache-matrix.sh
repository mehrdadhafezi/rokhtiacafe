#!/usr/bin/env bash
# R2-B CDN cache matrix (docs/R1-R2-RUNTIME-EVIDENCE.md §R2-B). OWNER-APPROVED RUNTIME USE ONLY.
#
# Requests every private and public path class twice through the CDN and prints, per request, only
# the status line and caching headers (Cache-Control, Pragma, Vary, Age, Expires and any header
# naming a cache, e.g. Mc-Cache-Status). Response bodies, cookies and tokens are never printed.
#   CDN_URL=https://staging.example deploy/r2/cache-matrix.sh
# Optional, for signed-in rows (staging test accounts only, never real users):
#   CUSTOMER_A_COOKIE, CUSTOMER_B_COOKIE, ADMIN_COOKIE, OPERATOR_COOKIE  ("name=value", not printed)
# PASS needs: every /api/*, /admin/* and HTML response shows a cache BYPASS/MISS status from the
# edge on the second request and never an Age > 0; hashed /assets/* may HIT.
set -euo pipefail
: "${CDN_URL:?}"
case "$CDN_URL" in
  *rokh.tiacafe.ir*) [ -n "${R2_OWNER_APPROVAL:-}" ] || { echo "refusing production without R2_OWNER_APPROVAL" >&2; exit 1; } ;;
esac
show() { # show LABEL PATH [cookie] [header]
  local label=$1 path=$2 cookie=${3:-} extra=${4:-}
  local args=(-sS -o /dev/null -D - --max-time 15)
  [ -n "$cookie" ] && args+=(-H "Cookie: $cookie")
  [ -n "$extra" ] && args+=(-H "$extra")
  for n in 1 2; do
    printf '%-34s #%s ' "$label" "$n"
    curl "${args[@]}" "$CDN_URL$path" | tr -d '\r' |
      grep -iE '^(HTTP/|cache-control:|pragma:|vary:|age:|expires:|[a-z-]*cache[a-z-]*:)' | tr '\n' ' '
    echo
  done
}
show "public menu" /api/v1/public/menu
show "tracking (fake token 1)" /api/v1/public/order "" "X-Tracking-Token: r2-matrix-0000000000000000000000000001"
show "tracking (fake token 2)" /api/v1/public/order "" "X-Tracking-Token: r2-matrix-0000000000000000000000000002"
show "customer me (anonymous)" /api/v1/customer/auth/me
show "staff me (anonymous)" /api/v1/auth/me
show "admin orders (anonymous)" /api/v1/admin/orders
show "customer html" /menu/account
show "admin html" /admin/
show "admin service worker" /admin/sw.js
for who in CUSTOMER_A CUSTOMER_B; do
  var="${who}_COOKIE"
  [ -n "${!var:-}" ] && show "customer me ($who)" /api/v1/customer/auth/me "${!var}" && show "history ($who)" /api/v1/customer/orders "${!var}"
done
for who in ADMIN OPERATOR; do
  var="${who}_COOKIE"
  [ -n "${!var:-}" ] && show "staff me ($who)" /api/v1/auth/me "${!var}" && show "summary report ($who)" "/api/v1/admin/reports/summary?from=2026-01-01&to=2026-01-01" "${!var}"
done
asset="$(curl -sS --max-time 15 "$CDN_URL/menu" | grep -oE '/assets/[A-Za-z0-9._-]+\.js' | head -n1 || true)"
[ -n "$asset" ] && show "hashed asset (may HIT)" "$asset"
