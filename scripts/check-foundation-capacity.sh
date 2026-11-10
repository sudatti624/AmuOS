#!/usr/bin/env bash
# Run from the repository root on Linux with sudo/mount available.
set -euo pipefail
root=$(mktemp -d)
mounted=0
cleanup() {
  if [ "$mounted" = 1 ]; then sudo umount "$root"; fi
  rmdir "$root"
}
trap cleanup EXIT
sudo mount -t tmpfs -o size=32m,mode=1777 tmpfs "$root"
mounted=1
AMUOS_FAULT_DIR="$root" go test -v -count=1 -timeout=5m ./internal/recovery -run '^TestFoundationCapacityFailure$'

AMUOS_FAULT_DIR="$root" go test -v -count=1 -timeout=5m ./internal/fileops -run '^TestFileCapacityFailure$'
