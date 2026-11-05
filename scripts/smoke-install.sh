#!/usr/bin/env bash
# Installs a release archive in a new directory by following docs/INSTALL.md,
# then checks first setup, login, a host slot, restart and stopped recovery.
# The certificate is self-signed and only for this check.
set -euo pipefail
trap 'echo "::error::line $LINENO: $BASH_COMMAND"' ERR

archive="$(realpath "${1:?usage: scripts/smoke-install.sh <archive.tar.gz>}")"
work="$(mktemp -d)"
pid=""
agent_pid=""
trap '[ -z "$pid" ] || kill "$pid" 2>/dev/null || true; [ -z "$agent_pid" ] || kill "$agent_pid" 2>/dev/null || true; rm -rf "$work"' EXIT
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
    --tls-cert data/server.pem --tls-key data/server-key.pem 2>>server.log &
  pid=$!
  for _ in $(seq 50); do
    curl -ksf "$origin/healthz" >/dev/null && return 0
    sleep 0.2
  done
  echo "::error::FAIL: server did not start"
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
  [ "$1" = "$2" ] || { echo "::error::FAIL: $3 (got $1, want $2)"; exit 1; }
}
login() {
  api -X POST "$origin/api/v1/auth/login" -d "{\"username\":\"owner\",\"password\":\"$password\"}" |
    sed -n 's/.*"csrf_token":"\([^"]*\)".*/\1/p'
}

start
curl -ksf "$origin/" | grep -q '<div id="root">' || { echo "::error::FAIL: web UI is missing"; exit 1; }

code=$(api -o /dev/null -w '%{http_code}' -X POST "$origin/api/v1/setup" \
  -d "{\"token\":\"$(tr -d '\n' < data/setup-token)\",\"username\":\"owner\",\"password\":\"$password\"}")
expect "$code" 201 "first setup"
rm data/setup-token

csrf=$(login)
[ -n "$csrf" ] || { echo "::error::FAIL: login"; exit 1; }
api "$origin/api/v1/me" | grep -q '"username":"owner"' || { echo "::error::FAIL: /me"; exit 1; }
code=$(api -o /dev/null -w '%{http_code}' -X POST "$origin/api/v1/hosts" \
  -H "X-CSRF-Token: $csrf" -H "Idempotency-Key: smoke-$(date +%s%N)" -d '{"name":"smoke-host"}')
expect "$code" 201 "host slot"

# Data must survive a restart.
stop
start
csrf=$(login)
[ -n "$csrf" ] || { echo "::error::FAIL: login after restart"; exit 1; }
api "$origin/api/v1/hosts" | grep -q '"name":"smoke-host"' || { echo "::error::FAIL: host after restart"; exit 1; }

