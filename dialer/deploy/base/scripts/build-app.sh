#!/usr/bin/env bash
set -Eeuo pipefail
umask 022

: "${DIALER_SOURCE_REPOSITORY:?DIALER_SOURCE_REPOSITORY is required}"
: "${DIALER_SOURCE_REF:?DIALER_SOURCE_REF is required}"
: "${DIALER_BUILD_PACKAGE:?DIALER_BUILD_PACKAGE is required}"

if [[ ! "$DIALER_SOURCE_REPOSITORY" =~ ^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\.git$ ]]; then
  echo "source repository must be a public HTTPS GitHub repository" >&2
  exit 64
fi
if [[ ! "$DIALER_SOURCE_REF" =~ ^[0-9a-fA-F]{40}$ ]] ||
   [[ "$DIALER_SOURCE_REF" == 0000000000000000000000000000000000000000 ]]; then
  echo "DIALER_SOURCE_REF must be an immutable 40-character commit SHA" >&2
  exit 64
fi
if [[ ! "$DIALER_BUILD_PACKAGE" =~ ^\./dialer/app/[A-Za-z0-9_./-]+$ ]]; then
  echo "DIALER_BUILD_PACKAGE must stay below ./dialer/app" >&2
  exit 64
fi

rm -rf /workspace/source
mkdir -p /workspace/source
cd /workspace/source
git init --quiet
git remote add origin "$DIALER_SOURCE_REPOSITORY"
GIT_TERMINAL_PROMPT=0 git fetch --quiet --depth=1 origin "$DIALER_SOURCE_REF"
git checkout --quiet --detach FETCH_HEAD

resolved="$(git rev-parse HEAD)"
if [[ "${resolved,,}" != "${DIALER_SOURCE_REF,,}" ]]; then
  echo "fetched commit does not match DIALER_SOURCE_REF" >&2
  exit 65
fi

export CGO_ENABLED=0 GOFLAGS="-mod=readonly -trimpath" GOTOOLCHAIN=local
go build -buildvcs=true -ldflags="-s -w" -o /app-bin/dialer.tmp "$DIALER_BUILD_PACKAGE"
chmod 0555 /app-bin/dialer.tmp
mv -f /app-bin/dialer.tmp /app-bin/dialer
