package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Scheduler struct {
	store   *Store
	gate    *DependencyGate
	metrics *Metrics
	log     *slog.Logger
}

func (s *Scheduler) Run(ctx context.Context) {
	for ctx.Err() == nil {
		conn, err := s.store.pool.Acquire(ctx)
		if err != nil {
			s.wait(ctx, time.Second)
			continue
		}
		var leader bool
		err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(77441002)").Scan(&leader)
		if err == nil && leader {
			s.metrics.leader.Store(true)
			s.runAsLeader(ctx, conn)
			s.metrics.leader.Store(false)
			_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock(77441002)")
		}
		conn.Release()
		if !leader {
			s.wait(ctx, time.Second)
		}
	}
}

func (s *Scheduler) runAsLeader(ctx context.Context, conn *pgxpool.Conn) {
	lastPing := time.Time{}
	for ctx.Err() == nil {
		if time.Since(lastPing) >= time.Second {
			if _, err := conn.Exec(ctx, "SELECT 1"); err != nil {
				return
			}
			lastPing = time.Now()
		}
		if !s.store.config.DialingEnabled || s.store.config.CPS <= 0 ||
			!s.gate.ReadyForDial() {
			s.wait(ctx, 250*time.Millisecond)
			continue
		}
		attempt, err := s.store.AllocateAttempt(ctx)
		if err != nil {
			s.log.Error("scheduler allocation failed", "error", err)
			s.wait(ctx, 250*time.Millisecond)
			continue
		}
		if attempt == nil {
			s.wait(ctx, 5*time.Millisecond)
		}
	}
}

func (s *Scheduler) wait(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
