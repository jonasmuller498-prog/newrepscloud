package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type ImportJob struct {
	ID         string    `json:"id"`
	CampaignID string    `json:"campaign_id"`
	State      string    `json:"state"`
	Received   int64     `json:"received_rows"`
	Inserted   int64     `json:"inserted_rows"`
	ErrorCode  *string   `json:"error_code,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Store) CreateImportJob(
	ctx context.Context, campaignID, actor, key string, rows []ImportRow,
) (ImportJob, error) {
	var job ImportJob
	key = strings.TrimSpace(key)
	if len(key) < 8 || len(key) > 200 || len(rows) == 0 {
		return job, errSafetyBlocked
	}
	payload, err := json.Marshal(rows)
	if err != nil {
		return job, err
	}
	digest := sha256.Sum256(payload)
	ciphertext, err := s.protector.EncryptBytes(payload)
	if err != nil {
		return job, err
	}
	job.ID, err = newUUID()
	if err != nil {
		return job, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return job, err
	}
	defer tx.Rollback(ctx)
	var existingDigest []byte
	scanErr := tx.QueryRow(ctx, `SELECT id,payload_sha256 FROM import_jobs
		WHERE campaign_id=$1 AND actor=$2 AND idempotency_key=$3`,
		campaignID, actor, key).Scan(&job.ID, &existingDigest)
	if scanErr == nil {
		if subtle.ConstantTimeCompare(existingDigest, digest[:]) != 1 {
			return job, errConflict
		}
		if err = scanImportJob(tx.QueryRow(ctx, importJobSelect+" WHERE id=$1", job.ID),
			&job); err != nil {
			return job, err
		}
		return job, tx.Commit(ctx)
	}
	if scanErr != pgx.ErrNoRows {
		return job, scanErr
	}
	var state string
	if err = tx.QueryRow(ctx, `SELECT state FROM campaigns WHERE id=$1
		FOR UPDATE`, campaignID).Scan(&state); err != nil {
		return job, dbError(err)
	}
	if state != "DRAFT" {
		return job, errConflict
	}
	tag, err := tx.Exec(ctx, `INSERT INTO import_jobs
		(id,campaign_id,actor,idempotency_key,payload_sha256,payload_cipher,received_rows)
		VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(campaign_id,actor,idempotency_key)
		DO NOTHING`, job.ID, campaignID, actor, key, digest[:], ciphertext, len(rows))
	if err != nil {
		return job, err
	}
	if tag.RowsAffected() == 0 {
		err = tx.QueryRow(ctx, `SELECT id,payload_sha256 FROM import_jobs
			WHERE campaign_id=$1 AND actor=$2 AND idempotency_key=$3`,
			campaignID, actor, key).Scan(&job.ID, &existingDigest)
		if err != nil {
			return job, err
		}
		if subtle.ConstantTimeCompare(existingDigest, digest[:]) != 1 {
			return job, errConflict
		}
	}
	if err = scanImportJob(tx.QueryRow(ctx, importJobSelect+" WHERE id=$1", job.ID),
		&job); err != nil {
		return job, err
	}
	return job, tx.Commit(ctx)
}

const importJobSelect = `SELECT id,campaign_id,state,received_rows,
	COALESCE(inserted_rows,0),error_code,created_at FROM import_jobs`

func (s *Store) GetImportJob(ctx context.Context, id string) (ImportJob, error) {
	var job ImportJob
	err := scanImportJob(s.pool.QueryRow(ctx, importJobSelect+" WHERE id=$1", id), &job)
	return job, dbError(err)
}

func scanImportJob(row rowScanner, job *ImportJob) error {
	return row.Scan(&job.ID, &job.CampaignID, &job.State, &job.Received,
		&job.Inserted, &job.ErrorCode, &job.CreatedAt)
}

func (s *Store) ClaimImportJob(ctx context.Context) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `UPDATE import_jobs SET state='PROCESSING',
		processing_at=now() WHERE id=(SELECT id FROM import_jobs
		WHERE state='PENDING' AND available_at<=clock_timestamp()
		ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id`).Scan(&id)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return id, err
}
