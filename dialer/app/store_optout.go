package main

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

func (s *Store) OptOutAttempt(ctx context.Context, attemptID, source, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = s.optOutAttemptTx(ctx, tx, attemptID, source, actor); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) optOutAttemptTx(
	ctx context.Context, tx pgx.Tx, attemptID, source, actor string,
) error {
	var hash, ciphertext []byte
	var campaignRecipientID string
	err := tx.QueryRow(ctx, `SELECT r.phone_hash,r.phone_cipher,cr.id FROM call_attempts a
		JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		JOIN recipients r ON r.id=cr.recipient_id WHERE a.id=$1 FOR UPDATE OF a`,
		attemptID).Scan(&hash, &ciphertext, &campaignRecipientID)
	if err != nil {
		return dbError(err)
	}
	if err = s.suppressTx(ctx, tx, hash, ciphertext, "recipient_opt_out", source, actor); err != nil {
		return err
	}
	err = requestTerminationTx(ctx, tx, attemptID, "opt_out")
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE campaign_recipients SET status='SUPPRESSED'
			WHERE id=$1`, campaignRecipientID)
	}
	if err == nil {
		detail, _ := json.Marshal(map[string]string{"source": source})
		err = audit(ctx, tx, actor, "recipient.opt_out", "attempt", attemptID, detail)
	}
	if err != nil {
		return err
	}
	return nil
}

func suppressionWins(suppressed bool, campaignRunning bool, consentValid bool) bool {
	return !suppressed && campaignRunning && consentValid
}
