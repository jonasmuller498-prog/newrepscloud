# Deployment and operations runbook

All commands are operator procedures; these assets have not been deployed.
Use a change window and retain `DIALING_ENABLED=false`, `CPS=0`, and
`DIALER_TRUNK_ENABLED=false` through all initial checks.

## 1. RKE2 preflight

```bash
kubectl get node runners -o wide
kubectl get storageclass longhorn
kubectl get clusterissuer letsencrypt-prod
kubectl get ingressclass nginx
kubectl get crd volumesnapshots.snapshot.storage.k8s.io
kubectl get services -A -o json > /tmp/services-before.json
```

Confirm `runners` reports public IP `5.196.90.231`, is schedulable, and has
capacity for both engine containers and build init containers. Confirm no live
Service claims `31100` or any port in `31500-31699`. The tracked root B2BUA
range (`32061`, `32200-32219`) does not conflict.

Verify the ingress-controller namespace, CoreDNS labels, provider CIDRs, exact
SIP account URI/port, explicit auth mode, caller ID, and upstream firewall.
`externalTrafficPolicy: Local` requires the engine to remain on `runners`.

## 2. Inputs and offline review

Run `scripts/init-production-inputs.sh` as described in the README. Review each
non-secret setting and inspect a secure render:

```bash
umask 077
kubectl kustomize overlays/production >/tmp/voice-dialer.yaml
./validate.sh
rg 'REQUIRED_|disabled\.invalid|192\.0\.2\.|198\.51\.100\.' /tmp/voice-dialer.yaml
```

The final `rg` must have no matches. Do not upload the rendered file to an
artifact service: it contains base64-encoded Secrets. Delete it securely under
the organization's workstation policy after review.

Verify the source commit exists in the public repository, contains the expected
`dialer/app/cmd/dialer` package, has a committed `go.sum`, and has completed
code/security review. The init build intentionally fails for a branch, tag,
missing module checksum, changed module graph, or newer automatic toolchain.

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

There must be no `voxbone` endpoint while the trunk flag is false. HTTP must
listen on `127.0.0.1:8088`, not the pod IP. Confirm unauthenticated API requests
return `401`/`403`, a valid token works over HTTPS, metrics are inaccessible
through the Ingress, PostgreSQL has no NodePort, and only UDP NodePorts exist.

Check `DIALING_ENABLED=false`, `CPS=0`, configured concurrency `20`, and hard
limit `100` through the authenticated diagnostics endpoint. Confirm no call can
be queued in paused mode.

## 5. Campaign media

Upload the reviewed 8 kHz, mono, 16-bit PCM WAV through the app's authenticated
campaign workflow. The app must write a unique temporary file on `/media`,
fsync it, verify `CAMPAIGN_WAV_SHA256`, chmod it `0440`, and atomically rename
it to `campaign.wav`. Asterisk sees the same RWO PVC read-only. Confirm the app
reports the expected digest. The dialplan checks file existence and rejects
before any external Dial if media is absent.

Run a test with a controlled destination while the scheduler remains paused.
The app must originate `Local/+1XXXXXXXXXX@dialer-app` with
`DIALER_ORIGIN_TOKEN`; direct arbitrary PSTN contexts are forbidden. Verify
answer, playback, completion, and DTMF `9` events and confirm opt-out persistence
before any later contact.

## 6. Two-stage enablement

First change only `DIALER_TRUNK_ENABLED=true` in the ignored `trunk.env`,
render/review/apply, and verify the one outbound endpoint uses the exact account
target, PCMU, RFC4733, plain RTP, and E.164 identity. Scheduler settings remain
paused.

After controlled call, opt-out, capacity, CPS, and failure-path tests pass,
change `CPS` to an approved positive value. Change `DIALING_ENABLED=true` in a
separate reviewed rollout. Never exceed `MAX_CONCURRENCY=100`; the supplied
default is `20`.

## Emergency pause and rollback

Set `DIALING_ENABLED=false` and `CPS=0`, render, and apply. If scheduler behavior
is suspect, also set `DIALER_TRUNK_ENABLED=false`; this removes the endpoint
after the pod rolls. Preserve logs and database evidence. Roll back application
code only to another reviewed 40-character commit and keep schema compatibility
in mind.

Both workloads are singletons with `minAvailable: 1` PDBs. This deliberately
blocks voluntary disruption while healthy. Coordinate an explicit PDB override
for node maintenance; do not casually delete the budget or force-delete a pod.
