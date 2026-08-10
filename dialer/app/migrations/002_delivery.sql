CREATE TABLE IF NOT EXISTS call_attempts (
    id uuid PRIMARY KEY,
    campaign_recipient_id uuid NOT NULL REFERENCES campaign_recipients(id),
    recipient_id uuid NOT NULL REFERENCES recipients(id),
    attempt_no integer NOT NULL CHECK (attempt_no > 0),
    channel_id text NOT NULL UNIQUE,
    slot_no integer NOT NULL CHECK (slot_no BETWEEN 1 AND 100),
    state text NOT NULL CHECK (state IN
      ('CLAIMED','ORIGINATING','RINGING','ANSWERED','MESSAGE_STARTED',
       'COMPLETED','BUSY','NO_ANSWER','TEMPORARY','INVALID','FORBIDDEN',
       'OPT_OUT','AMBIGUOUS','CANCELLED')),
    outcome text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    ended_at timestamptz,
    UNIQUE (campaign_recipient_id, attempt_no)
);

CREATE UNIQUE INDEX IF NOT EXISTS one_active_attempt_per_recipient
    ON call_attempts (recipient_id)
    WHERE state IN ('CLAIMED','ORIGINATING','RINGING','ANSWERED','MESSAGE_STARTED');

CREATE TABLE IF NOT EXISTS dialer_slots (
    slot_no integer PRIMARY KEY CHECK (slot_no BETWEEN 1 AND 100),
    attempt_id uuid UNIQUE REFERENCES call_attempts(id),
    leased_at timestamptz
);

INSERT INTO dialer_slots (slot_no)
SELECT generate_series(1, 100)
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS call_events (
    id bigserial PRIMARY KEY,
    attempt_id uuid NOT NULL REFERENCES call_attempts(id),
    ari_event_id text,
    event_type text NOT NULL,
    raw jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS unique_ari_event
    ON call_events (attempt_id, ari_event_id)
    WHERE ari_event_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS outbox (
    id uuid PRIMARY KEY,
    aggregate_id uuid NOT NULL,
    kind text NOT NULL,
    payload jsonb NOT NULL,
    state text NOT NULL DEFAULT 'PENDING' CHECK (state IN ('PENDING','PROCESSING','DONE')),
    created_at timestamptz NOT NULL DEFAULT now(),
    processing_at timestamptz,
    processed_at timestamptz
);

CREATE INDEX IF NOT EXISTS pending_outbox
    ON outbox (created_at) WHERE state = 'PENDING';

CREATE TABLE IF NOT EXISTS cps_limiter (
    limiter_key text PRIMARY KEY,
    theoretical_arrival timestamptz NOT NULL
);

INSERT INTO cps_limiter (limiter_key, theoretical_arrival)
VALUES ('global', '-infinity')
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS audit_log (
    id bigserial PRIMARY KEY,
    actor text NOT NULL,
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    detail jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS audit_resource
    ON audit_log (resource_type, resource_id, created_at DESC);

DROP TRIGGER IF EXISTS audit_log_immutable ON audit_log;
CREATE TRIGGER audit_log_immutable
BEFORE UPDATE OR DELETE ON audit_log
FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();
