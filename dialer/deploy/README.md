# Voice dialer deployment

Kustomize assets for a new, isolated `voice-dialer` namespace on Rancher RKE2.
They do not adopt, patch, or depend on any existing workload. The engine is one
StatefulSet pod on node `runners`; it contains Asterisk 22.10.1 and the Go app.
PostgreSQL 17 and both persistent data sets use Longhorn `ReadWriteOnce`.

Nothing in this directory is an instruction to apply the placeholder base.
The base exists so review and CI can render it without credentials. It generates
no Secrets, so PostgreSQL and the engine cannot start from it. Its carrier
networks are TEST-NET ranges, the trunk is absent, `DIALING_ENABLED=false`, and
`CPS=0`.

## Public and private surfaces

- HTTPS: `dialer.playground.obvious.tech`, via the `nginx` Ingress class and
  `letsencrypt-prod` ClusterIssuer.
- SIP: `5.196.90.231:31100/UDP`, NodePort with source IP preservation.
- RTP: `5.196.90.231:32300-32499/UDP`, split across ten NodePort Services.
- ARI is loopback-only at `127.0.0.1:8088`; PostgreSQL is namespace-private.
- The API Service targets only `8080`. Metrics use a separate private Service
  on `9090` and are not routed by the Ingress.

The repository's listed SIP B2BUA uses `32061` and `32200-32219`; this
deployment's `31100` and `32300-32499` ranges do not overlap. Still run the
production validation before any apply: it checks live `nodePort` and
`healthCheckNodePort` claims whenever the current kubeconfig is readable.

## Runtime contract

The immutable source init container fetches one exact 40-character commit,
checks out that commit, changes to `dialer/app`, and runs `go build .`. The
result is mode `0555` on a shared `emptyDir`. The pinned distroless Debian
runtime contains CA certificates and timezone data and runs as UID/GID 1000.

The app receives `DATABASE_URL`, independent operator/approver tokens,
`PHONE_HASH_KEY`, `FIELD_ENCRYPTION_KEY`, `AUDIT_HMAC_KEY`, and the exact ARI
variables named in the app contract. Defaults are `DIALING_ENABLED=false`,
`CPS=0`, and `MAX_CONCURRENCY=20`.

The app originates `PJSIP/%s@outbound` directly into Stasis and plays
`sound:campaigns/<sha>` through ARI. There is no Local-channel or playback
dialplan. Uploaded SHA-named WAV files are mode `0640` on the shared media PVC;
the aligned Asterisk process reads them at
`/var/lib/asterisk/sounds/campaigns`. The only dialplan context rejects calls.

## Render and validate

```bash
cd dialer/deploy
./validate.sh --static
```

Static validation renders every package and overlay with ephemeral nonproduction
inputs, runs `kubeconform`, verifies `go build .`, enforces the app/deploy
contract and file limits, and never calls `kubectl apply`.

## Create production inputs safely

Review an immutable, signed or otherwise trusted public repository commit.
Obtain exact carrier signaling/media CIDRs before running:

```bash
cd dialer/deploy
umask 077
./scripts/init-production-inputs.sh
./validate.sh --production
```

The helper prompts without placing passwords or tokens in shell history,
generates separate admin/runtime database credentials and independent 256-bit
application values, writes mode-`0600` files
below `overlays/production/inputs/`, and never prints secret values. That
directory ignores all `*.env` files. Do not use `--from-literal` with secrets
on a shared shell because command arguments can be observable.

The helper deliberately creates only `DIALER_TRUNK_ENABLED=false`; it never
guesses an SBC, account, auth mode, or caller ID. Add exact trunk values to the
ignored input only after provider review. Caller ID is supplied per attempt and
must be an app-authorized US E.164 identity.

## Required reviews before deployment

1. Follow [SECURITY.md](SECURITY.md), especially provider CIDRs/SIP target and
   the verified RKE2 ingress/DNS labels.
2. Follow [RUNBOOK.md](RUNBOOK.md) for preflight, media staging, migrations,
   rollout, smoke checks, and the two-step scheduler/trunk enablement.
3. Follow [BACKUP_RESTORE.md](BACKUP_RESTORE.md) before enabling backups.
4. Add [optional monitoring](MONITORING.md) only when Prometheus Operator CRDs
   exist.

The app must implement `/health/live` and `/health/ready` on `8080`, `/metrics`
only on `9090`, graceful SIGTERM, PostgreSQL migrations, immutable media
verification, ARI playback/DTMF handling, and durable opt-out persistence.
