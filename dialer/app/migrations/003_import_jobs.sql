CREATE TABLE import_jobs (
    id uuid PRIMARY KEY,
    campaign_id uuid NOT NULL REFERENCES campaigns(id),
    actor text NOT NULL,
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 8 AND 200),
    payload_sha256 bytea NOT NULL,
    payload_cipher bytea NOT NULL,
    state text NOT NULL DEFAULT 'PENDING'
      CHECK (state IN ('PENDING','PROCESSING','COMPLETED','FAILED')),
    received_rows integer NOT NULL CHECK (received_rows > 0 AND received_rows <= 100000),
    inserted_rows integer,
    error_code text,
    available_at timestamptz NOT NULL DEFAULT now(),
    processing_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, actor, idempotency_key)
);

CREATE INDEX pending_import_jobs
    ON import_jobs (available_at, created_at) WHERE state = 'PENDING';
