#!/usr/bin/env bash
# Builds the Linux amd64 release archive into dist/release.
# Run from the repository root on Linux after building web/ (pnpm exec vp build).
set -euo pipefail

version="${1:?usage: scripts/package.sh <version>}"
name="amuos_${version#v}_linux_amd64"
out="dist/release"

rm -rf "$out"
mkdir -p "$out/$name"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X main.version=$version" \
  -o "$out/$name/amuos-controlplane" ./cmd/controlplane
go run ./scripts/licenses -out "$out/$name/THIRD_PARTY_LICENSES.md"
cp LICENSE "$out/$name/LICENSE"
cp docs/INSTALL.md "$out/$name/INSTALL.md"
cp docs/BACKUP.md "$out/$name/BACKUP.md"

tar -C "$out" --owner=0 --group=0 -czf "$out/$name.tar.gz" "$name"
(cd "$out" && sha256sum "$name.tar.gz" > SHA256SUMS)
echo "$out/$name.tar.gz"
