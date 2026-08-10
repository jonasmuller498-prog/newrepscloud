# Staging-disabled inputs

Every `*.env` file here is ignored by Git. Create the seven mode-`0600` files
with `../../../scripts/init-staging-inputs.sh`; do not write them by hand:

- `runtime.env`
- `safety.env`
- `postgres-admin.env`
- `postgres-runtime.env`
- `app.env`
- `ari.env`
- `trunk.env`

There is deliberately no network input or carrier CIDR. The overlay replaces
the base network values with a disabled marker, removes every SIP/RTP Service
and carrier policy, and renders Asterisk without a transport or route.
