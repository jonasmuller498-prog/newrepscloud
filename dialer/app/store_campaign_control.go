package main

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func (s *Store) stopCampaignDelivery(
	ctx context.Context, id, actor string, pause bool,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current string
	if err = tx.QueryRow(ctx, `SELECT state FROM campaigns WHERE id=$1
		FOR UPDATE`, id).Scan(&current); err != nil {
		return dbError(err)
	}
	target, action := "DRAINING", "campaign.cancel"
	if pause {
		target, action = "PAUSED", "campaign.pause"
		if current != "RUNNING" {
			return errConflict
		}
	} else if !canTransition(current, target) {
		return errConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE campaigns SET state=$2,updated_at=now()
		WHERE id=$1`, id, target); err != nil {
		return err
	}
	queuedStatus := "CANCELLED"
	if pause {
		queuedStatus = "QUEUED"
	}
	_, err = tx.Exec(ctx, `UPDATE campaign_recipients SET status=$2
		WHERE campaign_id=$1 AND status='QUEUED'`, id, queuedStatus)
	if err == nil {
		_, err = tx.Exec(ctx, `WITH stopped AS (
			UPDATE call_attempts a SET state='CANCELLED',outcome='cancelled',
			  ended_at=now(),updated_at=now() FROM campaign_recipients cr
			WHERE a.campaign_recipient_id=cr.id AND cr.campaign_id=$1
			  AND a.state='CLAIMED' RETURNING a.id,a.campaign_recipient_id
		)
		UPDATE campaign_recipients cr SET status=$2 FROM stopped s
		WHERE cr.id=s.campaign_recipient_id`, id, queuedStatus)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE dialer_slots SET attempt_id=NULL,leased_at=NULL
			WHERE attempt_id IN (SELECT a.id FROM call_attempts a
			JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
			WHERE cr.campaign_id=$1 AND a.state='CANCELLED')`, id)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE outbox SET state='CANCELLED',processed_at=now(),
			processing_at=NULL WHERE kind='ARI_ORIGINATE' AND state IN ('PENDING','PROCESSING')
			AND aggregate_id IN (SELECT a.id FROM call_attempts a
			JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
			WHERE cr.campaign_id=$1 AND a.state='CANCELLED')`, id)
	}
	if err != nil {
		return err
	}
	states := []string{
		"ORIGINATING", "RINGING", "ANSWERED", "MESSAGE_STARTED",
		"TERMINATING", "UNCERTAIN",
	}
	if err = terminateCampaignAttempts(ctx, tx, id, states); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, action, "campaign", id, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func terminateCampaignAttempts(
	ctx context.Context, tx pgx.Tx, campaignID string, states []string,
) error {
	rows, err := tx.Query(ctx, `SELECT a.id FROM call_attempts a
		JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		WHERE cr.campaign_id=$1 AND a.state=ANY($2) FOR UPDATE OF a`,
		campaignID, states)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	for _, id := range ids {
		if err == nil {
			err = requestTerminationTx(ctx, tx, id, "cancelled")
		}
	}
	return err
}
