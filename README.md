# SIP B2BUA

Kubernetes manifests for the isolated Asterisk gateway used by:

```text
Voxbone -> 51.255.71.124:32061 -> Asterisk -> 100@2.24.198.63:10069
```

The dialplan accepts only `+18335167239` (with or without `+`) and has no
general-purpose outbound route. Asterisk anchors RTP and negotiates PCMU/PCMA
independently on each SIP leg. The Voxbone leg terminates SDES-SRTP while the
IVR leg uses plain RTP.

## Deploy

```bash
kubectl apply -k .
kubectl rollout status deployment/asterisk-b2bua -n sip-b2bua
```

Public ports:

- SIP: `32061/UDP` and `32061/TCP`
- RTP: `32200-32219/UDP`

The workload is pinned to the `playground` node because the NodePort service
uses `externalTrafficPolicy: Local`.

## Verify

```bash
kubectl exec -n sip-b2bua deploy/asterisk-b2bua -- \
  asterisk -rx "pjsip show endpoints"
kubectl logs -n sip-b2bua deploy/asterisk-b2bua
```

The anonymous PJSIP endpoint is temporary and reaches only the restricted
single-DID context. Replace it with current Voxbone signaling CIDRs after the
first verified call.
