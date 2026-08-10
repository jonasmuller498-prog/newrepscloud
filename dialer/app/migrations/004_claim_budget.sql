ALTER TABLE campaign_recipients
    ADD COLUMN claim_count integer NOT NULL DEFAULT 0
    CHECK (claim_count >= 0);

UPDATE campaign_recipients cr
SET claim_count = GREATEST(
    cr.attempt_count,
    COALESCE((
        SELECT max(a.attempt_no)
        FROM call_attempts a
        WHERE a.campaign_recipient_id = cr.id
    ), 0)
);

ALTER TABLE campaign_recipients
    ADD CONSTRAINT claim_count_covers_attempts
    CHECK (claim_count >= attempt_count);
