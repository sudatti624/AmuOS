#!/usr/bin/env bash
# Restores a backup made on another machine into a new, empty environment by
# following docs/BACKUP.md, then checks the restored state. The backup and its
# recovery key come from scripts/smoke-install.sh (AMUOS_EXPORT_BACKUP) and are
# dummy test data. The certificate is self-signed and only for this check.
set -euo pipefail

archive="$(realpath "${1:?usage: scripts/smoke-restore.sh <archive.tar.gz> <backup-dir>}")"
backup="$(realpath "${2:?usage: scripts/smoke-restore.sh <archive.tar.gz> <backup-dir>}")"
work="$(mktemp -d)"
pid=""
trap '[ -n "$pid" ] && kill "$pid" 2>/dev/null; rm -rf "$work"' EXIT
cd "$work"

origin="https://localhost:8443"
password="correct horse battery staple"
fail() { echo "FAIL: $*" >&2; exit 1; }
api() {
  curl -ksS -b cookies -c cookies -H "Origin: $origin" -H "Content-Type: application/json" "$@"
}

tar -xzf "$archive"
cd amuos_*_linux_amd64
mkdir -m 700 recovery
install -m 600 "$backup/identity.key" recovery/identity.key
id="$(sed -n 's/.*"backup_id":"\([0-9a-f]*\)".*/\1/p' "$backup/manifest.json")"
[ -n "$id" ] || fail "backup ID"

# Nothing exists here yet: restore creates the state directory and database.
[ ! -e data ] || fail "environment is not empty"
./amuos-controlplane --db data/amuos.db --restore-from "$backup/snapshot.age" \
  --recovery-key-file recovery/identity.key > restore.json || fail "restore"
grep -q "\"backup_id\":\"$id\"" restore.json || fail "restored a different backup"
[ "$(stat -c '%a' data)" = 700 ] || fail "state directory permissions"
[ "$(stat -c '%a' data/amuos.db)" = 600 ] || fail "database permissions"

openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 1 \
  -subj /CN=localhost -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
  -keyout data/server-key.pem -out data/server.pem 2>/dev/null
chmod 600 data/server-key.pem
./amuos-controlplane --db data/amuos.db --addr 127.0.0.1:8443 --origin "$origin" \
  --tls-cert data/server.pem --tls-key data/server-key.pem &
pid=$!
for _ in $(seq 50); do
  curl -ksf "$origin/healthz" >/dev/null && break
  sleep 0.2
done
curl -ksf "$origin/healthz" >/dev/null || fail "server did not start"

# Setup must stay closed: the restored instance already has an owner.
code=$(api -o /dev/null -w '%{http_code}' -X POST "$origin/api/v1/setup" \
  -d "{\"token\":\"invalid\",\"username\":\"intruder\",\"password\":\"$password\"}")
[ "$code" = 401 ] || fail "setup reopened after restore ($code)"
csrf=$(api -X POST "$origin/api/v1/auth/login" -d "{\"username\":\"owner\",\"password\":\"$password\"}" |
  sed -n 's/.*"csrf_token":"\([^"]*\)".*/\1/p')
[ -n "$csrf" ] || fail "owner login after restore"
api "$origin/api/v1/hosts" | grep -q '"name":"smoke-host"' || fail "host after restore"
audit="$(api "$origin/api/v1/audit")"
grep -q 'restore.complete' <<<"$audit" || fail "restore audit"
grep -q 'credentials_invalidated' <<<"$audit" || fail "credential invalidation audit"
kill "$pid"
wait "$pid" || true
pid=""

echo "PASS: independent restore into an empty environment"
