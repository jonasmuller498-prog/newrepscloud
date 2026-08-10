package main

import (
	"context"
)

func (s *Store) MediaReady(ctx context.Context) bool {
	if !checkMediaDirectory(s.config.MediaDir) {
		return false
	}
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ma.storage_name,ma.sha256,
		ma.byte_size,ma.duration_ms FROM campaigns c
		JOIN message_assets ma ON ma.id=c.message_asset_id
		WHERE c.state='RUNNING'`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var spec MediaSpec
		if rows.Scan(&spec.StorageName, &spec.SHA256, &spec.ByteSize, &spec.DurationMS) != nil {
			return false
		}
		if _, verifyErr := verifyMediaFile(s.config.MediaDir, spec,
			s.config.AssetMaxDuration, s.config.MaxBodyBytes); verifyErr != nil {
			return false
		}
	}
	return rows.Err() == nil
}
