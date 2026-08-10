package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type CallerIDView struct {
	ID            string    `json:"id"`
	Phone         string    `json:"phone"`
	AuthorizedAt  time.Time `json:"authorized_at"`
	CreatedAt     time.Time `json:"created_at"`
	Authorization string    `json:"authorization_reference"`
}

func (s *Store) RegisterCallerID(
	ctx context.Context, phone, reference, actor string, authorizedAt time.Time,
) (CallerIDView, error) {
	var view CallerIDView
	normalized, err := normalizeE164(phone)
	if err != nil {
		return view, err
	}
	reference = strings.TrimSpace(reference)
	if reference == "" || len(reference) > 200 ||
		authorizedAt.IsZero() || authorizedAt.After(time.Now().Add(5*time.Minute)) {
		return view, errSafetyBlocked
	}
	ciphertext, err := s.protector.Encrypt(normalized)
	if err != nil {
		return view, err
	}
	id, err := newUUID()
	if err != nil {
		return view, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return view, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `INSERT INTO caller_ids
		(id,phone_cipher,phone_hash,authorization_reference,authorized_at,created_by)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT (phone_hash) DO UPDATE SET phone_hash=EXCLUDED.phone_hash
		RETURNING id,authorized_at,created_at,authorization_reference`,
		id, ciphertext, s.protector.LookupHash(normalized), reference, authorizedAt, actor).
		Scan(&view.ID, &view.AuthorizedAt, &view.CreatedAt, &view.Authorization)
	if err == nil {
		detail, _ := json.Marshal(map[string]string{"phone": maskPhone(normalized)})
		err = audit(ctx, tx, actor, "caller_id.register", "caller_id", view.ID, detail)
	}
	if err != nil {
		return view, err
	}
	view.Phone = maskPhone(normalized)
	return view, tx.Commit(ctx)
}

func (s *Store) ListCallerIDs(ctx context.Context) ([]CallerIDView, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,phone_cipher,authorized_at,created_at,
		authorization_reference FROM caller_ids ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []CallerIDView
	for rows.Next() {
		var view CallerIDView
		var ciphertext []byte
		if err = rows.Scan(&view.ID, &ciphertext, &view.AuthorizedAt,
			&view.CreatedAt, &view.Authorization); err != nil {
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
