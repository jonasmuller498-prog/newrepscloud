package main

import (
	"context"
	"log/slog"
	"time"
)

type Reconciler struct {
	store  *Store
	client ARICommands
	gate   *DependencyGate
	log    *slog.Logger
}

func (r *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		r.gate.mediaReady.Store(r.store.MediaReady(runCtx))
		err := r.reconcile(runCtx)
		cancel()
		if err != nil && ctx.Err() == nil {
			r.log.Error("reconciliation failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Reconciler) reconcile(ctx context.Context) error {
	if err := r.store.ReconcileDatabase(ctx); err != nil {
		return err
	}
	if !r.gate.ariConnected.Load() {
		return nil
	}
	items, err := r.store.ReconcileCandidates(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		exists, lookupErr := r.client.ChannelExists(ctx, item.ChannelID)
		if lookupErr != nil {
			continue
		}
		if exists {
			err = r.store.RequestReconcileHangup(ctx, item.AttemptID)
		} else {
			err = r.store.ConfirmChannelAbsent(ctx, item.AttemptID)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
