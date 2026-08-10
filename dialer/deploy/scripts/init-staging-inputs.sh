#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
out="$root/overlays/staging-disabled/inputs"
files=(runtime.env safety.env postgres-admin.env postgres-runtime.env app.env ari.env trunk.env)

fail() {
  echo "ERROR: $*" >&2
  exit 64
}

(( $# <= 1 )) || fail "usage: $0 [40-character-source-commit]"
if (( $# == 1 )); then
  source_ref="$1"
elif [[ -t 0 ]]; then
  read -r -p "Immutable 40-character source commit: " source_ref
else
  fail "noninteractive use requires the source commit argument"
fi
[[ "$source_ref" =~ ^[0-9a-fA-F]{40}$ ]] || fail "invalid source commit"
[[ "$source_ref" != 0000000000000000000000000000000000000000 ]] ||
  fail "source commit must not be all zeroes"

for file in "${files[@]}"; do
  [[ ! -e "$out/$file" ]] || {
    echo "refusing to overwrite $out/$file" >&2
    exit 73
  }
done

stage="$(mktemp -d "$out/.init.XXXXXX")"
trap 'rm -rf "$stage"' EXIT
admin_password="$(openssl rand -hex 32)"
runtime_password="$(openssl rand -hex 32)"

printf '%s\n' \
  'DIALING_ENABLED=false' 'CPS=0' 'MAX_CONCURRENCY=20' \
  'HTTP_ADDR=:8080' 'METRICS_ADDR=:9090' \
  'MEDIA_DIR=/media' 'EVENT_JOURNAL_DIR=/media/ari-journal' \
  'ARI_URL=http://127.0.0.1:8088/ari' \
  'ARI_APP=voice-dialer' 'ARI_ENDPOINT=outbound' \
  'DIALER_SOURCE_REPOSITORY=https://github.com/jonasmuller498-prog/newrepscloud.git' \
  "DIALER_SOURCE_REF=${source_ref,,}" >"$stage/runtime.env"
printf '%s\n' \
  'BACKUP_STATUS=unconfigured-suspended' \
  'BACKUP_DESTINATION=UNCONFIGURED' \
  'BACKUP_ACKNOWLEDGED=false' >"$stage/safety.env"
printf '%s\n' 'POSTGRES_USER=postgres' \
  "POSTGRES_PASSWORD=$admin_password" \
  'POSTGRES_DB=dialer_staging' >"$stage/postgres-admin.env"
printf '%s\n' 'DB_USER=dialer_staging_app' \
  "DB_PASSWORD=$runtime_password" 'DB_NAME=dialer_staging' \
  "DATABASE_URL=postgres://dialer_staging_app:$runtime_password@postgres:5432/dialer_staging?sslmode=disable" \
  >"$stage/postgres-runtime.env"
printf '%s\n' \
  "OPERATOR_API_TOKEN=$(openssl rand -hex 32)" \
  "APPROVER_API_TOKEN=$(openssl rand -hex 32)" \
  "PHONE_HASH_KEY=$(openssl rand -hex 32)" \
  "FIELD_ENCRYPTION_KEY=$(openssl rand -hex 32)" \
  "AUDIT_HMAC_KEY=$(openssl rand -hex 32)" >"$stage/app.env"
printf '%s\n' 'ARI_USER=dialer_ari' \
  "ARI_PASSWORD=$(openssl rand -hex 32)" >"$stage/ari.env"
printf '%s\n' 'DIALER_TRUNK_ENABLED=false' >"$stage/trunk.env"

chmod 0600 "$stage"/*.env
for file in "${files[@]}"; do
  mv "$stage/$file" "$out/$file"
done
rmdir "$stage"
trap - EXIT
echo "Created seven ignored mode-0600 inputs; carrier routing and dialing remain absent."
