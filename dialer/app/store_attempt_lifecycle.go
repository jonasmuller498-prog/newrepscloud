package main

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"
)

var activeAttemptStates = map[string]bool{
	"CLAIMED": true, "ORIGINATING": true, "RINGING": true,
	"ANSWERED": true, "MESSAGE_STARTED": true,
	"TERMINATING": true, "UNCERTAIN": true,
}

func (s *Store) FinishAttempt(ctx context.Context, attemptID, outcome string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = s.finishAttemptTx(ctx, tx, attemptID, outcome); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) finishAttemptTx(
	ctx context.Context, tx pgx.Tx, attemptID, outcome string,
) error {
	var campaignRecipientID, state, campaignState string
	var attemptCount int
	var answered, messageStarted bool
	err := tx.QueryRow(ctx, `SELECT a.campaign_recipient_id,cr.attempt_count,a.state,c.state
		,a.answered_at IS NOT NULL,a.playback_started_at IS NOT NULL
		FROM call_attempts a JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		JOIN campaigns c ON c.id=cr.campaign_id WHERE a.id=$1 FOR UPDATE OF a,cr`,
		attemptID).Scan(&campaignRecipientID, &attemptCount, &state, &campaignState,
		&answered, &messageStarted)
	if err != nil {
		return dbError(err)
	}
	if !activeAttemptStates[state] {
		return nil
	}
	dbState := strings.ToUpper(outcome)
	switch outcome {
	case "completed":
		dbState = "COMPLETED"
	case "no_answer":
		dbState = "NO_ANSWER"
	case "temporary":
		dbState = "TEMPORARY"
	case "ambiguous":
		dbState = "AMBIGUOUS"
	case "invalid":
		dbState = "INVALID"
	case "forbidden":
		dbState = "FORBIDDEN"
	case "opt_out":
		dbState = "OPT_OUT"
	case "suppressed":
		dbState = "SUPPRESSED"
	case "cancelled":
		dbState = "CANCELLED"
	default:
		if !retryableOutcome(outcome) {
			outcome, dbState = "ambiguous", "AMBIGUOUS"
		}
	}
	_, err = tx.Exec(ctx, `UPDATE call_attempts SET state=$2,outcome=$3,
		pending_outcome=NULL,ended_at=now(),updated_at=now() WHERE id=$1`,
		attemptID, dbState, outcome)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE dialer_slots SET attempt_id=NULL,leased_at=NULL
			WHERE attempt_id=$1`, attemptID)
	}
	if err != nil {
		return err
	}
	status := "FAILED"
	if outcome == "completed" {
		status = "SUCCEEDED"
	} else if outcome == "ambiguous" {
		status = "QUARANTINED"
	} else if outcome == "opt_out" || outcome == "suppressed" {
		status = "SUPPRESSED"
	} else if outcome == "cancelled" {
		status = "CANCELLED"
		if campaignState == "PAUSED" && !answered && !messageStarted {
			status = "QUEUED"
		} else if campaignState == "PAUSED" {
			status = "QUARANTINED"
		}
	} else if retryableOutcome(outcome) && attemptCount < 3 &&
		(campaignState == "RUNNING" || campaignState == "PAUSED") &&
		!answered && !messageStarted {
		status = "QUEUED"
	}
	if status == "QUEUED" {
		_, err = tx.Exec(ctx, `UPDATE campaign_recipients SET status=$2,
			next_attempt_at=clock_timestamp()+($3 * interval '5 minutes')
			WHERE id=$1`, campaignRecipientID, status, attemptCount)
	} else {
		_, err = tx.Exec(ctx, "UPDATE campaign_recipients SET status=$2 WHERE id=$1",
			campaignRecipientID, status)
	}
	return err
}

func (s *Store) UpdateAttemptState(ctx context.Context, attemptID, state string) error {
	if !activeAttemptStates[state] || state == "CLAIMED" {
		return errConflict
	}
	_, err := s.pool.Exec(ctx, `UPDATE call_attempts SET state=$2,
		answered_at=CASE WHEN $2='ANSWERED' THEN COALESCE(answered_at,now()) ELSE answered_at END,
		playback_started_at=CASE WHEN $2='MESSAGE_STARTED'
		  THEN COALESCE(playback_started_at,now()) ELSE playback_started_at END,updated_at=now()
		WHERE id=$1 AND (
		  ($2='RINGING' AND state IN ('CLAIMED','ORIGINATING')) OR
		  ($2='ANSWERED' AND state IN ('CLAIMED','ORIGINATING','RINGING')) OR
		  ($2='MESSAGE_STARTED' AND state IN
		    ('CLAIMED','ORIGINATING','RINGING','ANSWERED','MESSAGE_STARTED'))
		)`,
		attemptID, state)
	return err
}

func (s *Store) MarkActiveUncertain(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE call_attempts SET state='UNCERTAIN',
		outcome='ambiguous',uncertain_at=COALESCE(uncertain_at,now()),updated_at=now()
		WHERE state IN ('ORIGINATING','RINGING','ANSWERED','MESSAGE_STARTED','TERMINATING')`)
	return err
}

func (s *Store) AttemptTerminationPending(ctx context.Context, attemptID string) (bool, error) {
	var pending bool
	err := s.pool.QueryRow(ctx, `SELECT state IN ('TERMINATING','UNCERTAIN')
		AND pending_outcome IS NOT NULL FROM call_attempts WHERE id=$1`,
		attemptID).Scan(&pending)
	return pending, dbError(err)
}

func requestTerminationTx(
	ctx context.Context, tx pgx.Tx, attemptID, outcome string,
) error {
	tag, err := tx.Exec(ctx, `UPDATE call_attempts SET state='TERMINATING',
		pending_outcome=CASE
		  WHEN $2='opt_out' THEN 'opt_out'
		  WHEN pending_outcome='opt_out' THEN pending_outcome
		  WHEN $2='suppressed' THEN 'suppressed'
		  WHEN pending_outcome IN ('suppressed','completed','ambiguous') THEN pending_outcome
		  ELSE $2 END,
		termination_requested_at=COALESCE(termination_requested_at,now()),
		updated_at=now() WHERE id=$1 AND state IN
		('ORIGINATING','RINGING','ANSWERED','MESSAGE_STARTED','UNCERTAIN','TERMINATING')`,
		attemptID, outcome)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	return enqueueARIActionTx(ctx, tx, attemptID, "ARI_HANGUP")
}

func enqueueARIActionTx(ctx context.Context, tx pgx.Tx, attemptID, kind string) error {
	sequence := 0
	switch kind {
	case "ARI_PLAY":
		sequence = 1
	case "ARI_HANGUP":
		sequence = 2
	}
	payload, _ := json.Marshal(map[string]string{"attempt_id": attemptID})
	_, err := tx.Exec(ctx, `INSERT INTO outbox(id,aggregate_id,kind,payload)
		VALUES($1,$2,$3,$4) ON CONFLICT(aggregate_id,kind) DO UPDATE SET
		state=CASE WHEN outbox.state IN ('DONE','CANCELLED')
		  THEN 'PENDING' ELSE outbox.state END,
		available_at=CASE WHEN outbox.state='PROCESSING'
		  THEN outbox.available_at ELSE now() END,
		processing_at=CASE WHEN outbox.state='PROCESSING'
		  THEN COALESCE(outbox.processing_at,now()) ELSE NULL END,
		processed_at=CASE WHEN outbox.state='PROCESSING'
		  THEN outbox.processed_at ELSE NULL END`,
		attemptUUID(attemptID, sequence), attemptID, kind, payload)
	return err
}
