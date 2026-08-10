#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$root/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

mode="${1:---static}"
case "$mode" in
  --static | --production | --staging-disabled) ;;
  *)
    echo "usage: $0 [--static|--production|--staging-disabled]" >&2
    exit 64
    ;;
esac
for command in git kubectl kubeconform python3 go openssl; do
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

render_staging() {
  local deploy_root="$1"
  python3 "$deploy_root/scripts/check-staging-inputs.py" \
    "$deploy_root/overlays/staging-disabled/inputs"
  render staging "$deploy_root/overlays/staging-disabled"
  kubectl create --dry-run=client --validate=false \
    -f "$tmp/staging.yaml" -o json >"$tmp/staging.json"
  python3 "$deploy_root/scripts/check-staging-render.py" "$tmp/staging.json"
}

render root "$root"
render base "$root/base"
render monitoring "$root/optional/monitoring"
render backups "$root/optional/backups"

case "$mode" in
  --production)
    python3 "$root/scripts/check-production-inputs.py" \
      "$root/overlays/production/inputs"
    render production "$root/overlays/production"
    render production-backups "$root/overlays/production-backups"
    ;;
  --staging-disabled)
    render_staging "$root"
    ;;
  --static)
    cp -a "$root" "$tmp/deploy"
    deploy_copy="$tmp/deploy"
    rm -f "$deploy_copy/overlays/production/inputs/"*.env
    rm -f "$deploy_copy/overlays/staging-disabled/inputs/"*.env
    bash "$deploy_copy/tests/create-test-inputs.sh" \
      "$deploy_copy/overlays/production/inputs"
    python3 "$deploy_copy/scripts/check-production-inputs.py" \
      --allow-test-net "$deploy_copy/overlays/production/inputs"
    render production "$deploy_copy/overlays/production"
    render production-backups "$deploy_copy/overlays/production-backups"
    source_ref="$(git -C "$repo" rev-parse HEAD)"
    bash "$deploy_copy/scripts/init-staging-inputs.sh" "$source_ref"
    render_staging "$deploy_copy"
    ;;
esac
echo "PASS: rendered requested Kustomize packages and overlays"

bash -n "$root/base/scripts/build-app.sh"
bash -n "$root/scripts/init-production-inputs.sh"
bash -n "$root/scripts/init-staging-inputs.sh"
sh -n "$root/base/postgres/init-runtime.sh"
python3 - "$root/scripts/check-production-inputs.py" \
  "$root/scripts/check-staging-inputs.py" \
  "$root/scripts/check-staging-render.py" "$root/scripts/check-live-ports.py" <<'PY'
import ast, pathlib, sys
for name in sys.argv[1:]:
    ast.parse(pathlib.Path(name).read_text(encoding="utf-8"), filename=name)
PY
if [[ -n "$(gofmt -d "$root/base/scripts/render-config.go")" ]]; then
  echo "render-config.go is not gofmt-clean" >&2
  gofmt -d "$root/base/scripts/render-config.go"
  exit 1
fi
go test "$root/base/scripts/render-config.go" \
  "$root/base/scripts/render-config_test.go"
echo "PASS: helper scripts parse and renderer compiles"

PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover \
  -s "$root/tests" -p 'test_*.py' -v
echo "PASS: static and fail-closed overlay policy tests"

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

echo "All ${mode#--} validation passed; no cluster resources were changed."
