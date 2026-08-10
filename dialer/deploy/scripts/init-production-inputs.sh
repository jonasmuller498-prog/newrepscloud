#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
out="$root/overlays/production/inputs"
files=(runtime.env network.env safety.env postgres-admin.env postgres-runtime.env app.env ari.env trunk.env)
for file in "${files[@]}"; do
  if [[ -e "$out/$file" ]]; then
    echo "refusing to overwrite $out/$file" >&2
    exit 73
  fi
done

read -r -p "Reviewed 40-character source commit SHA: " source_ref
[[ "$source_ref" =~ ^[0-9a-fA-F]{40}$ ]] || {
  echo "invalid source commit SHA" >&2
  exit 64
}

read -r -p "Primary carrier signaling CIDR (/32 preferred): " signal_cidr_primary
read -r -p "Secondary carrier signaling CIDR (/32 preferred): " signal_cidr_secondary
read -r -p "Exact carrier media CIDR: " media_cidr
for cidr in "$signal_cidr_primary" "$signal_cidr_secondary" "$media_cidr"; do
  python3 - "$cidr" <<'PY'
import ipaddress, sys
network = ipaddress.ip_network(sys.argv[1], strict=True)
if network.version != 4 or not network.is_global:
    raise SystemExit("carrier CIDRs must be canonical public IPv4 networks")
PY
done

read -r -p "PostgreSQL database name: " pg_db
read -r -p "PostgreSQL runtime role: " runtime_user
[[ "$pg_db" =~ ^[A-Za-z0-9_.-]+$ && "$runtime_user" =~ ^[A-Za-z0-9_.-]+$ ]] || {
  echo "database and role names contain unsupported characters" >&2
  exit 64
}
[[ "$runtime_user" != postgres ]] || {
  echo "runtime role must differ from the postgres superuser" >&2
  exit 64
}
admin_password="$(openssl rand -hex 32)"
runtime_password="$(openssl rand -hex 32)"
operator_token="$(openssl rand -hex 32)"
approver_token="$(openssl rand -hex 32)"
phone_hash_key="$(openssl rand -hex 32)"
field_encryption_key="$(openssl rand -hex 32)"
audit_hmac_key="$(openssl rand -hex 32)"
ari_password="$(openssl rand -hex 32)"

read -r -p "Non-secret external backup target ID (empty if unconfigured): " backup_destination
backup_status=unconfigured-suspended
backup_acknowledged=false
if [[ -n "$backup_destination" ]]; then
  read -r -p "Type BACKUPS_CONFIGURED after restore testing: " backup_ack
  if [[ "$backup_ack" == BACKUPS_CONFIGURED ]]; then
    backup_status=configured-suspended
    backup_acknowledged=true
  fi
fi

printf '%s\n' \
  'DIALING_ENABLED=false' 'CPS=0' 'MAX_CONCURRENCY=20' \
  'HTTP_ADDR=:8080' 'METRICS_ADDR=:9090' \
  'MEDIA_DIR=/media' 'EVENT_JOURNAL_DIR=/media/ari-journal' \
  'ARI_URL=http://127.0.0.1:8088/ari' \
  'ARI_APP=voice-dialer' 'ARI_ENDPOINT=outbound' \
  'DIALER_SOURCE_REPOSITORY=https://github.com/jonasmuller498-prog/newrepscloud.git' \
  "DIALER_SOURCE_REF=${source_ref,,}" >"$out/runtime.env"
printf '%s\n' "TRUNK_SIGNAL_CIDR_PRIMARY=$signal_cidr_primary" \
  "TRUNK_SIGNAL_CIDR_SECONDARY=$signal_cidr_secondary" \
  "TRUNK_MEDIA_CIDR=$media_cidr" >"$out/network.env"
printf '%s\n' "BACKUP_STATUS=$backup_status" \
  "BACKUP_DESTINATION=${backup_destination:-UNCONFIGURED}" \
  "BACKUP_ACKNOWLEDGED=$backup_acknowledged" >"$out/safety.env"
printf '%s\n' 'POSTGRES_USER=postgres' \
  "POSTGRES_PASSWORD=$admin_password" "POSTGRES_DB=$pg_db" \
  >"$out/postgres-admin.env"
printf '%s\n' "DB_USER=$runtime_user" "DB_PASSWORD=$runtime_password" \
  "DB_NAME=$pg_db" \
  "DATABASE_URL=postgres://$runtime_user:$runtime_password@postgres:5432/$pg_db?sslmode=disable" \
  >"$out/postgres-runtime.env"
printf '%s\n' "OPERATOR_API_TOKEN=$operator_token" \
  "APPROVER_API_TOKEN=$approver_token" "PHONE_HASH_KEY=$phone_hash_key" \
  "FIELD_ENCRYPTION_KEY=$field_encryption_key" \
  "AUDIT_HMAC_KEY=$audit_hmac_key" >"$out/app.env"
printf '%s\n' 'ARI_USER=dialer_ari' \
  "ARI_PASSWORD=$ari_password" >"$out/ari.env"
printf '%s\n' 'DIALER_TRUNK_ENABLED=false' >"$out/trunk.env"
chmod 0600 "$out"/*.env
echo "Created eight ignored mode-0600 inputs. Trunk and scheduler remain disabled."
