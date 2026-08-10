#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

command -v kubectl >/dev/null || {
  echo "kubectl is required" >&2
  exit 69
}

kubectl kustomize "$root" >"$tmp/base.yaml"
kubectl kustomize "$root/optional/monitoring" >"$tmp/monitoring.yaml"
kubectl kustomize "$root/optional/backups" >"$tmp/backups.yaml"
echo "PASS: kubectl kustomize rendered base and optional packages"

python3 "$root/tests/test_static.py"
echo "PASS: static deployment policy tests"

bash -n "$root/scripts/build-app.sh"
bash -n "$root/scripts/init-production-inputs.sh"
if command -v gofmt >/dev/null; then
  if [[ -n "$(gofmt -d "$root/scripts/render-config.go")" ]]; then
    echo "render-config.go is not gofmt-clean" >&2
    gofmt -d "$root/scripts/render-config.go"
    exit 1
  fi
fi
if command -v go >/dev/null; then
  go test "$root/scripts/render-config.go"
fi
echo "PASS: helper scripts parse and renderer compiles"

if command -v kubeconform >/dev/null; then
  kubeconform -strict -summary -ignore-missing-schemas \
    "$tmp/base.yaml" "$tmp/monitoring.yaml" "$tmp/backups.yaml"
  echo "PASS: kubeconform schema validation"
elif command -v kubeval >/dev/null; then
  kubeval --strict --ignore-missing-schemas \
    "$tmp/base.yaml" "$tmp/monitoring.yaml" "$tmp/backups.yaml"
  echo "PASS: kubeval schema validation"
else
  for manifest in "$tmp/base.yaml" "$tmp/monitoring.yaml" "$tmp/backups.yaml"; do
    kubectl apply --dry-run=client --validate=false -f "$manifest" >/dev/null
  done
  echo "PASS: kubectl client-side object decoding (install kubeconform for schemas)"
fi

echo "All static validation passed; no cluster resources were changed."
