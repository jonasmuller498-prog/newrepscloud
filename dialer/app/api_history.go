package main

import (
	"context"
	"net/http"
)

func (a *API) listAttempts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	items, err := a.store.ListAttempts(ctx, r.URL.Query().Get("campaign_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []Attempt{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (a *API) listEvents(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	items, err := a.store.ListEvents(ctx, r.URL.Query().Get("campaign_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []CallEvent{}
	}
	writeJSON(w, http.StatusOK, items)
}
