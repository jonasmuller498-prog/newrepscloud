package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

type Originator struct {
	store   *Store
	client  *ARIClient
	gate    *DependencyGate
	metrics *Metrics
	log     *slog.Logger
}

func (o *Originator) Run(ctx context.Context) {
	var workers sync.WaitGroup
	for i := 0; i < o.store.config.MaxConcurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			o.worker(ctx)
		}()
	}
	workers.Wait()
}

func (o *Originator) worker(ctx context.Context) {
	for ctx.Err() == nil {
		if !o.store.config.DialingEnabled || !o.gate.ReadyForDial() {
			waitContext(ctx, 250*time.Millisecond)
			continue
		}
		claimCtx, claimCancel := context.WithTimeout(ctx, defaultDBTimeout)
		item, err := o.store.ClaimOutbox(claimCtx)
		claimCancel()
		if err != nil {
			o.log.Error("outbox claim failed", "error", err)
			waitContext(ctx, 250*time.Millisecond)
			continue
		}
		if item == nil {
			waitContext(ctx, 25*time.Millisecond)
			continue
		}
		if !o.gate.ReadyForDial() {
			_ = o.store.ResetOutbox(ctx, item.ID)
			continue
		}
		loadCtx, loadCancel := context.WithTimeout(ctx, defaultDBTimeout)
		command, err := o.store.LoadOriginateCommand(loadCtx, item.AttemptID)
		loadCancel()
		if err != nil {
			if !errors.Is(err, errNotFound) {
				o.gate.mediaReady.Store(false)
				o.log.Error("originate prerequisites unavailable", "error", err)
			}
			_ = o.store.ResetOutbox(ctx, item.ID)
			waitContext(ctx, 100*time.Millisecond)
			continue
		}
		callCtx, callCancel := context.WithTimeout(ctx, 15*time.Second)
		result, originateErr := o.client.Originate(callCtx, command)
		callCancel()
		completeCtx, completeCancel := context.WithTimeout(ctx, defaultDBTimeout)
		completeErr := o.store.CompleteOriginate(completeCtx, *item, result)
		completeCancel()
		if completeErr != nil {
			o.log.Error("originate result persistence failed", "error", completeErr)
			continue
		}
		if result.Accepted {
			o.metrics.originatesAccepted.Add(1)
		} else {
			o.metrics.originatesFailed.Add(1)
			if result.Uncertain {
				o.metrics.quarantined.Add(1)
			}
			o.log.Warn("ARI originate rejected", "outcome", result.Outcome,
				"error", originateErr)
		}
	}
}

func waitContext(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
