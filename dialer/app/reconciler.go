package main

import (
	"context"
	"log/slog"
	"time"
)

type Reconciler struct {
	store *Store
	gate  *DependencyGate
	log   *slog.Logger
}

func (r *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		r.gate.mediaReady.Store(checkMediaDirectory(r.store.config.MediaDir))
		if err := r.reconcile(ctx); err != nil && ctx.Err() == nil {
			r.log.Error("reconciliation failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Reconciler) reconcile(ctx context.Context) error {
	tx, err := r.store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `WITH stale AS (
		UPDATE call_attempts a SET state='AMBIGUOUS',outcome='ambiguous',
		  ended_at=now(),updated_at=now() FROM outbox o
		WHERE o.aggregate_id=a.id AND o.state='PROCESSING'
		  AND o.processing_at<now()-interval '2 minutes'
		  AND a.state='CLAIMED' RETURNING a.id,a.campaign_recipient_id
	), quarantined AS (
		UPDATE campaign_recipients cr SET status='QUARANTINED'
		FROM stale s WHERE cr.id=s.campaign_recipient_id RETURNING s.id
	)
	UPDATE outbox o SET state='DONE',processed_at=now(),processing_at=NULL
	FROM quarantined q WHERE o.aggregate_id=q.id`)
	if err == nil {
		_, err = tx.Exec(ctx, `WITH stale AS (
			UPDATE call_attempts SET state='AMBIGUOUS',outcome='ambiguous',
			  ended_at=now(),updated_at=now()
			WHERE state IN ('ORIGINATING','RINGING') AND updated_at<now()-interval '2 hours'
			RETURNING id,campaign_recipient_id
		)
		UPDATE campaign_recipients cr SET status='QUARANTINED'
		FROM stale s WHERE cr.id=s.campaign_recipient_id`)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `WITH stale AS (
			UPDATE call_attempts SET state='COMPLETED',outcome='completed',
			  ended_at=now(),updated_at=now()
			WHERE state IN ('ANSWERED','MESSAGE_STARTED') AND updated_at<now()-interval '2 hours'
			RETURNING id,campaign_recipient_id
		)
		UPDATE campaign_recipients cr SET status='SUCCEEDED'
		FROM stale s WHERE cr.id=s.campaign_recipient_id`)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE dialer_slots ds SET attempt_id=NULL,leased_at=NULL
			WHERE attempt_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM call_attempts a
			WHERE a.id=ds.attempt_id AND a.state IN
			('CLAIMED','ORIGINATING','RINGING','ANSWERED','MESSAGE_STARTED'))`)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE campaigns c SET state='CANCELLED',updated_at=now()
			WHERE state='DRAINING' AND NOT EXISTS(SELECT 1 FROM campaign_recipients cr
			JOIN call_attempts a ON a.campaign_recipient_id=cr.id
			WHERE cr.campaign_id=c.id AND a.state IN
			('CLAIMED','ORIGINATING','RINGING','ANSWERED','MESSAGE_STARTED'))`)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE campaigns c SET state='COMPLETED',updated_at=now()
			WHERE state='RUNNING' AND EXISTS(SELECT 1 FROM campaign_recipients cr
			WHERE cr.campaign_id=c.id) AND NOT EXISTS(SELECT 1 FROM campaign_recipients cr
			WHERE cr.campaign_id=c.id AND cr.status IN ('QUEUED','ACTIVE'))`)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
