package main

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

func (s *Store) ImportRecipients(
	ctx context.Context, campaignID, actor string, rows []ImportRow,
) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var state string
	if err = tx.QueryRow(ctx, "SELECT state FROM campaigns WHERE id=$1 FOR UPDATE", campaignID).
		Scan(&state); err != nil {
		return 0, dbError(err)
	}
	if state != "DRAFT" {
		return 0, errConflict
	}
	var imported int64
	for _, row := range rows {
		added, importErr := s.importRecipient(ctx, tx, campaignID, actor, row)
		if importErr != nil {
			return 0, importErr
		}
		if added {
			imported++
		}
	}
	detail, _ := json.Marshal(map[string]int64{"rows": int64(len(rows)), "inserted": imported})
	if err = audit(ctx, tx, actor, "recipients.import", "campaign", campaignID, detail); err != nil {
		return 0, err
	}
	return imported, tx.Commit(ctx)
}

func (s *Store) importRecipient(
	ctx context.Context, tx pgx.Tx, campaignID, actor string, row ImportRow,
) (bool, error) {
	hash := s.protector.LookupHash(row.Phone)
	ciphertext, err := s.protector.Encrypt(row.Phone)
	if err != nil {
		return false, err
	}
	recipientID, err := newUUID()
	if err != nil {
		return false, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO recipients(id,phone_cipher,phone_hash,timezone)
		VALUES($1,$2,$3,$4)
		ON CONFLICT(phone_hash) DO UPDATE SET phone_hash=EXCLUDED.phone_hash
		RETURNING id`, recipientID, ciphertext, hash, row.Timezone).Scan(&recipientID)
	if err != nil {
		return false, err
	}
	consentID, err := newUUID()
	if err != nil {
		return false, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO consent_evidence
		(id,recipient_id,consent_at,source,imported_by) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(recipient_id,consent_at,source)
		DO UPDATE SET source=EXCLUDED.source RETURNING id`,
		consentID, recipientID, row.ConsentAt, row.ConsentSource, actor).Scan(&consentID)
	if err != nil {
		return false, err
	}
	var suppressed bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM suppressions WHERE phone_hash=$1)", hash).
		Scan(&suppressed); err != nil {
		return false, err
	}
	status := "QUEUED"
	if suppressed {
		status = "SUPPRESSED"
	}
	campaignRecipientID, err := newUUID()
	if err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO campaign_recipients
		(id,campaign_id,recipient_id,consent_evidence_id,timezone,status)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(campaign_id,recipient_id) DO NOTHING`,
		campaignRecipientID, campaignID, recipientID, consentID, row.Timezone, status)
	return tag.RowsAffected() == 1, err
}
