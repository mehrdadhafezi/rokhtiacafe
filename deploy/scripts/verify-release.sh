#!/usr/bin/env bash
# Verify a downloaded release before installing it (deploy/README.md §3, §7):
#   deploy/scripts/verify-release.sh <dir with rokh-linux-amd64, .sha256, BUILDINFO.txt> [expected sha256]
# Pass the SHA-256 shown by the CI release job as the second argument; the files alone only prove
# they agree with each other. Does not install or run anything except `rokh version`.
set -euo pipefail

die() {
  echo "error: $*" >&2
  exit 1
}
dir="${1:?usage: verify-release.sh <release dir> [expected sha256]}"
expected="${2:-}"
bin="$dir/rokh-linux-amd64"
[ -f "$bin" ] || die "missing $bin"
[ -f "$bin.sha256" ] || die "missing $bin.sha256"
[ -f "$dir/BUILDINFO.txt" ] || die "missing $dir/BUILDINFO.txt"

# "<hash>  name" (text mode) or "<hash> *name" (binary mode, as written on Windows).
grep -Eqx '[0-9a-f]{64} [ *]rokh-linux-amd64' "$bin.sha256" || die "malformed rokh-linux-amd64.sha256"
(cd "$dir" && sha256sum --quiet --strict -c rokh-linux-amd64.sha256) || die "checksum mismatch: do not install"
sum="$(cut -d' ' -f1 "$bin.sha256")"
grep -Eq "^sha256: +$sum\$" "$dir/BUILDINFO.txt" || die "BUILDINFO.txt names a different SHA-256"
if [ -n "$expected" ]; then
  [ "$sum" = "$expected" ] || die "SHA-256 $sum differs from the expected $expected"
fi
if command -v file > /dev/null; then
  file "$bin" | grep -q 'ELF 64-bit LSB executable, x86-64' || die "not a Linux x86-64 executable"
  file "$bin" | grep -q 'statically linked' || die "not statically linked"
fi
echo "release ok: sha256 $sum${expected:+ (matches expected)}"
grep -E '^(version|commit):' "$dir/BUILDINFO.txt"