# Enroll a real Agent with a one-time code, then let it reach the mTLS port.
agent_host=$(api -X POST "$origin/api/v1/hosts" -H "X-CSRF-Token: $csrf" -H "Idempotency-Key: agent-$(date +%s%N)" \
  -d '{"name":"agent-host"}' | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
[ -n "$agent_host" ] || { echo "::error::FAIL: agent host"; exit 1; }
(umask 077; api -X POST "$origin/api/v1/hosts/$agent_host/enrollment" -H "X-CSRF-Token: $csrf" -d '{}' |
  sed -n 's/.*"code":"\([^"]*\)".*/\1/p' > agent-code)
[ -s agent-code ] || { echo "::error::FAIL: enrollment code"; exit 1; }
agent_args=(--state-dir agent-state --server https://127.0.0.1:8443
  --agent-server https://127.0.0.1:9443 --ca-file data/server.pem)
./amuos-agent "${agent_args[@]}" --enroll-code-file agent-code
rm agent-code
# Start the binary directly so $! is the Agent itself and kill releases its lock.
./amuos-agent "${agent_args[@]}" &
agent_pid=$!
connected=""
for _ in $(seq 50); do
  api "$origin/api/v1/hosts" | grep -q '"connected":true' && { connected=yes; break; }
  sleep 0.2
done
# The first sync also sends host information and a sample read from /proc.
monitored=""
for _ in $(seq 50); do
  api "$origin/api/v1/hosts/$agent_host" | grep -q '"kernel":"[^"]' &&
    api "$origin/api/v1/hosts/$agent_host/metrics?range=1h" | grep -q '"times":\[[0-9]' &&
    { monitored=yes; break; }
  sleep 0.2
done
kill "$agent_pid"; wait "$agent_pid" 2>/dev/null || true
agent_pid=""
[ -n "$connected" ] || { echo "::error::FAIL: agent did not connect"; exit 1; }
[ -n "$monitored" ] || { echo "::error::FAIL: no host information or samples: $(api "$origin/api/v1/hosts/$agent_host" | head -c 300)"; exit 1; }
stop

# Recovery keys are never supplied as command-line values or printed.
mkdir -m 700 recovery
./amuos-controlplane --generate-recovery-key recovery/identity.key > recovery/recipient
recipient="$(cat recovery/recipient)"
./amuos-controlplane --db data/amuos.db --backup-out recovery/snapshot.age \
  --recipient "$recipient" --addr 127.0.0.1:8443 --origin "$origin" > recovery/manifest.json
# Hand the backup to a separate, empty environment for an independent restore.
if [ -n "${AMUOS_EXPORT_BACKUP:-}" ]; then
  mkdir -p "$AMUOS_EXPORT_BACKUP"
  cp recovery/snapshot.age recovery/identity.key recovery/manifest.json "$AMUOS_EXPORT_BACKUP/"
fi
if ./amuos-controlplane --db data/amuos.db --backup-out recovery/snapshot.age \
  --recipient "$recipient" >/dev/null 2>&1; then
  echo "::error::FAIL: backup overwrote an existing archive"; exit 1
fi

start
if ./amuos-controlplane --db data/amuos.db --restore-from recovery/snapshot.age \
  --recovery-key-file recovery/identity.key >/dev/null 2>&1; then
  echo "::error::FAIL: restore ran while the server owned the state"; exit 1
fi
stop

head -c -1 recovery/snapshot.age > recovery/broken.age
if ./amuos-controlplane --db data/amuos.db --restore-from recovery/broken.age \
  --recovery-key-file recovery/identity.key >/dev/null 2>&1; then
  echo "::error::FAIL: restore accepted a truncated archive"; exit 1
fi
./amuos-controlplane --db data/amuos.db --restore-from recovery/snapshot.age \
  --recovery-key-file recovery/identity.key > recovery/restore.json
start
# The cookie from before backup must no longer authenticate.
code=$(api -o /dev/null -w '%{http_code}' "$origin/api/v1/me")
expect "$code" 401 "old session after restore"
csrf=$(login)
[ -n "$csrf" ] || { echo "::error::FAIL: login after restore"; exit 1; }
api "$origin/api/v1/hosts" | grep -q '"name":"smoke-host"' || { echo "::error::FAIL: host after restore"; exit 1; }
api "$origin/api/v1/audit" | grep -q 'restore.complete' || { echo "::error::FAIL: restore audit"; exit 1; }
# A restore issues a new Agent CA, so the restored Agent credential fails the
# TLS handshake. The Agent keeps retrying until it is stopped.
status=0
timeout 5 ./amuos-agent "${agent_args[@]}" 2> agent.log || status=$?
expect "$status" 124 "agent after restore keeps retrying: $(tail -n 1 agent.log)"
grep -q 'heartbeat failed' agent.log &&
  grep -q 'TLS handshake error.*unknown authority' server.log || {
  echo "::error::FAIL: agent credential survived the restore: $(tail -n 2 agent.log | tr '\n' ' ')"; exit 1
}
api "$origin/api/v1/hosts" | grep -q '"name":"agent-host","state":"pending"' || { echo "::error::FAIL: agent host after restore"; exit 1; }
stop

echo "PASS: install, setup, login, host, restart, encrypted backup and restore"
