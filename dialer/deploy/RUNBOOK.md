# Deployment and operations runbook
All commands are operator procedures; these assets have not been deployed.
Use a change window and retain `DIALING_ENABLED=false`, `CPS=0`, and
`DIALER_TRUNK_ENABLED=false` through all initial checks.
## Carrier-free staging-disabled rollout
This is the only apply path before carrier details exist:
```bash
cd dialer/deploy
./scripts/init-staging-inputs.sh <exact-40-character-source-commit>
./validate.sh --staging-disabled
kubectl diff -k overlays/staging-disabled
kubectl apply -k overlays/staging-disabled
kubectl -n voice-dialer rollout status statefulset/postgres --timeout=10m
kubectl -n voice-dialer rollout status statefulset/dialer-engine --timeout=15m
curl --fail --show-error https://dialer.playground.obvious.tech/health/live
curl --fail --show-error https://dialer.playground.obvious.tech/health/ready
kubectl -n voice-dialer exec dialer-engine-0 -c asterisk -- \
  asterisk -C /config/asterisk.conf -rx 'pjsip show endpoints'
```
The last command must report no `outbound` endpoint. Services must all be
`ClusterIP`; ARI remains loopback-only and metrics remain off the Ingress.
Retrieve only the active token keys into shell variables, never the whole
Secret:
```bash
APP_SECRET="$(kubectl -n voice-dialer get statefulset dialer-engine \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="app")].envFrom[1].secretRef.name}')"
OPERATOR_TOKEN="$(kubectl -n voice-dialer get secret "$APP_SECRET" \
  -o go-template='{{index .data "OPERATOR_API_TOKEN" | base64decode}}')"
APPROVER_TOKEN="$(kubectl -n voice-dialer get secret "$APP_SECRET" \
  -o go-template='{{index .data "APPROVER_API_TOKEN" | base64decode}}')"
curl --fail --show-error -H "Authorization: Bearer $OPERATOR_TOKEN" \
  https://dialer.playground.obvious.tech/api/v1/status
unset OPERATOR_TOKEN APPROVER_TOKEN APP_SECRET
```
Run integration tests only against a disposable database, never
`dialer_staging`. From `dialer/deploy`, with local Go available:
```bash
TEST_DB="dialer_it_$(date -u +%Y%m%d%H%M%S)"
kubectl -n voice-dialer exec postgres-0 -c postgres -- sh -ec \
  'createdb -U "$POSTGRES_USER" -O "$DB_USER" "$1"' sh "$TEST_DB"
kubectl -n voice-dialer port-forward service/postgres 15432:5432 >/tmp/dialer-pf.log 2>&1 &
PF_PID=$!
until (echo >/dev/tcp/127.0.0.1/15432) 2>/dev/null; do kill -0 "$PF_PID" || exit 1; sleep .2; done
RUNTIME_SECRET="$(kubectl -n voice-dialer get statefulset dialer-engine \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="app")].envFrom[3].secretRef.name}')"
DB_USER="$(kubectl -n voice-dialer get secret "$RUNTIME_SECRET" -o go-template='{{index .data "DB_USER" | base64decode}}')"
DB_PASSWORD="$(kubectl -n voice-dialer get secret "$RUNTIME_SECRET" -o go-template='{{index .data "DB_PASSWORD" | base64decode}}')"
(cd ../app && TEST_DATABASE_URL="postgres://${DB_USER}:${DB_PASSWORD}@127.0.0.1:15432/${TEST_DB}?sslmode=disable" go test -tags=integration ./...)
kill "$PF_PID"; wait "$PF_PID" 2>/dev/null || true
kubectl -n voice-dialer exec postgres-0 -c postgres -- sh -ec \
  'dropdb --force -U "$POSTGRES_USER" "$1"' sh "$TEST_DB"
unset DB_USER DB_PASSWORD RUNTIME_SECRET TEST_DB PF_PID
```
The staging-disabled overlay can never be patched to enable dialing. Do not add
carrier CIDRs, trunk fields, PJSIP transports, or public SIP/RTP Services; use
the production overlay later and complete all production gates.
## 1. Resolve inputs and RKE2 preflight
Do not start production rendering until owners provide all of:
- exact outbound SBC URI/port and explicit digest or IP authentication;
- carrier signaling and media CIDRs;
- approved CPS;
- authorized US E.164 caller IDs and STIR/SHAKEN treatment;
- final TTS/audio, campaign consent, calling-window, DNC, and legal inputs; and
- an external backup destination with a tested restore acknowledgement.
```bash
kubectl get node runners -o wide
kubectl get storageclass longhorn
kubectl get clusterissuer letsencrypt-prod
kubectl get ingressclass nginx
kubectl get ns kube-system cattle-monitoring-system --show-labels
kubectl -n kube-system get pods -l app.kubernetes.io/name=rke2-ingress-nginx --show-labels
kubectl -n kube-system get pods -l k8s-app=kube-dns --show-labels
kubectl get services -A -o json > /tmp/services-before.json
```
Confirm `runners` reports public IP `5.196.90.231`, is schedulable, and has
capacity for both engine containers and build init containers. Confirm no live
Service claims `31100` or `32300-32499` in either a `nodePort` or
`healthCheckNodePort`. The tracked root B2BUA range (`32061`,
`32200-32219`) does not conflict.
The app policy selects `kube-system` pods labeled
`app.kubernetes.io/name=rke2-ingress-nginx` and
`app.kubernetes.io/component=controller`. Verify those live labels, CoreDNS,
carrier CIDRs, exact account details, and upstream firewall.
`externalTrafficPolicy: Local` requires the engine to remain on `runners`.
## 2. Generate and validate fail-closed inputs
Run `scripts/init-production-inputs.sh`. It creates separate PostgreSQL
superuser/runtime credentials, app/ARI secrets, immutable source input, and a
disabled-only trunk input. Review non-secret settings, then:
```bash
umask 077
./validate.sh --production
kubectl kustomize overlays/production > /tmp/voice-dialer.yaml
rg 'REQUIRED_|UNCONFIGURED|192\.0\.2\.|198\.51\.100\.' /tmp/voice-dialer.yaml
```
The final search must have no matches before dialing is enabled. Validation
also rejects placeholder refs/credentials and queries live NodePort and health
check claims when kube access exists. Do not upload the rendered file to an
artifact service: it contains base64-encoded Secrets. Delete it securely under
the organization's workstation policy after review.
Verify the source commit exists in the public repository, contains the expected
`dialer/app` root package and committed `go.sum`, builds with `go build .`, and
has completed code/security review. The init build rejects a branch, tag,
non-40-character ref, changed module graph, or automatic toolchain download.
Before first apply, inspect retained claims:
```bash
kubectl -n voice-dialer get pvc
```
If an earlier placeholder deployment initialized `data-postgres-0`, stop. Do
not reuse or delete it until its ownership and retention requirements are
reviewed. The current base generates no Secrets and cannot initialize it.
## 3. First rollout
Review the diff, then apply only the production overlay:
```bash
kubectl diff -k overlays/production
kubectl apply -k overlays/production
kubectl -n voice-dialer rollout status statefulset/postgres --timeout=10m
kubectl -n voice-dialer rollout status statefulset/dialer-engine --timeout=15m
```

