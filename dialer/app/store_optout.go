package main

import (
	"context"
	"encoding/json"
)

func (s *Store) OptOutAttempt(ctx context.Context, attemptID, source, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var hash, ciphertext []byte
	var campaignRecipientID string
	err = tx.QueryRow(ctx, `SELECT r.phone_hash,r.phone_cipher,cr.id FROM call_attempts a
		JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		JOIN recipients r ON r.id=cr.recipient_id WHERE a.id=$1 FOR UPDATE OF a`,
		attemptID).Scan(&hash, &ciphertext, &campaignRecipientID)
	if err != nil {
		return dbError(err)
	}
	if err = s.suppressTx(ctx, tx, hash, ciphertext, "recipient_opt_out", source, actor); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE call_attempts SET state='OPT_OUT',outcome='opt_out',
		ended_at=COALESCE(ended_at,now()),updated_at=now() WHERE id=$1
		AND state IN ('CLAIMED','ORIGINATING','RINGING','ANSWERED','MESSAGE_STARTED','OPT_OUT')`,
		attemptID)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE campaign_recipients SET status='SUPPRESSED'
			WHERE id=$1`, campaignRecipientID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE dialer_slots SET attempt_id=NULL,leased_at=NULL
			WHERE attempt_id=$1`, attemptID)
	}
	if err == nil {
		detail, _ := json.Marshal(map[string]string{"source": source})
		err = audit(ctx, tx, actor, "recipient.opt_out", "attempt", attemptID, detail)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func suppressionWins(suppressed bool, campaignRunning bool, consentValid bool) bool {
	return !suppressed && campaignRunning && consentValid
}
