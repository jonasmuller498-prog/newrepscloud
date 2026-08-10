# Production inputs

This directory is intentionally empty except for this file. Every `*.env` file
is ignored by Git. Create the eight files described in the top-level runbook,
keep them mode `0600`, and render the production overlay only from a trusted
workstation.
The render is fixed to `voice-dialer-production`; it never adopts staging PVCs
or Secrets. `network.env` must define a distinct `PUBLIC_HOSTNAME`, the
`DIALER_PUBLIC_IPV4`, and exact carrier networks.

Required files:

- `runtime.env`
- `network.env`
- `safety.env`
- `postgres-admin.env`
- `postgres-runtime.env`
- `app.env`
- `ari.env`
- `trunk.env`

Kustomize replaces the placeholder generators from the base with these local
files. It hashes generated names, so changed inputs trigger a controlled pod
restart without committing a Secret.
