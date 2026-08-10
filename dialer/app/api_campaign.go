package main

import (
	"context"
	"net/http"
	"time"
)

func (a *API) createCampaign(w http.ResponseWriter, r *http.Request) {
	var input CampaignInput
	if err := decodeJSON(w, r, 64<<10, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "Invalid campaign payload.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	campaign, err := a.store.CreateCampaign(ctx, input, principal(r).Actor)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, campaign)
}

func (a *API) listCampaigns(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	campaigns, err := a.store.ListCampaigns(ctx)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if campaigns == nil {
		campaigns = []Campaign{}
	}
	writeJSON(w, http.StatusOK, campaigns)
}

func (a *API) getCampaign(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	campaign, err := a.store.GetCampaign(ctx, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	blocks, err := a.store.CampaignSafetyBlocks(ctx, campaign.ID, true, nowUTC())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaign": campaign, "safety_blocks": blocks})
}

func (a *API) updateCampaign(w http.ResponseWriter, r *http.Request) {
	var input CampaignInput
	if err := decodeJSON(w, r, 64<<10, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "Invalid campaign payload.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	campaign, err := a.store.UpdateCampaign(ctx, r.PathValue("id"), input, principal(r).Actor)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, campaign)
}

func nowUTC() time.Time { return time.Now().UTC() }
