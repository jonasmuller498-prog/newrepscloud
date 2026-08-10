package main

import (
	"context"
	"log/slog"
	"time"
)

type ImportWorker struct {
	store *Store
	log   *slog.Logger
}

func (w *ImportWorker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		claimCtx, cancel := context.WithTimeout(ctx, defaultDBTimeout)
		id, err := w.store.ClaimImportJob(claimCtx)
		cancel()
		if err != nil {
			w.log.Error("import job claim failed", "error", err)
			waitContext(ctx, time.Second)
			continue
		}
		if id == "" {
			waitContext(ctx, 100*time.Millisecond)
			continue
		}
		processCtx, processCancel := context.WithTimeout(ctx, 5*time.Minute)
		err = w.store.ProcessImportJob(processCtx, id)
		processCancel()
		if err != nil {
			w.log.Error("import job processing failed", "job_id", id, "error", err)
			resetCtx, resetCancel := context.WithTimeout(ctx, defaultDBTimeout)
			_ = w.store.DeferImportJob(resetCtx, id)
			resetCancel()
		}
	}
}
