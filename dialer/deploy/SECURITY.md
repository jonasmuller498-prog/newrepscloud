# Security model

## Fail-closed controls

The placeholder base is intentionally non-operational. Config rendering rejects
placeholder ARI/origin values, source building rejects a non-commit ref, the
Voxbone endpoint is not emitted unless `DIALER_TRUNK_ENABLED=true`, and the app
starts with dialing disabled and zero CPS. Enabling the trunk does not enable
the scheduler; both controls require separate reviewed input changes.

The Asterisk dialplan has one outbound context. It accepts only a `Local`
channel carrying the unguessable app origin token, validates exactly `+1` plus
ten digits, checks the immutable local WAV exists before `Dial()`, and enforces
a hard 100-external-call guard in addition to the app's hard ceiling. There is
no generic PSTN context. Unidentified or provider-originated SIP requests have
no endpoint; the endpoint's defensive context only hangs up.

The answered provider channel plays `/media/campaign.wav` locally. `9` is the
only valid `Background()` digit and emits `DialerOptOut`; answer, playback
start/completion, final completion, and rejection events are also emitted.
Channel variables mirror lifecycle state for ARI. Recording and AMD modules are
explicitly disabled and no recording application appears in the dialplan.

## Asterisk and provider

- ARI HTTP binds only `127.0.0.1:8088`; no Service or Ingress targets it.
- SIP exposes UDP only. RTP is plain by default, PCMU (`ulaw`) only, with
  RFC4733 DTMF. From and P-Asserted-Identity use the reviewed US E.164 caller ID.
- `digest` mode emits outbound auth. `ip` mode emits no auth object. Neither
  mode enables inbound campaign routes or provider registration.
- The exact assigned URI must be `sip:[account@]sbc:port`; the renderer rejects
  `.invalid`, empty, and placeholder targets when the trunk is enabled.
- Set signaling and media CIDRs to provider-published values. Prefer a `/32`
  for a single SBC. Do not infer an SBC from a brand-level hostname.

The app writes the Longhorn media PVC; Asterisk mounts it read-only. Stage a
new WAV under a unique temporary name, verify the reviewed SHA-256, set mode
`0440`, and atomically rename it to `campaign.wav`. Never overwrite the inode
in place. The app must refuse a digest mismatch and must not delete a file in
use.

## NetworkPolicy boundaries

`default-deny-all` selects every pod for ingress and egress. Explicit policies
permit only:

- ingress-controller namespace to app TCP 8080;
- configured provider signaling/media CIDRs to SIP/RTP;
- engine pod to PostgreSQL TCP 5432 and provider SIP/RTP;
- maintenance jobs to PostgreSQL; and
- cluster DNS where name resolution is required.

App-to-Asterisk traffic is loopback within one pod and does not traverse a
NetworkPolicy boundary. PostgreSQL accepts only engine and labeled maintenance
pods. The optional Prometheus policy opens only TCP 9090 from its selected
namespace/pods.

One constrained exception is public TCP 443 egress from the engine pod. It is
required because the mandated init container fetches a public Git commit and Go
modules, while standard RKE2 NetworkPolicy cannot select FQDNs or containers
inside one pod. Private/link-local CIDRs are excluded. Consequently the running
app also has public HTTPS egress. If that is unacceptable, install an approved
egress proxy or an FQDN-aware CNI policy and restrict GitHub/module hosts; the
init-container build topology cannot be made container-specific with standard
NetworkPolicy.

Before rendering production, verify actual labels:

```bash
kubectl get ns --show-labels
kubectl -n kube-system get pods --show-labels | sed -n '/dns/p;/ingress/p'
```

Set `INGRESS_NAMESPACE` accordingly. If RKE2 CoreDNS does not have
`k8s-app=kube-dns`, patch both DNS rules before rollout. NodePort source CIDR
filtering depends on the installed CNI honoring NetworkPolicy for host-routed
traffic; retain upstream firewalls for defense in depth.

## Secret and supply-chain handling

Only example placeholders are committed. Generated Secret names are hashed and
immutable. Production values stay in ignored mode-`0600` env files. Encrypt
those files at rest or remove them after handing values to the organization's
secret delivery system.

Images include tags for review and manifest-list digests for immutability.
Re-resolve and review digests during planned upgrades. The Go build disables
CGO, toolchain auto-download, mutable module edits, and credential prompts.
Grant no service-account token, Linux capability, privilege escalation, or
root UID. The namespace enforces the restricted Pod Security profile.
