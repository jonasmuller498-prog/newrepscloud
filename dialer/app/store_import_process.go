package main

import (
	"context"
	"encoding/json"
)

func (s *Store) ProcessImportJob(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var campaignID, actor, state string
	var ciphertext []byte
	err = tx.QueryRow(ctx, `SELECT campaign_id,actor,state,payload_cipher
		FROM import_jobs WHERE id=$1 FOR UPDATE`, id).
		Scan(&campaignID, &actor, &state, &ciphertext)
	if err != nil {
		return dbError(err)
	}
	if state != "PROCESSING" {
		return nil
	}
	var campaignState string
	if err = tx.QueryRow(ctx, `SELECT state FROM campaigns WHERE id=$1
		FOR UPDATE`, campaignID).Scan(&campaignState); err != nil {
		return err
	}
	if campaignState != "DRAFT" {
		_, err = tx.Exec(ctx, `UPDATE import_jobs SET state='FAILED',
			error_code='campaign_state_changed',completed_at=now() WHERE id=$1`, id)
		return commitResult(ctx, tx, err)
	}
	payload, err := s.protector.DecryptBytes(ciphertext)
	if err != nil {
		_, err = tx.Exec(ctx, `UPDATE import_jobs SET state='FAILED',
			error_code='payload_decrypt',processing_at=NULL,completed_at=now()
			WHERE id=$1`, id)
		return commitResult(ctx, tx, err)
	}
	var rows []ImportRow
	if err = json.Unmarshal(payload, &rows); err != nil || len(rows) == 0 {
		_, err = tx.Exec(ctx, `UPDATE import_jobs SET state='FAILED',
			error_code='payload_invalid',processing_at=NULL,completed_at=now()
			WHERE id=$1`, id)
		return commitResult(ctx, tx, err)
	}
	var imported int64
	for _, row := range rows {
		added, importErr := s.importRecipient(ctx, tx, campaignID, actor, row)
		if importErr != nil {
			return importErr
		}
		if added {
			imported++
		}
	}
	detail, _ := json.Marshal(map[string]any{
		"job_id": id, "rows": len(rows), "inserted": imported,
	})
	if err = audit(ctx, tx, actor, "recipients.import", "campaign",
		campaignID, detail); err == nil {
		_, err = tx.Exec(ctx, `UPDATE import_jobs SET state='COMPLETED',
			inserted_rows=$2,completed_at=now(),processing_at=NULL WHERE id=$1`,
			id, imported)
	}
	return commitResult(ctx, tx, err)
}

func (s *Store) DeferImportJob(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE import_jobs SET state='PENDING',
		processing_at=NULL,available_at=clock_timestamp()+interval '5 seconds'
		WHERE id=$1 AND state='PROCESSING'`, id)
	return err
}

func (s *Store) ResetStaleImportJobs(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE import_jobs SET state='PENDING',
		processing_at=NULL,available_at=now() WHERE state='PROCESSING'
		AND processing_at<now()-interval '5 minutes'`)
	return err
}
