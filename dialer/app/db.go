package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool      *pgxpool.Pool
	protector *Protector
	config    Config
}

func openStore(ctx context.Context, cfg Config, protector *Protector) (*Store, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL")
	}
	poolCfg.MaxConns = int32(min(cfg.MaxConcurrency+12, 100))
	poolCfg.MinConns = 2
	poolCfg.MaxConnLifetime = time.Hour
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, err
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database ping: %w", err)
	}
	return &Store{pool: pool, protector: protector, config: cfg}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func dbError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	return err
}

func audit(ctx context.Context, tx pgx.Tx, actor, action, kind, id string, detail []byte) error {
	if len(detail) == 0 {
		detail = []byte("{}")
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_log
		(actor, action, resource_type, resource_id, detail)
		VALUES($1,$2,$3,$4,$5)`, actor, action, kind, id, detail)
	return err
}
