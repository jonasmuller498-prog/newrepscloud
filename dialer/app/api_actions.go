package main

import (
	"context"
	"errors"
	"net/http"
	"time"
)

func (a *API) validateCampaign(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	blocks, err := a.store.ValidateCampaign(ctx, r.PathValue("id"), principal(r).Actor, nowUTC())
	if len(blocks) > 0 {
		writeSafetyProblem(w, blocks)
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	actionSuccess(w, "VALIDATING")
}

func (a *API) approveCampaign(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	blocks, err := a.store.ApproveCampaign(ctx, r.PathValue("id"), principal(r).Actor, nowUTC())
	if len(blocks) > 0 {
		writeSafetyProblem(w, blocks)
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	actionSuccess(w, "APPROVED")
}

func (a *API) scheduleCampaign(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ScheduledAt time.Time `json:"scheduled_at"`
	}
	if err := decodeJSON(w, r, 16<<10, &input); err != nil || input.ScheduledAt.IsZero() {
		writeProblem(w, http.StatusBadRequest, "invalid_schedule", "scheduled_at must be RFC3339.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	if err := a.store.ScheduleCampaign(ctx, r.PathValue("id"), principal(r).Actor, input.ScheduledAt); err != nil {
		writeStoreError(w, err)
		return
	}
	actionSuccess(w, "SCHEDULED")
}

func (a *API) startCampaign(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	blocks, err := a.store.StartCampaign(ctx, r.PathValue("id"), principal(r).Actor,
		nowUTC(), a.gate.ReadyForDial())
	if len(blocks) > 0 {
		writeSafetyProblem(w, blocks)
		return
	}
	if err != nil {
		if errors.Is(err, errSafetyBlocked) {
			writeSafetyProblem(w, blocks)
		} else {
			writeStoreError(w, err)
		}
		return
	}
	actionSuccess(w, "RUNNING")
}

func (a *API) pauseCampaign(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	if err := a.store.PauseCampaign(ctx, r.PathValue("id"), principal(r).Actor); err != nil {
		writeStoreError(w, err)
		return
	}
	actionSuccess(w, "PAUSED")
}

func (a *API) cancelCampaign(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	if err := a.store.CancelCampaign(ctx, r.PathValue("id"), principal(r).Actor); err != nil {
		writeStoreError(w, err)
		return
	}
	actionSuccess(w, "DRAINING")
}

func actionSuccess(w http.ResponseWriter, state string) {
	writeJSON(w, http.StatusOK, map[string]string{"state": state})
}
