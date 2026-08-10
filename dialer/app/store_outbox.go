package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
)

type OutboxItem struct {
	ID, AttemptID string
}

func (s *Store) ClaimOutbox(ctx context.Context) (*OutboxItem, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var item OutboxItem
	err = tx.QueryRow(ctx, `SELECT o.id,o.aggregate_id FROM outbox o
		JOIN call_attempts a ON a.id=o.aggregate_id
		JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		JOIN campaigns c ON c.id=cr.campaign_id
		WHERE o.state='PENDING' AND o.kind='ARI_ORIGINATE'
		AND a.state='CLAIMED' AND c.state='RUNNING'
		ORDER BY o.created_at FOR UPDATE OF o SKIP LOCKED LIMIT 1`).
		Scan(&item.ID, &item.AttemptID)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE outbox SET state='PROCESSING',processing_at=now()
		WHERE id=$1`, item.ID); err != nil {
		return nil, err
	}
	return &item, tx.Commit(ctx)
}

func (s *Store) LoadOriginateCommand(ctx context.Context, attemptID string) (OriginateCommand, error) {
	var cmd OriginateCommand
	var phoneCipher, callerCipher []byte
	err := s.pool.QueryRow(ctx, `SELECT a.id,a.channel_id,r.phone_cipher,ci.phone_cipher,
		ma.storage_name FROM call_attempts a
		JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		JOIN campaigns c ON c.id=cr.campaign_id
		JOIN recipients r ON r.id=cr.recipient_id
		JOIN consent_evidence ce ON ce.id=cr.consent_evidence_id
		JOIN caller_ids ci ON ci.id=c.caller_id_id
		JOIN message_assets ma ON ma.id=c.message_asset_id
		JOIN campaign_approvals ca ON ca.campaign_id=c.id
		  AND ca.message_asset_id=ma.id AND ca.caller_id_id=ci.id
		WHERE a.id=$1 AND a.state='CLAIMED' AND cr.status='ACTIVE' AND c.state='RUNNING'
		  AND c.dnc_attested_at>=clock_timestamp()-interval '31 days'
		  AND ce.consent_at<=clock_timestamp()+interval '5 minutes'
		  AND NOT EXISTS(SELECT 1 FROM suppressions sp WHERE sp.phone_hash=r.phone_hash)`,
		attemptID).Scan(&cmd.AttemptID, &cmd.ChannelID, &phoneCipher, &callerCipher, &cmd.Media)
	if err != nil {
		return cmd, dbError(err)
	}
	cmd.Phone, err = s.protector.Decrypt(phoneCipher)
	if err == nil {
		cmd.CallerID, err = s.protector.Decrypt(callerCipher)
	}
	if err != nil {
		return cmd, err
	}
	if _, err = os.Stat(filepath.Join(s.config.MediaDir, filepath.Base(cmd.Media))); err != nil {
		return cmd, err
	}
	return cmd, nil
}

func (s *Store) ResetOutbox(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET state='PENDING',processing_at=NULL
		WHERE id=$1 AND state='PROCESSING'`, id)
	return err
}

func (s *Store) CompleteOriginate(
	ctx context.Context, item OutboxItem, result OriginateResult,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if result.Accepted {
		_, err = tx.Exec(ctx, `UPDATE call_attempts SET state='ORIGINATING',updated_at=now()
			WHERE id=$1 AND state='CLAIMED'`, item.AttemptID)
	} else {
		err = s.finishAttemptTx(ctx, tx, item.AttemptID, result.Outcome)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE outbox SET state='DONE',processed_at=now(),processing_at=NULL
			WHERE id=$1`, item.ID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
