#!/usr/bin/env bash
# Installs a release archive in a new directory by following docs/INSTALL.md,
# then checks first setup, login, a host slot and a restart.
# The certificate is self-signed and only for this check.
set -euo pipefail

archive="$(realpath "${1:?usage: scripts/smoke-install.sh <archive.tar.gz>}")"
work="$(mktemp -d)"
pid=""
trap '[ -n "$pid" ] && kill "$pid" 2>/dev/null; rm -rf "$work"' EXIT
cd "$work"

tar -xzf "$archive"
cd amuos_*_linux_amd64
./amuos-controlplane --version

mkdir -m 700 data
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 1 \
  -subj /CN=localhost -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
  -keyout data/server-key.pem -out data/server.pem 2>/dev/null
chmod 600 data/server-key.pem
./amuos-controlplane --db data/amuos.db --bootstrap-file data/setup-token

origin="https://localhost:8443"
password="correct horse battery staple"

start() {
  ./amuos-controlplane --db data/amuos.db --addr 127.0.0.1:8443 --origin "$origin" \
    --tls-cert data/server.pem --tls-key data/server-key.pem &
  pid=$!
  for _ in $(seq 50); do
    curl -ksf "$origin/healthz" >/dev/null && return 0
    sleep 0.2
  done
  echo "FAIL: server did not start" >&2
  exit 1
}
stop() {
  kill "$pid"
  wait "$pid" || true
  pid=""
}
api() {
  curl -ksS -b cookies -c cookies -H "Origin: $origin" -H "Content-Type: application/json" "$@"
}
expect() {
  [ "$1" = "$2" ] || { echo "FAIL: $3 (got $1, want $2)" >&2; exit 1; }
}
login() {
  api -X POST "$origin/api/v1/auth/login" -d "{\"username\":\"owner\",\"password\":\"$password\"}" |
    sed -n 's/.*"csrf_token":"\([^"]*\)".*/\1/p'
}

start
curl -ksf "$origin/" | grep -q '<div id="root">' || { echo "FAIL: web UI is missing" >&2; exit 1; }

code=$(api -o /dev/null -w '%{http_code}' -X POST "$origin/api/v1/setup" \
  -d "{\"token\":\"$(tr -d '\n' < data/setup-token)\",\"username\":\"owner\",\"password\":\"$password\"}")
expect "$code" 201 "first setup"
rm data/setup-token

csrf=$(login)
[ -n "$csrf" ] || { echo "FAIL: login" >&2; exit 1; }
api "$origin/api/v1/me" | grep -q '"username":"owner"' || { echo "FAIL: /me" >&2; exit 1; }
code=$(api -o /dev/null -w '%{http_code}' -X POST "$origin/api/v1/hosts" \
  -H "X-CSRF-Token: $csrf" -H "Idempotency-Key: smoke-$(date +%s%N)" -d '{"name":"smoke-host"}')
expect "$code" 201 "host slot"

# Data must survive a restart.
stop
start
csrf=$(login)
[ -n "$csrf" ] || { echo "FAIL: login after restart" >&2; exit 1; }
api "$origin/api/v1/hosts" | grep -q '"name":"smoke-host"' || { echo "FAIL: host after restart" >&2; exit 1; }
stop

echo "PASS: install, first setup, login, host slot and restart"
