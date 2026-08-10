package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) PrepareOriginate(
	ctx context.Context, item OutboxItem,
) (OriginateCommand, bool, time.Time, error) {
	var cmd OriginateCommand
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return cmd, false, time.Time{}, err
	}
	defer tx.Rollback(ctx)
	var phoneCipher, callerCipher []byte
	var media MediaSpec
	var campaignState, recipientStatus string
	var suppressed, approved, dncOK, consentOK, callerOK, windowOK bool
	err = tx.QueryRow(ctx, `SELECT a.id,a.channel_id,r.phone_cipher,ci.phone_cipher,
		ma.storage_name,ma.sha256,ma.byte_size,ma.duration_ms,c.state,cr.status,
		EXISTS(SELECT 1 FROM suppressions sp WHERE sp.phone_hash=r.phone_hash),
		ca.id IS NOT NULL,
		c.dnc_attested_at BETWEEN clock_timestamp()-interval '31 days' AND clock_timestamp(),
		ce.consent_at<=clock_timestamp() AND ce.source<>'',
		ci.authorized_at<=clock_timestamp(),
		(clock_timestamp() AT TIME ZONE cr.timezone)::time>=c.window_start AND
		  (clock_timestamp() AT TIME ZONE cr.timezone)::time<c.window_end
		FROM outbox o JOIN call_attempts a ON a.id=o.aggregate_id
		JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		JOIN campaigns c ON c.id=cr.campaign_id
		JOIN recipients r ON r.id=cr.recipient_id
		JOIN consent_evidence ce ON ce.id=cr.consent_evidence_id
		JOIN caller_ids ci ON ci.id=c.caller_id_id
		JOIN message_assets ma ON ma.id=c.message_asset_id
		LEFT JOIN campaign_approvals ca ON ca.campaign_id=c.id
		  AND ca.message_asset_id=ma.id AND ca.caller_id_id=ci.id
		WHERE o.id=$1 AND o.state='PROCESSING' AND o.kind='ARI_ORIGINATE'
		  AND a.id=$2 AND a.state='CLAIMED' FOR UPDATE OF o,a,cr,c`,
		item.ID, item.AttemptID).Scan(&cmd.AttemptID, &cmd.ChannelID, &phoneCipher,
		&callerCipher, &media.StorageName, &media.SHA256, &media.ByteSize,
		&media.DurationMS, &campaignState, &recipientStatus, &suppressed, &approved,
		&dncOK, &consentOK, &callerOK, &windowOK)
	if err != nil {
		return cmd, false, time.Time{}, dbError(err)
	}
	eligible := campaignState == "RUNNING" && recipientStatus == "ACTIVE" &&
		!suppressed && approved && dncOK && consentOK && callerOK && windowOK
	if !eligible {
		status, outcome := "QUARANTINED", "eligibility_changed"
		if suppressed {
			status, outcome = "SUPPRESSED", "suppressed"
		} else if campaignState == "PAUSED" ||
			(campaignState == "RUNNING" && approved && dncOK && consentOK && callerOK) {
			status, outcome = "QUEUED", "cancelled"
		} else if campaignState != "RUNNING" {
			status, outcome = "CANCELLED", "cancelled"
		}
		err = failUndeliveredTx(ctx, tx, item, status, outcome)
		return cmd, false, time.Time{}, commitResult(ctx, tx, err)
	}
	mediaSHA, verifyErr := verifyMediaFile(s.config.MediaDir, media,
		s.config.AssetMaxDuration, s.config.MaxBodyBytes)
	if verifyErr != nil {
		err = failUndeliveredTx(ctx, tx, item, "QUARANTINED", "media_integrity")
		return cmd, false, time.Time{}, commitResult(ctx, tx, err)
	}
	cmd.MediaSHA = mediaSHA
	if cmd.Phone, err = s.protector.Decrypt(phoneCipher); err == nil {
		cmd.CallerID, err = s.protector.Decrypt(callerCipher)
	}
	if err != nil {
		return cmd, false, time.Time{}, err
	}
	allowed, retryAt, err := takeCPSToken(ctx, tx, s.config.CPS)
	if err != nil {
		return cmd, false, time.Time{}, err
	}
	if !allowed {
		_, err = tx.Exec(ctx, `UPDATE outbox SET state='PENDING',processing_at=NULL,
			available_at=$2 WHERE id=$1 AND state='PROCESSING'`, item.ID, retryAt)
		return cmd, false, retryAt, commitResult(ctx, tx, err)
	}
	_, err = tx.Exec(ctx, `UPDATE call_attempts SET state='ORIGINATING',updated_at=now()
		WHERE id=$1 AND state='CLAIMED'`, item.AttemptID)
	if err != nil {
		return cmd, false, time.Time{}, err
	}
	return cmd, true, retryAt, tx.Commit(ctx)
}

func failUndeliveredTx(
	ctx context.Context, tx pgx.Tx, item OutboxItem, recipientStatus, outcome string,
) error {
	state := "CANCELLED"
	if outcome == "suppressed" {
		state = "SUPPRESSED"
	}
	_, err := tx.Exec(ctx, `UPDATE call_attempts SET state=$2,outcome=$3,
		ended_at=now(),updated_at=now() WHERE id=$1 AND state='CLAIMED'`,
		item.AttemptID, state, outcome)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE campaign_recipients SET status=$2
			WHERE id=(SELECT campaign_recipient_id FROM call_attempts WHERE id=$1)`,
			item.AttemptID, recipientStatus)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE dialer_slots SET attempt_id=NULL,leased_at=NULL
			WHERE attempt_id=$1`, item.AttemptID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE outbox SET state='CANCELLED',processed_at=now(),
			processing_at=NULL WHERE id=$1`, item.ID)
	}
	return err
}

func commitResult(ctx context.Context, tx pgx.Tx, err error) error {
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