The first engine start requires public HTTPS egress for source/module fetches.
Inspect init logs without printing environment values:

```bash
kubectl -n voice-dialer logs dialer-engine-0 -c render-asterisk-config
kubectl -n voice-dialer logs dialer-engine-0 -c build-dialer-app
kubectl -n voice-dialer logs dialer-engine-0 -c asterisk
kubectl -n voice-dialer logs dialer-engine-0 -c app
```

The app owns schema migrations and must use an advisory lock so one failed
restart cannot partially migrate. PostgreSQL shutdown has 90 seconds; Asterisk
receives `core stop gracefully`; the app must stop scheduling immediately on
SIGTERM and drain or mark in-flight calls before exiting.

## 4. Disabled-state checks

```bash
kubectl -n voice-dialer get pods,pvc,svc,ingress,pdb,networkpolicy
kubectl -n voice-dialer exec dialer-engine-0 -c asterisk -- \
  asterisk -C /config/asterisk.conf -rx 'pjsip show endpoints'
kubectl -n voice-dialer exec dialer-engine-0 -c asterisk -- \
  asterisk -C /config/asterisk.conf -rx 'http show status'
```

There must be no `outbound` endpoint while the trunk flag is false. Asterisk
HTTP must listen on `127.0.0.1:8088`. Confirm unauthenticated API requests
return `401`/`403`, role tokens differ, health uses `/health/live` and
`/health/ready`, metrics answer only through the private `9090` Service, and
metrics are inaccessible through the Ingress.
Confirm the app UID can create, fsync, and remove a test file in
`/media/ari-journal`; do not remove any existing journal records.

Check `DIALING_ENABLED=false`, `CPS=0`, and concurrency `20` through the
authenticated status endpoint. The app enforces a hard maximum of 100.

## 5. Campaign media

Upload the reviewed 8 kHz, mono, 16-bit PCM WAV through the app's authenticated
campaign workflow. The app must validate it, hash the bytes, fsync a temporary
file, set mode `0640`, and atomically install `/media/<sha>.wav`. Asterisk sees
the same file read-only at
`/var/lib/asterisk/sounds/campaigns/<sha>.wav`.

For each attempt the app validates destination and caller ID as US E.164,
originates `PJSIP/<destination>@outbound` into the ARI application, and plays
`sound:campaigns/<sha>`. Verify answer, playback completion, DTMF `9`, and
durable opt-out state using approved controlled numbers before broad dialing.
The runtime `ARI_ENDPOINT` value itself must be only `outbound`, because the app
constructs the complete PJSIP endpoint string.

## 6. Two-stage enablement

First add the reviewed URI/auth fields and set `DIALER_TRUNK_ENABLED=true` in
the ignored `trunk.env`. Render/review/apply and verify the one `outbound`
endpoint uses the exact account target, PCMU, RFC4733, and plain RTP. Caller ID
is intentionally not hardcoded in PJSIP; the app supplies an authorized value
per attempt. Scheduler settings remain paused.

Configure/test external backups and set the destination, status, and
acknowledgement in `safety.env`. After controlled call, opt-out, capacity, CPS,
and failure-path tests pass, change `CPS` to the approved positive value.
Change `DIALING_ENABLED=true` in a separate reviewed rollout and rerun
`./validate.sh --production`. The validator refuses enablement without trunk,
carrier, CPS, and backup gates. Never exceed `MAX_CONCURRENCY=100`; default is
`20`.

## Emergency pause and rollback

Set `DIALING_ENABLED=false` and `CPS=0`, render, and apply. If scheduler behavior
is suspect, also set `DIALER_TRUNK_ENABLED=false`; this removes the endpoint
after the pod rolls. Preserve logs and database evidence. Roll back application
code only to another reviewed 40-character commit and keep schema compatibility
in mind. Preserve `/media/ari-journal`; pending or malformed records are
operator-action evidence and must not be discarded to recover readiness.

Both singleton PDBs use `maxUnavailable: 1`, so they do not deadlock voluntary
maintenance. Maintenance still causes downtime. StatefulSet ordered replacement
prevents two engine pods from contending for the RWO media claim.
