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

read -r -p "Distinct production HTTPS hostname: " public_hostname
read -r -p "Dialer public IPv4 used for SIP and RTP: " public_ipv4
read -r -p "Primary carrier signaling IPv4 /32: " signal_cidr_primary
read -r -p "Secondary carrier signaling IPv4 /32: " signal_cidr_secondary
read -r -p "Exact carrier media CIDR: " media_cidr
python3 - "$public_hostname" "$public_ipv4" \
  "$signal_cidr_primary" "$signal_cidr_secondary" "$media_cidr" <<'PY'
import ipaddress, re, sys
hostname = sys.argv[1]
if (not re.fullmatch(r"(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}", hostname)
        or hostname == "dialer.playground.obvious.tech"
        or hostname.endswith((".invalid", ".example", ".test", ".localhost"))):
    raise SystemExit("production hostname must be a distinct lowercase public FQDN")
public_ip = ipaddress.ip_address(sys.argv[2])
signals = [ipaddress.ip_network(value, strict=True) for value in sys.argv[3:5]]
media = ipaddress.ip_network(sys.argv[5], strict=True)
if public_ip.version != 4 or not public_ip.is_global:
    raise SystemExit("dialer public address must be a public IPv4")
if any(item.version != 4 or not item.is_global or item.prefixlen != 32 for item in signals):
    raise SystemExit("carrier signaling CIDRs must be distinct public IPv4 /32 networks")
if signals[0] == signals[1]:
    raise SystemExit("carrier signaling CIDRs must be distinct public IPv4 /32 networks")
if media.version != 4 or not media.is_global:
    raise SystemExit("carrier media CIDR must be a canonical public IPv4 network")
PY

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
  'ARI_APP=voice-dialer' 'ARI_DIAL_CONTEXT=dialer-outbound' \
  'DIALER_SOURCE_REPOSITORY=https://github.com/jonasmuller498-prog/newrepscloud.git' \
  "DIALER_SOURCE_REF=${source_ref,,}" >"$out/runtime.env"
printf '%s\n' "PUBLIC_HOSTNAME=$public_hostname" \
  "DIALER_PUBLIC_IPV4=$public_ipv4" \
  "TRUNK_SIGNAL_CIDR_PRIMARY=$signal_cidr_primary" \
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
