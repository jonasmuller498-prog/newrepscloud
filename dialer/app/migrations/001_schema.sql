CREATE TABLE IF NOT EXISTS message_assets (
    id uuid PRIMARY KEY,
    sha256 bytea NOT NULL UNIQUE,
    storage_name text NOT NULL UNIQUE,
    byte_size bigint NOT NULL CHECK (byte_size > 0),
    duration_ms bigint NOT NULL CHECK (duration_ms > 0),
    format text NOT NULL CHECK (format = 'pcm_s16le_mono_8000'),
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS caller_ids (
    id uuid PRIMARY KEY,
    phone_cipher bytea NOT NULL,
    phone_hash bytea NOT NULL UNIQUE,
    authorization_reference text NOT NULL CHECK (length(authorization_reference) > 0),
    authorized_at timestamptz NOT NULL,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS campaigns (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    state text NOT NULL DEFAULT 'DRAFT' CHECK (state IN
      ('DRAFT','VALIDATING','APPROVED','SCHEDULED','RUNNING','PAUSED',
       'DRAINING','COMPLETED','CANCELLED')),
    message_asset_id uuid REFERENCES message_assets(id),
    caller_id_id uuid REFERENCES caller_ids(id),
    dnc_attested_at timestamptz,
    scheduled_at timestamptz,
    window_start time NOT NULL DEFAULT '08:00',
    window_end time NOT NULL DEFAULT '21:00',
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (window_start < window_end)
);

CREATE TABLE IF NOT EXISTS campaign_approvals (
    id uuid PRIMARY KEY,
    campaign_id uuid NOT NULL UNIQUE REFERENCES campaigns(id),
    message_asset_id uuid NOT NULL REFERENCES message_assets(id),
    caller_id_id uuid NOT NULL REFERENCES caller_ids(id),
    approver text NOT NULL,
    approved_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS recipients (
    id uuid PRIMARY KEY,
    phone_cipher bytea NOT NULL,
    phone_hash bytea NOT NULL UNIQUE,
    timezone text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS consent_evidence (
    id uuid PRIMARY KEY,
    recipient_id uuid NOT NULL REFERENCES recipients(id),
    consent_at timestamptz NOT NULL,
    source text NOT NULL CHECK (length(source) BETWEEN 1 AND 200),
    imported_by text NOT NULL,
    imported_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (recipient_id, consent_at, source)
);

CREATE TABLE IF NOT EXISTS suppressions (
    phone_hash bytea PRIMARY KEY,
    phone_cipher bytea NOT NULL,
    reason text NOT NULL,
    source text NOT NULL,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS campaign_recipients (
    id uuid PRIMARY KEY,
    campaign_id uuid NOT NULL REFERENCES campaigns(id),
    recipient_id uuid NOT NULL REFERENCES recipients(id),
    consent_evidence_id uuid NOT NULL REFERENCES consent_evidence(id),
    timezone text NOT NULL,
    status text NOT NULL DEFAULT 'QUEUED' CHECK (status IN
      ('QUEUED','ACTIVE','SUCCEEDED','FAILED','SUPPRESSED','CANCELLED','QUARANTINED')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, recipient_id)
);

CREATE INDEX IF NOT EXISTS campaign_recipient_queue
    ON campaign_recipients (next_attempt_at, campaign_id) WHERE status = 'QUEUED';

CREATE OR REPLACE FUNCTION reject_immutable_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION '% rows are immutable', TG_TABLE_NAME;
END;
$$;

DROP TRIGGER IF EXISTS message_assets_immutable ON message_assets;
CREATE TRIGGER message_assets_immutable
BEFORE UPDATE OR DELETE ON message_assets
FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();

DROP TRIGGER IF EXISTS consent_evidence_immutable ON consent_evidence;
CREATE TRIGGER consent_evidence_immutable
BEFORE UPDATE OR DELETE ON consent_evidence
FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();

DROP TRIGGER IF EXISTS campaign_approvals_immutable ON campaign_approvals;
CREATE TRIGGER campaign_approvals_immutable
BEFORE UPDATE OR DELETE ON campaign_approvals
FOR EACH ROW EXECUTE FUNCTION reject_immutable_change();
