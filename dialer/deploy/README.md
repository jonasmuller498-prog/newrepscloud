# Voice dialer deployment

Kustomize assets for a new, isolated `voice-dialer` namespace on Rancher RKE2.
They do not adopt, patch, or depend on any existing workload. The engine is one
StatefulSet pod on node `runners`; it contains Asterisk 22.10.1 and the Go app.
PostgreSQL 17 and both persistent data sets use Longhorn `ReadWriteOnce`.

Nothing in this directory is an instruction to apply the placeholder base.
The base exists so review and CI can render it without credentials. Its Secrets
contain only `REQUIRED_*` values, its provider networks are TEST-NET ranges, the
trunk is absent, `DIALING_ENABLED=false`, and `CPS=0`.

## Public and private surfaces

- HTTPS: `dialer.playground.obvious.tech`, via the `nginx` Ingress class and
  `letsencrypt-prod` ClusterIssuer.
- SIP: `5.196.90.231:31100/UDP`, NodePort with source IP preservation.
- RTP: `5.196.90.231:31500-31699/UDP`, split across ten NodePort Services.
- PostgreSQL, ARI (`127.0.0.1:8088`), and metrics are never public Services.
- API token authentication is mandatory (`AUTH_REQUIRED=true`).

The repository's listed SIP B2BUA uses `32061` and `32200-32219`; this
deployment's `31100` and `31500-31699` ranges do not overlap. Still query the
live cluster before any apply because untracked NodePorts may exist:

```bash
kubectl get services -A -o jsonpath='{range .items[*]}{.metadata.namespace}{"/"}{.metadata.name}{" "}{range .spec.ports[*]}{.nodePort}{" "}{end}{"\n"}{end}'
```

## Render and validate

```bash
cd dialer/deploy
kubectl kustomize . >/tmp/voice-dialer-placeholder.yaml
./validate.sh
```

`validate.sh` renders the base and optional packages, runs static policy tests,
and uses `kubeconform`, `kubeval`, or client-side Kubernetes decoding when the
stronger schema tools are unavailable.

## Create production inputs safely

Review an immutable, signed or otherwise trusted public repository commit and
the campaign WAV digest first. Then run:

```bash
cd dialer/deploy
umask 077
./scripts/init-production-inputs.sh
kubectl kustomize overlays/production >/tmp/voice-dialer-production.yaml
```

The helper prompts without placing passwords or tokens in shell history,
generates independent 256-bit values with OpenSSL, writes mode-`0600` files
below `overlays/production/inputs/`, and never prints secret values. That
directory ignores all `*.env` files. Do not use `--from-literal` with secrets
on a shared shell because command arguments can be observable.

The init container accepts only a 40-character `DIALER_SOURCE_REF`, fetches
that exact commit from the public HTTPS repository, verifies `HEAD`, and builds
`./dialer/app/cmd/dialer` with `CGO_ENABLED=0`, `-mod=readonly`, and a pinned Go
builder. The app executes from a pinned non-root distroless Debian runtime, so
no private image registry is required.

## Required reviews before deployment

1. Follow [SECURITY.md](SECURITY.md), especially provider CIDRs/SIP target and
   RKE2 ingress/DNS labels.
2. Follow [RUNBOOK.md](RUNBOOK.md) for preflight, media staging, migrations,
   rollout, smoke checks, and the two-step scheduler/trunk enablement.
3. Follow [BACKUP_RESTORE.md](BACKUP_RESTORE.md) before enabling backups.
4. Add [optional monitoring](MONITORING.md) only when Prometheus Operator CRDs
   exist.

The app must implement `/healthz`, `/readyz`, `/metrics`, graceful SIGTERM,
the documented metric contract, token rejection by default, PostgreSQL
migrations, immutable media verification, and persistence of `DialerOptOut`
events before operators enable dialing.
