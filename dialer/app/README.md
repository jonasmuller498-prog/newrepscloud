# Compliance-first voice broadcast control plane

This directory builds one Go binary that serves the admin UI/API, applies PostgreSQL
migrations, elects one scheduler, enforces fixed call slots and database-time CPS,
delivers a transactional ARI outbox, consumes ARI events, reconciles state, and
exports Prometheus text metrics.

Calls originate directly to `PJSIP/<E164>@<ARI_ENDPOINT>` with Stasis `app` and
`appArgs` only. Once answered in Stasis, ARI plays
`sound:campaigns/<approved-sha>` with a deterministic playback ID. Completion or
DTMF `9` queues an ARI hangup, while the fixed slot remains held until terminal
event or channel-absence reconciliation.

State-changing ARI events are fsynced as redacted records in
`EVENT_JOURNAL_DIR` before database projection. DTMF `9` is journaled before
the immediate hangup request. Failed projections stop new originations and
retry; startup replays the original event key before ARI readiness is restored.
Malformed records are retained and require operator action.

The service does not decide whether a call is legally exempt. Operators must provide
consent and current DNC evidence for every production campaign. It has no scraping,
predictive over-dialing, AMD, recording, caller-ID rotation, or script generation.

## Safety defaults

`DIALING_ENABLED=false` and `CPS=0` prevent origination. A campaign never launches
from its schedule alone: an operator must explicitly start it after a separate
approver approves the selected audio and caller ID. Suppression is checked at import,
validation, queue claim, and immediately before ARI delivery.

Recipient timezones are restricted to canonical IANA locations in the United States
(including Alaska, Aleutian, Arizona, Hawaii, Indiana, Kentucky, and North Dakota
variants). `UTC`, fixed offsets, non-US locations, and `Local` are rejected.

Ambiguous ARI requests and unknown terminal events are quarantined. Only explicit
busy, no-answer, and temporary outcomes retry, up to three attempts. Answered,
message-started, invalid, forbidden, opt-out, and ambiguous outcomes do not retry.
Pause is immediate: claimed work is released, ringing channels are terminated, and
answered or message-started calls are quarantined instead of automatically retried.

## Configuration

| Variable | Default / requirement |
| --- | --- |
| `DATABASE_URL` | Required PostgreSQL URL |
| `HTTP_ADDR` | `:8080` |
| `METRICS_ADDR` | `:9090`; must differ from `HTTP_ADDR` |
| `DIALING_ENABLED` | `false` |
| `MAX_CONCURRENCY` | `20`; hard maximum `100` |
| `CPS` | `0`; range `0..100` |
| `CALLING_HOURS_START` / `END` | `08:00` / `21:00`, recipient-local |
| `MEDIA_DIR` | `/var/lib/dialer/media` |
| `ARI_URL` | ARI server base URL, required when dialing is enabled |
| `ARI_APP`, `ARI_USER`, `ARI_PASSWORD` | Required when dialing is enabled |
| `ARI_ENDPOINT` | Plain PJSIP endpoint name; deployment value is `outbound` |
| `EVENT_JOURNAL_DIR` | Required writable absolute path when dialing is enabled |
| `OPERATOR_API_TOKEN` | Required high-entropy token of at least 32 bytes |
| `APPROVER_API_TOKEN` | Required high-entropy token; must differ from operator |
| `PHONE_HASH_KEY` | Required high-entropy phone-index key, at least 32 bytes |
| `FIELD_ENCRYPTION_KEY` | Required high-entropy field-encryption key, at least 32 bytes |
| `AUDIT_HMAC_KEY` | Required high-entropy audit actor/callback key, at least 32 bytes |

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
The process must have read/write access to `MEDIA_DIR` and, while dialing,
`EVENT_JOURNAL_DIR`. The deployment runs as UID/GID `1000`. Media files are
written mode `0640` for the shared fsGroup.

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
`X-Dialer-Signature`, `v1=` plus the lowercase HMAC-SHA256 of
`callback:v1:` and the exact request body.

Recipient uploads require an `Idempotency-Key`. They return `202 Accepted` with
an import job; poll the response `Location` until it is `COMPLETED` or `FAILED`.

## Endpoints

Health is at `/health/live` and `/health/ready` on `HTTP_ADDR`. `/metrics` is served
only on `METRICS_ADDR` and is absent from the public mux. The operator UI is at `/`
and the separate approver UI is at `/approver.html`. Versioned JSON routes are under
`/api/v1`. Operator routes mutate drafts,
imports, caller IDs, schedules, controls, and suppressions. Only the approver token
can call campaign approval. Attempts and persisted correlated ARI events support an
optional `campaign_id` query parameter.

Payloads are limited to 20 MiB globally for CSV/WAV and 16–64 KiB for JSON routes.
Deploy the metrics listener behind network policy if its counts are sensitive.

## Integration tests

Set `TEST_DATABASE_URL` to an isolated PostgreSQL database and run:

```sh
go test -tags=integration ./...
```

Integration tests skip when the variable is absent. Never point them at production;
the test schema is migrated and test-created rows are removed.
