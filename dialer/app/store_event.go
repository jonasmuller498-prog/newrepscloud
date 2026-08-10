package main

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func (s *Store) ApplyARIEvent(
	ctx context.Context, event ARIEvent, raw []byte,
) (attemptID string, inserted bool, err error) {
	return s.ApplyARIEventWithKey(ctx, event, event.Key(raw))
}

func (s *Store) ApplyARIEventWithKey(
	ctx context.Context, event ARIEvent, eventKey string,
) (attemptID string, inserted bool, err error) {
	if !validEventKey(eventKey) {
		return "", false, errUnsafeARIEvent
	}
	channelID := event.ChannelID()
	if channelID == "" {
		return "", false, errNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)
	var recipientID string
	err = tx.QueryRow(ctx, `SELECT cr.id FROM call_attempts a
		JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		WHERE a.channel_id=$1 FOR UPDATE OF cr`, channelID).Scan(&recipientID)
	if err != nil {
		return "", false, dbError(err)
	}
	err = tx.QueryRow(ctx, `SELECT id FROM call_attempts
		WHERE channel_id=$1 FOR UPDATE`, channelID).Scan(&attemptID)
	if err != nil {
		return "", false, dbError(err)
	}
	tag, err := tx.Exec(ctx, `INSERT INTO call_events
		(attempt_id,ari_event_id,event_type,raw) VALUES($1,$2,$3,$4)
		ON CONFLICT(attempt_id,ari_event_id) WHERE ari_event_id IS NOT NULL DO NOTHING`,
		attemptID, eventKey, event.Type, event.SafeJSON())
	if err != nil {
		return "", false, err
	}
	if tag.RowsAffected() == 0 {
		return attemptID, false, tx.Commit(ctx)
	}
	if err = s.projectARIEventTx(ctx, tx, attemptID, event); err != nil {
		return "", false, err
	}
	_, err = tx.Exec(ctx, `UPDATE call_events SET processed_at=now()
		WHERE attempt_id=$1 AND ari_event_id=$2`,
		attemptID, eventKey)
	if err != nil {
		return "", false, err
	}
	return attemptID, true, tx.Commit(ctx)
}

func (s *Store) ListEvents(ctx context.Context, campaignID string) ([]CallEvent, error) {
	var campaign any
	if campaignID != "" {
		campaign = campaignID
	}
	rows, err := s.pool.Query(ctx, `SELECT e.id,e.attempt_id,e.event_type,r.phone_cipher,e.created_at
		FROM call_events e JOIN call_attempts a ON a.id=e.attempt_id
		JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		JOIN recipients r ON r.id=cr.recipient_id
		WHERE ($1::uuid IS NULL OR cr.campaign_id=$1::uuid)
		ORDER BY e.created_at DESC LIMIT 500`, campaign)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []CallEvent
	for rows.Next() {
		var event CallEvent
		var ciphertext []byte
		if err = rows.Scan(&event.ID, &event.AttemptID, &event.Type,
			&ciphertext, &event.CreatedAt); err != nil {
			return nil, err
		}
		phone, decryptErr := s.protector.Decrypt(ciphertext)
		if decryptErr != nil {
			return nil, decryptErr
		}
		event.MaskedPhone = maskPhone(phone)
		result = append(result, event)
	}
	return result, rows.Err()
}
