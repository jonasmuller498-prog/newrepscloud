package main

import (
	"context"
	"time"
)

func (s *Store) CampaignSafetyBlocks(
	ctx context.Context, id string, requireApproval bool, now time.Time,
) ([]SafetyBlock, error) {
	var assetID, callerID, storage *string
	var mediaSHA []byte
	var mediaBytes, mediaDuration int64
	var dnc, authorized *time.Time
	var recipients, missingConsent, suppressed int64
	err := s.pool.QueryRow(ctx, `SELECT c.message_asset_id,c.caller_id_id,c.dnc_attested_at,
		ci.authorized_at,ma.storage_name,COALESCE(ma.sha256,''::bytea),
		COALESCE(ma.byte_size,0),COALESCE(ma.duration_ms,0),
		(SELECT count(*) FROM campaign_recipients cr JOIN recipients r ON r.id=cr.recipient_id
		  WHERE cr.campaign_id=c.id AND cr.status='QUEUED'
		  AND NOT EXISTS(SELECT 1 FROM suppressions sp WHERE sp.phone_hash=r.phone_hash)),
		(SELECT count(*) FROM campaign_recipients cr LEFT JOIN consent_evidence ce
		  ON ce.id=cr.consent_evidence_id WHERE cr.campaign_id=c.id
		  AND (ce.id IS NULL OR ce.consent_at>now() OR ce.source='')),
		(SELECT count(*) FROM campaign_recipients cr JOIN recipients r ON r.id=cr.recipient_id
		  JOIN suppressions sp ON sp.phone_hash=r.phone_hash WHERE cr.campaign_id=c.id
		  AND cr.status<>'SUPPRESSED')
		FROM campaigns c
		LEFT JOIN caller_ids ci ON ci.id=c.caller_id_id
		LEFT JOIN message_assets ma ON ma.id=c.message_asset_id WHERE c.id=$1`, id).
		Scan(&assetID, &callerID, &dnc, &authorized, &storage,
			&mediaSHA, &mediaBytes, &mediaDuration,
			&recipients, &missingConsent, &suppressed)
	if err != nil {
		return nil, dbError(err)
	}
	var blocks []SafetyBlock
	add := func(code, message string, count int64) {
		blocks = append(blocks, SafetyBlock{code, message, count})
	}
	if assetID == nil || storage == nil {
		add("audio_missing", "A validated WAV message asset is required.", 0)
	} else {
		spec := MediaSpec{*storage, mediaSHA, mediaBytes, mediaDuration}
		if _, verifyErr := verifyMediaFile(s.config.MediaDir, spec,
			s.config.AssetMaxDuration, s.config.MaxBodyBytes); verifyErr != nil {
			add("audio_tampered", "The approved message asset failed integrity validation.", 0)
		}
	}
	if callerID == nil || authorized == nil || authorized.After(now.Add(5*time.Minute)) {
		add("caller_id_unauthorized", "An authorized caller ID is required.", 0)
	}
	if dnc == nil || dnc.Before(now.AddDate(0, 0, -31)) || dnc.After(now.Add(5*time.Minute)) {
		add("dnc_stale", "DNC attestation must be current within 31 days.", 0)
	}
	if recipients == 0 {
		add("recipients_missing", "At least one consented recipient is required.", 0)
	}
	if missingConsent > 0 {
		add("consent_missing", "Every recipient requires timestamped consent evidence.", missingConsent)
	}
	if suppressed > 0 {
		add("suppressed_recipients", "Suppressed recipients must be removed from eligibility.", suppressed)
	}
	invalidTZ, err := s.invalidTimezoneCount(ctx, id)
	if err != nil {
		return nil, err
	}
	if invalidTZ > 0 {
		add("timezone_invalid", "Every recipient requires a valid IANA timezone.", invalidTZ)
	}
	if requireApproval && assetID != nil && callerID != nil {
		var approved bool
		err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM campaign_approvals
			WHERE campaign_id=$1 AND message_asset_id=$2 AND caller_id_id=$3)`,
			id, *assetID, *callerID).Scan(&approved)
		if err != nil {
			return nil, err
		}
		if !approved {
			add("approval_missing", "A separate approver must approve audio and caller ID.", 0)
		}
	}
	return blocks, nil
}

func (s *Store) invalidTimezoneCount(ctx context.Context, campaignID string) (int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT cr.timezone FROM campaign_recipients cr
		WHERE cr.campaign_id=$1`, campaignID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var invalid int64
	for rows.Next() {
		var timezone string
		if err := rows.Scan(&timezone); err != nil {
			return 0, err
		}
		if validTimezone(timezone) != nil {
			invalid++
		}
	}
	return invalid, rows.Err()
}
