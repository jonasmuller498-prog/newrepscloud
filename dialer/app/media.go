package main

import (
	"context"
	"os"
	"path/filepath"
)

func (s *Store) MediaReady(ctx context.Context) bool {
	if !checkMediaDirectory(s.config.MediaDir) {
		return false
	}
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ma.storage_name FROM campaigns c
		JOIN message_assets ma ON ma.id=c.message_asset_id
		WHERE c.state='RUNNING'`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			return false
		}
		info, statErr := os.Stat(filepath.Join(s.config.MediaDir, filepath.Base(name)))
		if statErr != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return rows.Err() == nil
}
