package main

import (
	"context"
	"net/http"
)

func (a *API) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "live"})
}

func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	journalReady := !a.config.DialingEnabled ||
		(a.gate.journalReady.Load() &&
			NewEventJournal(a.config.EventJournalDir).Probe() == nil)
	if err := a.store.Ping(ctx); err != nil || !a.gate.mediaReady.Load() ||
		(a.config.DialingEnabled && (!a.gate.ariConnected.Load() || !journalReady)) {
		writeProblem(w, http.StatusServiceUnavailable, "not_ready", "A required dependency is unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	var campaigns, queued, active int64
	err := a.store.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM campaigns),
		(SELECT count(*) FROM campaign_recipients WHERE status='QUEUED'),
		(SELECT count(*) FROM campaign_recipients WHERE status='ACTIVE')`).
		Scan(&campaigns, &queued, &active)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var blocks []SafetyBlock
	if !a.config.DialingEnabled {
		blocks = append(blocks, SafetyBlock{"dialing_disabled", "DIALING_ENABLED is false.", 0})
	}
	if a.config.CPS <= 0 {
		blocks = append(blocks, SafetyBlock{"cps_zero", "CPS is zero; no calls can originate.", 0})
	}
	if a.config.DialingEnabled && !a.gate.ariConnected.Load() {
		blocks = append(blocks, SafetyBlock{"ari_unavailable", "The ARI event stream is unavailable.", 0})
	}
	if a.config.DialingEnabled && !a.gate.journalReady.Load() {
		blocks = append(blocks, SafetyBlock{
			"ari_journal_unavailable", "The durable ARI event journal is unavailable.", 0})
	}
	if !a.gate.mediaReady.Load() {
		blocks = append(blocks, SafetyBlock{"media_unavailable", "Media storage is unavailable.", 0})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dialing_enabled": a.config.DialingEnabled,
		"cps":             a.config.CPS, "max_concurrency": a.config.MaxConcurrency,
		"scheduler_leader": a.metrics.leader.Load(),
		"ari_connected":    a.gate.ariConnected.Load(),
		"journal_ready":    a.gate.journalReady.Load(), "media_ready": a.gate.mediaReady.Load(),
		"campaigns": campaigns, "queued": queued, "active": active, "safety_blocks": blocks,
	})
}
