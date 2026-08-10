#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
out="$root/overlays/production/inputs"
files=(runtime.env network.env postgres.env app.env ari.env trunk.env)
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
read -r -p "Campaign WAV SHA-256 (64 hex): " campaign_sha
[[ "$campaign_sha" =~ ^[0-9a-fA-F]{64}$ ]] || {
  echo "invalid campaign digest" >&2
  exit 64
}
read -r -p "Ingress controller namespace [ingress-nginx]: " ingress_ns
ingress_ns="${ingress_ns:-ingress-nginx}"
read -r -p "Provider signaling CIDR (exact /32 preferred): " signal_cidr
read -r -p "Provider media CIDR: " media_cidr

read -r -p "PostgreSQL user [dialer]: " pg_user
pg_user="${pg_user:-dialer}"
read -r -p "PostgreSQL database [dialer]: " pg_db
pg_db="${pg_db:-dialer}"
pg_password="$(openssl rand -hex 32)"
api_token="$(openssl rand -hex 32)"
origin_token="$(openssl rand -hex 32)"
ari_password="$(openssl rand -hex 32)"

read -r -p "Exact provider SIP URI (sip:[account@]sbc:port): " sip_uri
[[ "$sip_uri" =~ ^sip:([A-Za-z0-9+_.%-]+@)?[A-Za-z0-9.-]+:[0-9]{2,5}$ ]] || {
  echo "invalid SIP URI" >&2
  exit 64
}
read -r -p "Trunk auth mode (digest or ip): " auth_mode
[[ "$auth_mode" == digest || "$auth_mode" == ip ]] || {
  echo "invalid auth mode" >&2
  exit 64
}
read -r -p "Trunk username (empty only for IP auth): " trunk_user
read -r -s -p "Trunk password (empty only for IP auth): " trunk_password
printf '\n'
if [[ "$auth_mode" == digest && ( -z "$trunk_user" || -z "$trunk_password" ) ]]; then
  echo "digest mode requires username and password" >&2
  exit 64
fi
read -r -p "Trunk realm [*]: " trunk_realm
trunk_realm="${trunk_realm:-*}"
read -r -p "Approved US E.164 caller ID (+1 plus ten digits): " caller_id
[[ "$caller_id" =~ ^\+1[0-9]{10}$ ]] || {
  echo "invalid caller ID" >&2
  exit 64
}

printf '%s\n' \
  'DIALING_ENABLED=false' 'CPS=0' 'MAX_CONCURRENCY=20' \
  'HARD_MAX_CONCURRENCY=100' 'AUTH_REQUIRED=true' \
  'HTTP_ADDR=:8080' 'METRICS_ADDR=:9090' \
  'ARI_URL=http://127.0.0.1:8088/ari' \
  'PGHOST=postgres' 'PGPORT=5432' 'PGSSLMODE=disable' \
  'CAMPAIGN_WAV_PATH=/media/campaign.wav' \
  "CAMPAIGN_WAV_SHA256=$campaign_sha" \
  'DIALER_SOURCE_REPOSITORY=https://github.com/jonasmuller498-prog/newrepscloud.git' \
  "DIALER_SOURCE_REF=$source_ref" \
  'DIALER_BUILD_PACKAGE=./dialer/app/cmd/dialer' >"$out/runtime.env"
printf '%s\n' "INGRESS_NAMESPACE=$ingress_ns" \
  "TRUNK_SIGNAL_CIDR=$signal_cidr" "TRUNK_MEDIA_CIDR=$media_cidr" \
  >"$out/network.env"
printf '%s\n' "POSTGRES_USER=$pg_user" "POSTGRES_PASSWORD=$pg_password" \
  "POSTGRES_DB=$pg_db" >"$out/postgres.env"
printf '%s\n' "DIALER_API_TOKEN=$api_token" \
  "DIALER_ORIGIN_TOKEN=$origin_token" >"$out/app.env"
printf '%s\n' 'ARI_USERNAME=dialer_app' \
  "ARI_PASSWORD=$ari_password" >"$out/ari.env"
printf '%s\n' 'DIALER_TRUNK_ENABLED=false' \
  "DIALER_TRUNK_AUTH_MODE=$auth_mode" "DIALER_TRUNK_SIP_URI=$sip_uri" \
  "DIALER_TRUNK_USERNAME=$trunk_user" "DIALER_TRUNK_PASSWORD=$trunk_password" \
  "DIALER_TRUNK_REALM=$trunk_realm" "DIALER_CALLER_ID=$caller_id" \
  >"$out/trunk.env"
chmod 0600 "$out"/*.env
echo "Created six ignored mode-0600 inputs. Trunk and scheduler remain disabled."
