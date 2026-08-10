#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

out="${1:?output directory is required}"
mkdir -p "$out"
admin_password="$(openssl rand -hex 32)"
runtime_password="$(openssl rand -hex 32)"

cat >"$out/runtime.env" <<'EOF'
DIALING_ENABLED=false
CPS=0
MAX_CONCURRENCY=20
HTTP_ADDR=:8080
METRICS_ADDR=:9090
MEDIA_DIR=/media
EVENT_JOURNAL_DIR=/media/ari-journal
ARI_URL=http://127.0.0.1:8088/ari
ARI_APP=voice-dialer
ARI_DIAL_CONTEXT=dialer-outbound
DIALER_SOURCE_REPOSITORY=https://github.com/example/repository.git
DIALER_SOURCE_REF=1111111111111111111111111111111111111111
EOF
cat >"$out/network.env" <<'EOF'
TRUNK_SIGNAL_CIDR_PRIMARY=192.0.2.10/32
TRUNK_SIGNAL_CIDR_SECONDARY=192.0.2.11/32
TRUNK_MEDIA_CIDR=198.51.100.0/24
EOF
cat >"$out/safety.env" <<'EOF'
BACKUP_STATUS=unconfigured-suspended
BACKUP_DESTINATION=UNCONFIGURED
BACKUP_ACKNOWLEDGED=false
EOF
cat >"$out/postgres-admin.env" <<EOF
POSTGRES_USER=postgres
POSTGRES_PASSWORD=$admin_password
POSTGRES_DB=dialer_test
EOF
cat >"$out/postgres-runtime.env" <<EOF
DB_USER=dialer_test_app
DB_PASSWORD=$runtime_password
DB_NAME=dialer_test
DATABASE_URL=postgres://dialer_test_app:$runtime_password@postgres:5432/dialer_test?sslmode=disable
EOF
cat >"$out/app.env" <<EOF
OPERATOR_API_TOKEN=$(openssl rand -hex 32)
APPROVER_API_TOKEN=$(openssl rand -hex 32)
PHONE_HASH_KEY=$(openssl rand -hex 32)
FIELD_ENCRYPTION_KEY=$(openssl rand -hex 32)
AUDIT_HMAC_KEY=$(openssl rand -hex 32)
EOF
cat >"$out/ari.env" <<EOF
ARI_USER=dialer_ari
ARI_PASSWORD=$(openssl rand -hex 32)
EOF
printf 'DIALER_TRUNK_ENABLED=false\n' >"$out/trunk.env"
chmod 0600 "$out"/*.env
