package main

import (
	"context"
	"database/sql"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Store) projectARIEventTx(
	ctx context.Context, tx pgx.Tx, attemptID string, event ARIEvent,
) error {
	if event.Type == "ChannelDtmfReceived" && event.Digit == "9" {
		return s.optOutAttemptTx(ctx, tx, attemptID, "ari_dtmf_9",
			"ari:"+s.config.ARIApp)
	}
	switch event.Type {
	case "StasisStart":
		state := "RINGING"
		if strings.EqualFold(event.Channel.State, "Up") {
			state = "ANSWERED"
		}
		_, err := tx.Exec(ctx, `UPDATE call_attempts SET
			stasis_started_at=COALESCE(stasis_started_at,now()),
			state=CASE WHEN $2='ANSWERED' AND state IN ('CLAIMED','ORIGINATING','RINGING')
			  THEN 'ANSWERED' WHEN $2='RINGING' AND state IN ('CLAIMED','ORIGINATING')
			  THEN 'RINGING' ELSE state END,
			answered_at=CASE WHEN $2='ANSWERED' THEN COALESCE(answered_at,now())
			  ELSE answered_at END,updated_at=now() WHERE id=$1`, attemptID, state)
		if err == nil && state == "ANSWERED" {
			err = queuePlaybackTx(ctx, tx, attemptID)
		}
		return err
	case "ChannelStateChange":
		if !strings.EqualFold(event.Channel.State, "Up") {
			return nil
		}
		_, err := tx.Exec(ctx, `UPDATE call_attempts SET
			state=CASE WHEN state IN ('CLAIMED','ORIGINATING','RINGING')
			  THEN 'ANSWERED' ELSE state END,
			answered_at=COALESCE(answered_at,now()),updated_at=now()
			WHERE id=$1`, attemptID)
		if err == nil {
			err = queuePlaybackTx(ctx, tx, attemptID)
		}
		return err
	case "PlaybackStarted":
		_, err := tx.Exec(ctx, `UPDATE call_attempts SET
			state=CASE WHEN state='ANSWERED' THEN 'MESSAGE_STARTED' ELSE state END,
			playback_started_at=COALESCE(playback_started_at,now()),updated_at=now()
			WHERE id=$1 AND playback_id=$2`, attemptID, event.Playback.ID)
		return err
	case "PlaybackFinished":
		tag, err := tx.Exec(ctx, `UPDATE call_attempts SET
			playback_finished_at=COALESCE(playback_finished_at,now()),updated_at=now()
			WHERE id=$1 AND playback_id=$2`, attemptID, event.Playback.ID)
		if err == nil && tag.RowsAffected() == 1 {
			err = requestTerminationTx(ctx, tx, attemptID, "completed")
		}
		return err
	case "StasisEnd":
		_, err := tx.Exec(ctx, `UPDATE call_attempts SET stasis_ended_at=now(),
			updated_at=now() WHERE id=$1`, attemptID)
		if err != nil {
			return err
		}
		var pending sql.NullString
		if err = tx.QueryRow(ctx, `SELECT pending_outcome FROM call_attempts
			WHERE id=$1`, attemptID).Scan(&pending); err != nil {
			return err
		}
		if pending.Valid {
			return s.finishAttemptTx(ctx, tx, attemptID, pending.String)
		}
		return nil
	case "ChannelDestroyed":
		_, err := tx.Exec(ctx, `UPDATE call_attempts SET destroyed_at=now(),
			updated_at=now() WHERE id=$1`, attemptID)
		if err != nil {
			return err
		}
		var state string
		var pending sql.NullString
		err = tx.QueryRow(ctx, `SELECT state,pending_outcome FROM call_attempts
			WHERE id=$1`, attemptID).Scan(&state, &pending)
		if err != nil {
			return err
		}
		outcome := destroyedOutcome(event, state)
		if pending.Valid {
			outcome = pending.String
		}
		return s.finishAttemptTx(ctx, tx, attemptID, outcome)
	}
	return nil
}

func queuePlaybackTx(ctx context.Context, tx pgx.Tx, attemptID string) error {
	var eligible bool
	err := tx.QueryRow(ctx, `SELECT state='ANSWERED' AND stasis_started_at IS NOT NULL
		AND pending_outcome IS NULL FROM call_attempts WHERE id=$1`, attemptID).
		Scan(&eligible)
	if err != nil || !eligible {
		return err
	}
	if _, err = setPlaybackIDTx(ctx, tx, attemptID); err != nil {
		return err
	}
	return enqueueARIActionTx(ctx, tx, attemptID, "ARI_PLAY")
}
