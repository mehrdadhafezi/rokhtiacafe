#!/usr/bin/env bash
# Run the deployment test suite inside a throwaway PostgreSQL 16 container:
#   deploy/test/in-docker.sh release/
# The container gets a fresh cluster, the host's client tools (PG_CLIENT=host), a separate tmpfs
# mounted at /backup (so the mount-point checks are real) and an unprivileged user for the rokh
# binary. The repository is mounted read-only; the container is removed afterwards. Nothing else
# is touched.
set -euo pipefail
repo="$(cd "$(dirname "$0")/../.." && pwd)"
release="$(cd "${1:?usage: in-docker.sh <release dir>}" && pwd)"
image="${TEST_PG_IMAGE:-postgres:16-bookworm}"
if command -v cygpath > /dev/null; then # Git Bash on Windows
  repo="$(cygpath -w "$repo")"
  release="$(cygpath -w "$release")"
  export MSYS_NO_PATHCONV=1
fi
exec docker run --rm --name "rokh-deploytest-$$" \
  --tmpfs /backup:rw,size=256m,mode=755 \
  -v "$repo:/repo:ro" -v "$release:/release:ro" \
  --entrypoint bash "$image" /repo/deploy/test/in-container.sh
