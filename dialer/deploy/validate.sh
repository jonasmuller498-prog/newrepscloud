#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$root/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

mode="${1:---static}"
[[ "$mode" == --static || "$mode" == --production ]] || {
  echo "usage: $0 [--static|--production]" >&2
  exit 64
}
for command in kubectl kubeconform python3 go openssl; do
  command -v "$command" >/dev/null || {
    echo "$command is required" >&2
    exit 69
  }
done

manifests=()
render() {
  local name="$1" path="$2" output="$tmp/$1.yaml"
  kubectl kustomize "$path" >"$output"
  manifests+=("$output")
}

render root "$root"
render base "$root/base"
render monitoring "$root/optional/monitoring"
render backups "$root/optional/backups"

if [[ "$mode" == --production ]]; then
  production_root="$root"
  python3 "$root/scripts/check-production-inputs.py" \
    "$root/overlays/production/inputs"
else
  cp -a "$root" "$tmp/deploy"
  production_root="$tmp/deploy"
  bash "$production_root/tests/create-test-inputs.sh" \
    "$production_root/overlays/production/inputs"
  python3 "$production_root/scripts/check-production-inputs.py" \
    --allow-test-net "$production_root/overlays/production/inputs"
fi
render production "$production_root/overlays/production"
render production-backups "$production_root/overlays/production-backups"
echo "PASS: rendered every Kustomize package and overlay"

bash -n "$root/base/scripts/build-app.sh"
bash -n "$root/scripts/init-production-inputs.sh"
sh -n "$root/base/postgres/init-runtime.sh"
python3 - "$root/scripts/check-production-inputs.py" \
  "$root/scripts/check-live-ports.py" <<'PY'
import ast, pathlib, sys
for name in sys.argv[1:]:
    ast.parse(pathlib.Path(name).read_text(encoding="utf-8"), filename=name)
PY
if [[ -n "$(gofmt -d "$root/base/scripts/render-config.go")" ]]; then
  echo "render-config.go is not gofmt-clean" >&2
  gofmt -d "$root/base/scripts/render-config.go"
  exit 1
fi
go test "$root/base/scripts/render-config.go"
echo "PASS: helper scripts parse and renderer compiles"

PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover \
  -s "$root/tests" -p 'test_*.py' -v
echo "PASS: static and fail-closed production policy tests"

(cd "$repo/dialer/app" && go build -o "$tmp/dialer-app" .)
test -x "$tmp/dialer-app"
echo "PASS: application builds as one root package with go build ."

kubeconform -strict -summary -ignore-missing-schemas "${manifests[@]}"
echo "PASS: kubeconform validated every rendered package"

if kubectl get services --all-namespaces -o json \
  --request-timeout=5s >"$tmp/live-services.json" 2>/dev/null; then
  python3 "$root/scripts/check-live-ports.py" "$tmp/live-services.json"
else
  echo "SKIP: no readable live cluster; nodePort and healthCheckNodePort preflight not run"
fi

echo "All static validation passed; no cluster resources were changed."
