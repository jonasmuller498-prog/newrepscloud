package main

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
)

var activeAttemptStates = map[string]bool{
	"CLAIMED": true, "ORIGINATING": true, "RINGING": true,
	"ANSWERED": true, "MESSAGE_STARTED": true,
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
	var attemptNo int
	err := tx.QueryRow(ctx, `SELECT a.campaign_recipient_id,a.attempt_no,a.state,c.state
		FROM call_attempts a JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		JOIN campaigns c ON c.id=cr.campaign_id WHERE a.id=$1 FOR UPDATE OF a,cr`,
		attemptID).Scan(&campaignRecipientID, &attemptNo, &state, &campaignState)
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
	default:
		if !retryableOutcome(outcome) {
			outcome, dbState = "ambiguous", "AMBIGUOUS"
		}
	}
	_, err = tx.Exec(ctx, `UPDATE call_attempts SET state=$2,outcome=$3,
		ended_at=now(),updated_at=now() WHERE id=$1`, attemptID, dbState, outcome)
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
	} else if retryableOutcome(outcome) && attemptNo < 3 && campaignState != "DRAINING" {
		status = "QUEUED"
	}
	if status == "QUEUED" {
		_, err = tx.Exec(ctx, `UPDATE campaign_recipients SET status=$2,
			next_attempt_at=clock_timestamp()+($3 * interval '5 minutes')
			WHERE id=$1`, campaignRecipientID, status, attemptNo)
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
	_, err := s.pool.Exec(ctx, `UPDATE call_attempts SET state=$2,updated_at=now()
		WHERE id=$1 AND state IN ('CLAIMED','ORIGINATING','RINGING','ANSWERED','MESSAGE_STARTED')`,
		attemptID, state)
	return err
}
