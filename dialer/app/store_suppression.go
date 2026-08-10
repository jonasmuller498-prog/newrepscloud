package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type SuppressionView struct {
	Phone     string    `json:"phone"`
	Reason    string    `json:"reason"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) SuppressPhone(ctx context.Context, phone, reason, source, actor string) error {
	normalized, err := normalizeE164(phone)
	reason, source = strings.TrimSpace(reason), strings.TrimSpace(source)
	if err != nil || reason == "" || source == "" || len(reason) > 200 || len(source) > 100 {
		return errSafetyBlocked
	}
	ciphertext, err := s.protector.Encrypt(normalized)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	hash := s.protector.LookupHash(normalized)
	if err = s.suppressTx(ctx, tx, hash, ciphertext, reason, source, actor); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]string{"phone": maskPhone(normalized), "reason": reason})
	if err = audit(ctx, tx, actor, "suppression.create", "phone_hash",
		jsonHash(hash), detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) suppressTx(
	ctx context.Context, tx pgx.Tx, hash, ciphertext []byte, reason, source, actor string,
) error {
	_, err := tx.Exec(ctx, `INSERT INTO suppressions
		(phone_hash,phone_cipher,reason,source,created_by) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(phone_hash) DO NOTHING`, hash, ciphertext, reason, source, actor)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE campaign_recipients cr SET status='SUPPRESSED'
		FROM recipients r WHERE r.id=cr.recipient_id AND r.phone_hash=$1
		AND cr.status='QUEUED'`, hash)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE call_attempts a SET state='CANCELLED',outcome='suppressed',
		ended_at=now(),updated_at=now() FROM campaign_recipients cr JOIN recipients r
		ON r.id=cr.recipient_id WHERE a.campaign_recipient_id=cr.id AND r.phone_hash=$1
		AND a.state='CLAIMED'`, hash)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE campaign_recipients cr SET status='SUPPRESSED'
		FROM recipients r WHERE r.id=cr.recipient_id AND r.phone_hash=$1
		AND cr.status='ACTIVE' AND NOT EXISTS(SELECT 1 FROM call_attempts a
		WHERE a.campaign_recipient_id=cr.id AND a.state IN
		('CLAIMED','ORIGINATING','RINGING','ANSWERED','MESSAGE_STARTED'))`, hash)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE dialer_slots SET attempt_id=NULL,leased_at=NULL
		WHERE attempt_id IN (SELECT a.id FROM call_attempts a JOIN campaign_recipients cr
		ON cr.id=a.campaign_recipient_id JOIN recipients r ON r.id=cr.recipient_id
		WHERE r.phone_hash=$1 AND a.state='CANCELLED')`, hash)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE outbox SET state='DONE',processed_at=now()
		WHERE state='PENDING' AND aggregate_id IN (SELECT a.id FROM call_attempts a
		JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id JOIN recipients r
		ON r.id=cr.recipient_id WHERE r.phone_hash=$1 AND a.state='CANCELLED')`, hash)
	return err
}

func (s *Store) ListSuppressions(ctx context.Context) ([]SuppressionView, error) {
	rows, err := s.pool.Query(ctx, `SELECT phone_cipher,reason,source,created_at
		FROM suppressions ORDER BY created_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []SuppressionView
	for rows.Next() {
		var view SuppressionView
		var ciphertext []byte
		if err = rows.Scan(&ciphertext, &view.Reason, &view.Source, &view.CreatedAt); err != nil {
			return nil, err
		}
		phone, decryptErr := s.protector.Decrypt(ciphertext)
		if decryptErr != nil {
			return nil, decryptErr
		}
		view.Phone = maskPhone(phone)
		result = append(result, view)
	}
	return result, rows.Err()
}

func jsonHash(hash []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, min(16, len(hash)*2))
	for i := range out {
		n := hash[i/2]
		if i%2 == 0 {
			n >>= 4
		}
		out[i] = digits[n&15]
	}
	return string(out)
}
