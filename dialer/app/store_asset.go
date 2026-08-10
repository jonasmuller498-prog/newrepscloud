package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

func (s *Store) SaveAsset(
	ctx context.Context, campaignID, actor string, data []byte,
) (string, error) {
	info, err := validateWAV(data, s.config.AssetMaxDuration)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	storage := hex.EncodeToString(sum[:]) + ".wav"
	if err = writeMediaFile(s.config.MediaDir, storage, data); err != nil {
		return "", err
	}
	id, err := newUUID()
	if err != nil {
		return "", err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `INSERT INTO message_assets
		(id,sha256,storage_name,byte_size,duration_ms,format,created_by)
		VALUES($1,$2,$3,$4,$5,'pcm_s16le_mono_8000',$6)
		ON CONFLICT (sha256) DO UPDATE SET sha256=EXCLUDED.sha256 RETURNING id`,
		id, sum[:], storage, len(data), info.Duration.Milliseconds(), actor).Scan(&id)
	if err == nil {
		tag, updateErr := tx.Exec(ctx, `UPDATE campaigns SET message_asset_id=$2,updated_at=now()
			WHERE id=$1 AND state='DRAFT'`, campaignID, id)
		if updateErr != nil {
			err = updateErr
		} else if tag.RowsAffected() != 1 {
			err = errConflict
		}
	}
	if err == nil {
		detail, _ := json.Marshal(map[string]any{"bytes": len(data), "duration_ms": info.Duration.Milliseconds()})
		err = audit(ctx, tx, actor, "asset.attach", "campaign", campaignID, detail)
	}
	if err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func writeMediaFile(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, filepath.Base(name))
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	file, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(temp, path); err != nil && !os.IsExist(err) {
		return err
	}
	return nil
}
