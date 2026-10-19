#!/usr/bin/env bash
# Release build (T13): both React entry points embedded in one static Linux amd64 binary.
#
#   scripts/build-release.sh [version]     # default version: git describe
#
# Output in release/: rokh-linux-amd64, rokh-linux-amd64.sha256, BUILDINFO.txt.
# Reproducible: the same commit, Go toolchain and npm lockfile give the same SHA-256
# (no build paths, build IDs, VCS stamps or timestamps in the binary).
# Builds only — never deploys.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
version="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
commit="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
out="$root/release"
ui="$root/backend/web/ui"

echo "==> frontend (npm ci, build, staff-only PWA check)"
(cd frontend && npm ci --no-audit --no-fund && npm run build && npm run check:pwa)

echo "==> embedding frontend/dist into backend/web/ui"
find "$ui" -mindepth 1 ! -name .gitkeep -exec rm -rf {} +
cp -R frontend/dist/. "$ui/"
rm -rf "$ui/.vite" # build manifest: not served
test -f "$ui/index.html" && test -f "$ui/admin/index.html" && test -f "$ui/admin/sw.js"

echo "==> backend (linux/amd64, static)"
mkdir -p "$out"
(
  cd backend
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFLAGS=-mod=readonly \
    go build -trimpath -buildvcs=false \
    -ldflags "-s -w -buildid= -X main.version=$version" \
    -o "$out/rokh-linux-amd64" ./cmd/rokh
)

(cd "$out" && sha256sum rokh-linux-amd64 > rokh-linux-amd64.sha256)
{
  echo "version: $version"
  echo "commit:  $commit"
  echo "go:      $(cd backend && go env GOVERSION)"
  echo "node:    $(node --version)"
  echo "sha256:  $(cut -d' ' -f1 "$out/rokh-linux-amd64.sha256")"
} > "$out/BUILDINFO.txt"
cat "$out/BUILDINFO.txt"
