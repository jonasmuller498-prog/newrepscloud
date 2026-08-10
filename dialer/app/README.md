# Compliance-first voice broadcast control plane

This directory builds one Go binary that serves the admin UI/API, applies PostgreSQL
migrations, elects one scheduler, enforces fixed call slots and database-time CPS,
delivers a transactional ARI outbox, consumes ARI events, reconciles state, and
exports Prometheus text metrics.

The service does not decide whether a call is legally exempt. Operators must provide
consent and current DNC evidence for every production campaign. It has no scraping,
predictive over-dialing, AMD, recording, caller-ID rotation, or script generation.

## Safety defaults

`DIALING_ENABLED=false` and `CPS=0` prevent origination. A campaign never launches
from its schedule alone: an operator must explicitly start it after a separate
approver approves the selected audio and caller ID. Suppression is checked at import,
validation, queue claim, and immediately before ARI delivery.

Ambiguous ARI requests and unknown terminal events are quarantined. Only explicit
busy, no-answer, and temporary outcomes retry, up to three attempts. Answered,
message-started, invalid, forbidden, opt-out, and ambiguous outcomes do not retry.

## Configuration

| Variable | Default / requirement |
| --- | --- |
| `DATABASE_URL` | Required PostgreSQL URL |
| `HTTP_ADDR` | `:8080` |
| `DIALING_ENABLED` | `false` |
| `MAX_CONCURRENCY` | `20`; hard maximum `100` |
| `CPS` | `0`; range `0..100` |
| `CALLING_HOURS_START` / `END` | `08:00` / `21:00`, recipient-local |
| `MEDIA_DIR` | `/var/lib/dialer/media` |
| `ARI_URL` | ARI server base URL, required when dialing is enabled |
| `ARI_APP`, `ARI_USER`, `ARI_PASSWORD` | Required when dialing is enabled |
| `ARI_ENDPOINT_TEMPLATE` | `PJSIP/%s@outbound`; exactly one `%s` |
| `ARI_CONTEXT`, `ARI_EXTENSION` | `outbound-compliance`, `s` |
| `OPERATOR_API_TOKEN` | Required |
| `APPROVER_API_TOKEN` | Required and must differ from operator |
| `HMAC_KEY` | Required, at least 32 raw or base64-decoded bytes |

Use a dedicated database role, TLS for PostgreSQL and ARI, a TLS reverse proxy for
HTTP, and secret injection rather than environment files in an image. Phone numbers
are AES-GCM encrypted at rest and indexed by keyed HMAC. API responses only show the
last four digits.

## Run

```sh
go test ./...
go build -o dialer .
./dialer
```

Migrations are embedded and applied under a PostgreSQL advisory lock at startup.
The process must have read/write access to `MEDIA_DIR`. The container runs as
UID/GID `10001`.

## Workflow

1. Register caller ID authorization evidence.
2. Create a draft with a fresh DNC attestation and caller-ID ID.
3. Upload recipient CSV with exactly
   `phone_e164,timezone,consent_at,consent_source`.
4. Upload RIFF PCM 16-bit, mono, 8 kHz WAV audio (maximum 10 minutes).
5. Operator submits validation; separate approver approves.
6. Operator schedules, then explicitly starts when the scheduled time arrives.
7. Monitor safety blocks, attempts, events, suppressions, readiness, and metrics.

DTMF `9` ARI events immediately opt out the associated recipient. A dialplan may
instead POST `{"attempt_id":"..."}` or `{"phone_e164":"+..."}` to
`/api/v1/opt-outs`, authenticated with an operator bearer token or
`X-Dialer-Signature`, the lowercase hex HMAC-SHA256 of the exact request body.

## Endpoints

Health is at `/health/live` and `/health/ready`; metrics are at `/metrics`; the UI is
at `/`. Versioned JSON routes are under `/api/v1`. Operator routes mutate drafts,
imports, caller IDs, schedules, controls, and suppressions. Only the approver token
can call campaign approval. Attempts and persisted correlated ARI events support an
optional `campaign_id` query parameter.

Payloads are limited to 20 MiB globally for CSV/WAV and 16–64 KiB for JSON routes.
Deploy the metrics and health endpoints behind network policy if their counts are
sensitive.

## Integration tests

Set `TEST_DATABASE_URL` to an isolated PostgreSQL database and run:

```sh
go test -tags=integration ./...
```

Integration tests skip when the variable is absent. Never point them at production;
the test schema is migrated and test-created rows are removed.
