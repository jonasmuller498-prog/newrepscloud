#!/bin/sh
set -eu

: "${POSTGRES_USER:?POSTGRES_USER is required}"
: "${POSTGRES_DB:?POSTGRES_DB is required}"
: "${DB_USER:?DB_USER is required}"
: "${DB_PASSWORD:?DB_PASSWORD is required}"
: "${DB_NAME:?DB_NAME is required}"

case "$DB_USER" in
  "" | *[!A-Za-z0-9_.-]*) echo "invalid DB_USER" >&2; exit 64 ;;
esac
case "$DB_NAME" in
  "" | *[!A-Za-z0-9_.-]*) echo "invalid DB_NAME" >&2; exit 64 ;;
esac
test "$DB_NAME" = "$POSTGRES_DB" || {
  echo "DB_NAME must equal POSTGRES_DB" >&2
  exit 64
}
test "$DB_USER" != "$POSTGRES_USER" || {
  echo "runtime and PostgreSQL superuser must differ" >&2
  exit 64
}

psql --set=ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  --set=runtime_user="$DB_USER" --set=runtime_password="$DB_PASSWORD" \
  --set=runtime_db="$DB_NAME" <<'SQL'
CREATE ROLE :"runtime_user"
  LOGIN PASSWORD :'runtime_password'
  NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
GRANT CONNECT, TEMPORARY ON DATABASE :"runtime_db" TO :"runtime_user";
GRANT USAGE, CREATE ON SCHEMA public TO :"runtime_user";
SQL
