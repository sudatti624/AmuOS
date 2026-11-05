#!/usr/bin/env bash
# Installs a release archive as a systemd service by following docs/INSTALL.md,
# then updates it to another build without reinstalling and rolls it back with
# the pre-update backup. Run as root on a disposable machine with systemd.
# The certificate is self-signed and only for this check.
set -euo pipefail

archive="$(realpath "${1:?usage: scripts/smoke-systemd.sh <archive.tar.gz> <next-binary>}")"
next="$(realpath "${2:?usage: scripts/smoke-systemd.sh <archive.tar.gz> <next-binary>}")"
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cd "$work"

origin="https://localhost:8443"
password="correct horse battery staple"
db=/var/lib/amuos/amuos.db
as_amuos() { runuser -u amuos -- "$@"; }
fail() { echo "FAIL: $*" >&2; journalctl -u amuos-controlplane --no-pager -n 50 >&2 || true; exit 1; }
api() {
  curl -ksS -b "$work/cookies" -c "$work/cookies" -H "Origin: $origin" -H "Content-Type: application/json" "$@"
}
wait_ready() {
  for _ in $(seq 50); do
    curl -ksf "$origin/healthz" >/dev/null && return 0
    sleep 0.2
  done
  fail "service did not become ready"
}
login() {
  rm -f "$work/cookies"
  api -X POST "$origin/api/v1/auth/login" -d "{\"username\":\"owner\",\"password\":\"$password\"}" |
    sed -n 's/.*"csrf_token":"\([^"]*\)".*/\1/p'
}
add_host() {
  code=$(api -o /dev/null -w '%{http_code}' -X POST "$origin/api/v1/hosts" \
    -H "X-CSRF-Token: $1" -H "Idempotency-Key: smoke-$(date +%s%N)" -d "{\"name\":\"$2\"}")
  [ "$code" = 201 ] || fail "host $2 ($code)"
}

# Install (INSTALL.md "サービスとして導入する").
tar -xzf "$archive"
cd amuos_*_linux_amd64
old_version="$(./amuos-controlplane --version)"
useradd --system --home-dir /var/lib/amuos --no-create-home --shell /usr/sbin/nologin amuos
install -m 755 amuos-controlplane /usr/local/bin/amuos-controlplane
install -d -m 755 /etc/amuos
install -d -m 700 /etc/amuos/tls
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 1 \
  -subj /CN=localhost -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
  -keyout /etc/amuos/tls/server-key.pem -out /etc/amuos/tls/server.pem 2>/dev/null
chmod 600 /etc/amuos/tls/server-key.pem
install -m 644 packaging/controlplane.env /etc/amuos/controlplane.env
install -m 644 packaging/amuos-controlplane.service /etc/systemd/system/amuos-controlplane.service
install -d -m 700 -o amuos -g amuos /var/lib/amuos
systemctl daemon-reload
as_amuos /usr/local/bin/amuos-controlplane --db "$db" --bootstrap-file /var/lib/amuos/setup-token
systemctl enable --now amuos-controlplane
wait_ready
cd "$work"

code=$(api -o /dev/null -w '%{http_code}' -X POST "$origin/api/v1/setup" \
  -d "{\"token\":\"$(tr -d '\n' < /var/lib/amuos/setup-token)\",\"username\":\"owner\",\"password\":\"$password\"}")
[ "$code" = 201 ] || fail "first setup ($code)"
rm /var/lib/amuos/setup-token
csrf=$(login)
[ -n "$csrf" ] || fail "login"
add_host "$csrf" before-update

# The service runs as the dedicated user and keeps the state private.
[ "$(ps -o user= -p "$(systemctl show -P MainPID amuos-controlplane)")" = amuos ] || fail "service user"
[ "$(stat -c '%U %a' /var/lib/amuos)" = "amuos 700" ] || fail "state directory permissions"
[ "$(stat -c '%U %a' "$db")" = "amuos 600" ] || fail "database permissions"
[ "$(stat -c '%U %a' /etc/amuos/tls/server-key.pem)" = "root 600" ] || fail "TLS key permissions"

