# Production inputs

This directory is intentionally empty except for this file. Every `*.env` file
is ignored by Git. Create the six files described in the top-level runbook,
keep them mode `0600`, and render the production overlay only from a trusted
workstation.

Required files:

- `runtime.env`
- `network.env`
- `postgres.env`
- `app.env`
- `ari.env`
- `trunk.env`

Kustomize replaces the placeholder generators from the base with these local
files. It hashes generated names, so changed inputs trigger a controlled pod
restart without committing a Secret.
