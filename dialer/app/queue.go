package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type QueuedAttempt struct {
	ID, ChannelID, CampaignRecipientID string
	AttemptNo                          int
	SlotNo                             int
}

type queueCandidate struct {
	ID, Timezone    string
	AttemptCount    int
	WindowStartSecs float64
	WindowEndSecs   float64
	DatabaseNow     time.Time
}

func (s *Store) AllocateAttempt(ctx context.Context) (*QueuedAttempt, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	candidate, err := claimCandidate(ctx, tx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	start := time.Duration(candidate.WindowStartSecs * float64(time.Second))
	end := time.Duration(candidate.WindowEndSecs * float64(time.Second))
	inside, windowErr := withinCallingWindow(candidate.DatabaseNow, candidate.Timezone, start, end)
	if windowErr != nil {
		_, err = tx.Exec(ctx, `UPDATE campaign_recipients SET status='QUARANTINED' WHERE id=$1`,
			candidate.ID)
		return nil, commitIfNoError(ctx, tx, err)
	}
	if !inside {
		next, nextErr := nextCallingWindow(candidate.DatabaseNow, candidate.Timezone, start)
		if nextErr != nil {
			return nil, nextErr
		}
		_, err = tx.Exec(ctx, "UPDATE campaign_recipients SET next_attempt_at=$2 WHERE id=$1",
			candidate.ID, next)
		return nil, commitIfNoError(ctx, tx, err)
	}
	var slot int
	err = tx.QueryRow(ctx, `SELECT slot_no FROM dialer_slots
		WHERE slot_no<=$1 AND attempt_id IS NULL ORDER BY slot_no
		FOR UPDATE SKIP LOCKED LIMIT 1`, s.config.MaxConcurrency).Scan(&slot)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	allowed, nextToken, err := takeCPSToken(ctx, tx, s.config.CPS)
	if err != nil {
		return nil, err
	}
	if !allowed {
		_, err = tx.Exec(ctx, `UPDATE campaign_recipients
			SET next_attempt_at=GREATEST(next_attempt_at,$2) WHERE id=$1`,
			candidate.ID, nextToken)
		return nil, commitIfNoError(ctx, tx, err)
	}
	attemptNo := candidate.AttemptCount + 1
	attemptID := attemptUUID(candidate.ID, attemptNo)
	channelID := "dialer-" + strings.ReplaceAll(attemptID, "-", "")
	_, err = tx.Exec(ctx, `INSERT INTO call_attempts
		(id,campaign_recipient_id,attempt_no,channel_id,slot_no,state)
		VALUES($1,$2,$3,$4,$5,'CLAIMED')`,
		attemptID, candidate.ID, attemptNo, channelID, slot)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE dialer_slots SET attempt_id=$2,leased_at=now()
			WHERE slot_no=$1`, slot, attemptID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE campaign_recipients SET status='ACTIVE',
			attempt_count=$2 WHERE id=$1`, candidate.ID, attemptNo)
	}
	if err == nil {
		payload, _ := json.Marshal(map[string]string{"attempt_id": attemptID})
		_, err = tx.Exec(ctx, `INSERT INTO outbox(id,aggregate_id,kind,payload)
			VALUES($1,$2,'ARI_ORIGINATE',$3)`,
			attemptUUID(attemptID, 0), attemptID, payload)
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &QueuedAttempt{attemptID, channelID, candidate.ID, attemptNo, slot}, nil
}

func claimCandidate(ctx context.Context, tx pgx.Tx) (queueCandidate, error) {
	var c queueCandidate
	err := tx.QueryRow(ctx, `SELECT cr.id,cr.timezone,cr.attempt_count,
		EXTRACT(EPOCH FROM c.window_start),EXTRACT(EPOCH FROM c.window_end),clock_timestamp()
		FROM campaign_recipients cr JOIN campaigns c ON c.id=cr.campaign_id
		JOIN recipients r ON r.id=cr.recipient_id
		JOIN consent_evidence ce ON ce.id=cr.consent_evidence_id
		JOIN caller_ids ci ON ci.id=c.caller_id_id
		JOIN campaign_approvals ca ON ca.campaign_id=c.id
		  AND ca.message_asset_id=c.message_asset_id AND ca.caller_id_id=c.caller_id_id
		WHERE c.state='RUNNING' AND cr.status='QUEUED' AND cr.next_attempt_at<=clock_timestamp()
		  AND cr.attempt_count<3 AND c.dnc_attested_at>=clock_timestamp()-interval '31 days'
		  AND c.dnc_attested_at<=clock_timestamp()+interval '5 minutes'
		  AND ce.consent_at<=clock_timestamp()+interval '5 minutes' AND ce.source<>''
		  AND ci.authorized_at<=clock_timestamp()+interval '5 minutes'
		  AND NOT EXISTS(SELECT 1 FROM suppressions sp WHERE sp.phone_hash=r.phone_hash)
		ORDER BY cr.next_attempt_at,cr.id FOR UPDATE OF cr SKIP LOCKED LIMIT 1`).
		Scan(&c.ID, &c.Timezone, &c.AttemptCount, &c.WindowStartSecs,
			&c.WindowEndSecs, &c.DatabaseNow)
	return c, err
}

func commitIfNoError(ctx context.Context, tx pgx.Tx, err error) error {
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
