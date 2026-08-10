package main

import (
	"context"
)

func (s *Store) PersistARIEvent(
	ctx context.Context, event ARIEvent, raw []byte,
) (attemptID, state string, inserted bool, err error) {
	channelID := event.ChannelID()
	if channelID == "" {
		return "", "", false, errNotFound
	}
	err = s.pool.QueryRow(ctx, "SELECT id,state FROM call_attempts WHERE channel_id=$1",
		channelID).Scan(&attemptID, &state)
	if err != nil {
		return "", "", false, dbError(err)
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO call_events
		(attempt_id,ari_event_id,event_type,raw) VALUES($1,$2,$3,$4)
		ON CONFLICT(attempt_id,ari_event_id) WHERE ari_event_id IS NOT NULL DO NOTHING`,
		attemptID, event.Key(raw), event.Type, raw)
	return attemptID, state, err == nil && tag.RowsAffected() == 1, err
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
