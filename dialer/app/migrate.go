package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock(77441001)"); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock(77441001)")
	}()
	if _, err = conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}
	names, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		content, readErr := migrationFiles.ReadFile(name)
		if readErr != nil {
			return readErr
		}
		sum := sha256.Sum256(content)
		checksum := hex.EncodeToString(sum[:])
		var existing string
		scanErr := conn.QueryRow(ctx,
			"SELECT checksum FROM schema_migrations WHERE name=$1", name).Scan(&existing)
		if scanErr == nil {
			if existing != checksum {
				return fmt.Errorf("migration checksum changed: %s", name)
			}
			continue
		}
		if scanErr != pgx.ErrNoRows {
			return scanErr
		}
		tx, beginErr := conn.Begin(ctx)
		if beginErr != nil {
			return beginErr
		}
		if _, err = tx.Exec(ctx, string(content), pgx.QueryExecModeSimpleProtocol); err == nil {
			_, err = tx.Exec(ctx,
				"INSERT INTO schema_migrations(name, checksum) VALUES($1,$2)", name, checksum)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}
