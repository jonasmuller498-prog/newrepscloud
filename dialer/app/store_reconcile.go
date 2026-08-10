package main

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5"
)

type ReconcileAttempt struct {
	AttemptID, ChannelID string
}

func (s *Store) ReconcileDatabase(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `UPDATE import_jobs SET state='PENDING',processing_at=NULL,
		available_at=now() WHERE state='PROCESSING'
		AND processing_at<now()-interval '5 minutes'`)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE outbox o SET state='PENDING',processing_at=NULL,
		available_at=now() FROM call_attempts a WHERE o.aggregate_id=a.id
			AND o.state='PROCESSING' AND (o.processing_at IS NULL
			  OR o.processing_at<now()-interval '2 minutes')
		AND ((o.kind='ARI_ORIGINATE' AND a.state='CLAIMED')
		  OR (o.kind='ARI_PLAY' AND a.state='ANSWERED')
		  OR (o.kind='ARI_HANGUP' AND a.state IN ('TERMINATING','UNCERTAIN')))`)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `WITH stopped AS (
			UPDATE call_attempts a SET
			  state=CASE WHEN sp.phone_hash IS NULL THEN 'CANCELLED' ELSE 'SUPPRESSED' END,
			  outcome=CASE WHEN sp.phone_hash IS NULL THEN 'cancelled' ELSE 'suppressed' END,
			  ended_at=now(),updated_at=now()
			FROM campaign_recipients cr JOIN campaigns c ON c.id=cr.campaign_id
			JOIN recipients r ON r.id=cr.recipient_id
			LEFT JOIN suppressions sp ON sp.phone_hash=r.phone_hash
			WHERE a.campaign_recipient_id=cr.id AND a.state='CLAIMED'
			  AND (c.state<>'RUNNING' OR sp.phone_hash IS NOT NULL)
			RETURNING a.id,a.campaign_recipient_id
		)
		UPDATE campaign_recipients cr SET status=CASE
		  WHEN EXISTS(SELECT 1 FROM suppressions sp JOIN recipients r
		    ON r.phone_hash=sp.phone_hash WHERE r.id=cr.recipient_id) THEN 'SUPPRESSED'
		  WHEN (SELECT state FROM campaigns WHERE id=cr.campaign_id)='PAUSED' THEN 'QUEUED'
		  ELSE 'CANCELLED' END FROM stopped s WHERE cr.id=s.campaign_recipient_id`)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE outbox o SET state='CANCELLED',processed_at=now(),
			processing_at=NULL FROM call_attempts a WHERE o.aggregate_id=a.id
			AND o.state IN ('PENDING','PROCESSING') AND a.state NOT IN
			('CLAIMED','ORIGINATING','RINGING','ANSWERED','MESSAGE_STARTED',
			 'TERMINATING','UNCERTAIN')`)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE dialer_slots ds SET attempt_id=NULL,leased_at=NULL
			WHERE attempt_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM call_attempts a
			WHERE a.id=ds.attempt_id AND a.state IN
			('CLAIMED','ORIGINATING','RINGING','ANSWERED','MESSAGE_STARTED',
			 'TERMINATING','UNCERTAIN'))`)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE campaigns c SET state='CANCELLED',updated_at=now()
			WHERE state='DRAINING' AND NOT EXISTS(SELECT 1 FROM campaign_recipients cr
			JOIN call_attempts a ON a.campaign_recipient_id=cr.id
			WHERE cr.campaign_id=c.id AND a.state IN
			('CLAIMED','ORIGINATING','RINGING','ANSWERED','MESSAGE_STARTED',
			 'TERMINATING','UNCERTAIN'))`)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE campaigns c SET state='COMPLETED',updated_at=now()
			WHERE state='RUNNING' AND EXISTS(SELECT 1 FROM campaign_recipients cr
			WHERE cr.campaign_id=c.id) AND NOT EXISTS(SELECT 1 FROM campaign_recipients cr
			WHERE cr.campaign_id=c.id AND cr.status IN ('QUEUED','ACTIVE'))`)
	}
	return commitResult(ctx, tx, err)
}

func (s *Store) ReconcileCandidates(ctx context.Context) ([]ReconcileAttempt, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,channel_id FROM call_attempts WHERE
		state='UNCERTAIN'
		OR (state='TERMINATING' AND termination_requested_at<now()-interval '15 seconds')
		OR (state IN ('ORIGINATING','RINGING','ANSWERED','MESSAGE_STARTED')
		  AND updated_at<now()-interval '2 hours')
		ORDER BY updated_at LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ReconcileAttempt
	for rows.Next() {
		var item ReconcileAttempt
		if err = rows.Scan(&item.AttemptID, &item.ChannelID); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) RequestReconcileHangup(ctx context.Context, attemptID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = requestTerminationTx(ctx, tx, attemptID, "ambiguous"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ConfirmChannelAbsent(ctx context.Context, attemptID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var pending sql.NullString
	if err = tx.QueryRow(ctx, `SELECT pending_outcome FROM call_attempts
		WHERE id=$1 FOR UPDATE`, attemptID).Scan(&pending); err != nil {
		return dbError(err)
	}
	outcome := "ambiguous"
	if pending.Valid {
		outcome = pending.String
	}
	if err = s.finishAttemptTx(ctx, tx, attemptID, outcome); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
