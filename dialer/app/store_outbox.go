package main

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5"
)

type OutboxItem struct {
	ID, AttemptID, Kind string
}

func (s *Store) ClaimOutbox(ctx context.Context) (*OutboxItem, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var item OutboxItem
	err = tx.QueryRow(ctx, `SELECT o.id,o.aggregate_id,o.kind FROM outbox o
		JOIN call_attempts a ON a.id=o.aggregate_id
		JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		JOIN campaigns c ON c.id=cr.campaign_id
		WHERE o.state='PENDING' AND o.available_at<=clock_timestamp() AND (
		  (o.kind='ARI_ORIGINATE' AND a.state='CLAIMED' AND c.state='RUNNING') OR
		  (o.kind='ARI_PLAY' AND a.state='ANSWERED' AND a.pending_outcome IS NULL) OR
		  (o.kind='ARI_HANGUP' AND a.state IN ('TERMINATING','UNCERTAIN'))
		)
		ORDER BY CASE o.kind WHEN 'ARI_HANGUP' THEN 0 WHEN 'ARI_PLAY' THEN 1 ELSE 2 END,
		  o.available_at,o.created_at FOR UPDATE OF o SKIP LOCKED LIMIT 1`).
		Scan(&item.ID, &item.AttemptID, &item.Kind)
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

func (s *Store) ResetOutbox(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET state='PENDING',processing_at=NULL
		WHERE id=$1 AND state='PROCESSING'`, id)
	return err
}

func (s *Store) CompleteOriginate(
	ctx context.Context, item OutboxItem, result OriginateResult,
) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var state string
	var pending sql.NullString
	if err = tx.QueryRow(ctx, `SELECT state,pending_outcome FROM call_attempts WHERE id=$1
		FOR UPDATE`, item.AttemptID).Scan(&state, &pending); err != nil {
		return false, dbError(err)
	}
	compensate := false
	if result.Accepted {
		compensate = state == "TERMINATING"
		if compensate {
			err = enqueueARIActionTx(ctx, tx, item.AttemptID, "ARI_HANGUP")
		}
	} else if result.Uncertain {
		_, err = tx.Exec(ctx, `UPDATE call_attempts SET state='UNCERTAIN',
			outcome='ambiguous',uncertain_at=COALESCE(uncertain_at,now()),updated_at=now()
			WHERE id=$1 AND state IN ('ORIGINATING','TERMINATING')`, item.AttemptID)
	} else {
		outcome := result.Outcome
		if pending.Valid {
			outcome = pending.String
		}
		err = s.finishAttemptTx(ctx, tx, item.AttemptID, outcome)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE outbox SET state='DONE',processed_at=now(),processing_at=NULL
			WHERE id=$1`, item.ID)
	}
	if err != nil {
		return false, err
	}
	return compensate, tx.Commit(ctx)
}