systemctl restart amuos-controlplane
wait_ready
csrf=$(login)
api "$origin/api/v1/hosts" | grep -q '"name":"before-update"' || fail "host after service restart"

# Update without reinstalling (INSTALL.md "更新").
install -d -m 700 /root/amuos-recovery
/usr/local/bin/amuos-controlplane --generate-recovery-key /root/amuos-recovery/recovery.key > /root/amuos-recovery/recipient
install -d -m 700 -o amuos -g amuos /var/backups/amuos
systemctl stop amuos-controlplane
. /etc/amuos/controlplane.env
as_amuos /usr/local/bin/amuos-controlplane --db "$db" \
  --backup-out /var/backups/amuos/pre-update.age --recipient "$(cat /root/amuos-recovery/recipient)" \
  --addr "$AMUOS_ADDR" --origin "$AMUOS_ORIGIN" > "$work/backup.json"
install -m 700 -o amuos -g amuos /usr/local/bin/amuos-controlplane /var/backups/amuos/amuos-controlplane.previous
install -m 755 "$next" "$work/amuos-controlplane.new"
as_amuos "$work/amuos-controlplane.new" --db "$db" --check-db > "$work/check.json" || fail "update check"
grep -q '"migration_required":false' "$work/check.json" || fail "unexpected migration: $(cat "$work/check.json")"
install -m 755 "$work/amuos-controlplane.new" /usr/local/bin/amuos-controlplane
new_version="$(/usr/local/bin/amuos-controlplane --version)"
[ "$new_version" != "$old_version" ] || fail "update did not change the version"
systemctl start amuos-controlplane
wait_ready
csrf=$(login)
[ -n "$csrf" ] || fail "login after update"
api "$origin/api/v1/hosts" | grep -q '"name":"before-update"' || fail "host after update"
add_host "$csrf" after-update

# Roll back with the pre-update backup and its binary (INSTALL.md "切り戻し").
systemctl stop amuos-controlplane
install -m 755 /var/backups/amuos/amuos-controlplane.previous /usr/local/bin/amuos-controlplane
install -d -m 700 -o amuos -g amuos /run/amuos-restore
install -m 600 -o amuos -g amuos /root/amuos-recovery/recovery.key /run/amuos-restore/recovery.key
as_amuos /usr/local/bin/amuos-controlplane --db "$db" --restore-from /var/backups/amuos/pre-update.age \
  --recovery-key-file /run/amuos-restore/recovery.key > "$work/restore.json" || fail "restore"
rm -rf /run/amuos-restore
[ "$(/usr/local/bin/amuos-controlplane --version)" = "$old_version" ] || fail "rollback binary"
systemctl start amuos-controlplane
wait_ready
code=$(api -o /dev/null -w '%{http_code}' "$origin/api/v1/me")
[ "$code" = 401 ] || fail "session from before rollback still works ($code)"
csrf=$(login)
[ -n "$csrf" ] || fail "login after rollback"
hosts="$(api "$origin/api/v1/hosts")"
grep -q '"name":"before-update"' <<<"$hosts" || fail "host from before the update after rollback"
if grep -q '"name":"after-update"' <<<"$hosts"; then fail "change after the backup survived rollback"; fi
api "$origin/api/v1/audit" | grep -q 'restore.complete' || fail "restore audit"
[ "$(stat -c '%U' "$db")" = amuos ] || fail "database owner after restore"

# Removing the service keeps the state (INSTALL.md "アンインストール").
systemctl disable --now amuos-controlplane
rm /etc/systemd/system/amuos-controlplane.service /usr/local/bin/amuos-controlplane
systemctl daemon-reload
[ -f "$db" ] || fail "uninstall removed the database"

echo "PASS: systemd install, restart, update with check, rollback with backup, uninstall keeps state"
