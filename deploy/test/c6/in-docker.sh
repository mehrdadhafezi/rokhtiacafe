#!/usr/bin/env bash
# C6 staging rehearsal in a throwaway PostgreSQL container (docs/C6-STAGING-REHEARSAL.md §2, §4):
#   deploy/test/c6/in-docker.sh NEW_RELEASE_DIR OLD_RELEASE_DIR C6_TOOL [EVIDENCE_DIR]
# NEW/OLD_RELEASE_DIR hold rokh-linux-amd64 (+ BUILDINFO.txt) of the C1–C5 build and of the
# previous production-compatible build; C6_TOOL is backend/tools/c6 built for linux/amd64.
# TEST_PG_IMAGE selects the server (default postgres:16-bookworm; CI also runs 17).
# The cluster, roles, passwords, uploads and backups exist only inside the container, which is
# removed afterwards. The evidence directory receives snapshots (row digests and sums only) and
# the log. Nothing else is touched; never point this at a real server.
set -euo pipefail
repo="$(cd "$(dirname "$0")/../../.." && pwd)"
new="$(cd "${1:?usage: in-docker.sh NEW OLD C6_TOOL [EVIDENCE]}" && pwd)"
old="$(cd "${2:?}" && pwd)"
tool="$(cd "$(dirname "${3:?}")" && pwd)/$(basename "$3")"
evidence="${4:-$(mktemp -d)}"
mkdir -p "$evidence"
evidence="$(cd "$evidence" && pwd)"
image="${TEST_PG_IMAGE:-postgres:16-bookworm}"
native() { if command -v cygpath > /dev/null; then cygpath -w "$1"; else printf '%s' "$1"; fi; }
command -v cygpath > /dev/null && export MSYS_NO_PATHCONV=1 # Git Bash on Windows
echo "evidence: $evidence"
exec docker run --rm --name "rokh-c6-$$" \
  --tmpfs /backup:rw,size=256m,mode=755 \
  -v "$(native "$repo"):/repo:ro" -v "$(native "$new"):/new:ro" -v "$(native "$old"):/old:ro" \
  -v "$(native "$tool"):/tools/c6:ro" -v "$(native "$evidence"):/evidence" \
  --entrypoint bash "$image" /repo/deploy/test/c6/rehearsal.sh
