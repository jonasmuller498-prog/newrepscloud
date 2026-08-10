package main

import "context"

func (s *Store) ListAttempts(ctx context.Context, campaignID string) ([]Attempt, error) {
	var campaign any
	if campaignID != "" {
		campaign = campaignID
	}
	rows, err := s.pool.Query(ctx, `SELECT a.id,a.campaign_recipient_id,cr.campaign_id,
		r.phone_cipher,a.attempt_no,a.state,a.outcome,a.channel_id,a.created_at,a.ended_at
		FROM call_attempts a JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		JOIN recipients r ON r.id=cr.recipient_id
		WHERE ($1::uuid IS NULL OR cr.campaign_id=$1::uuid)
		ORDER BY a.created_at DESC LIMIT 500`, campaign)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Attempt
	for rows.Next() {
		var item Attempt
		var ciphertext []byte
		if err = rows.Scan(&item.ID, &item.CampaignRecipientID, &item.CampaignID,
			&ciphertext, &item.AttemptNo, &item.State, &item.Outcome, &item.ChannelID,
			&item.CreatedAt, &item.EndedAt); err != nil {
			return nil, err
		}
		phone, decryptErr := s.protector.Decrypt(ciphertext)
		if decryptErr != nil {
			return nil, decryptErr
		}
		item.MaskedPhone = maskPhone(phone)
		result = append(result, item)
	}
	return result, rows.Err()
}
