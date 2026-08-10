package main

import (
	"context"
	"net/http"
	"time"
)

func (a *API) registerCallerID(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Phone                  string    `json:"phone_e164"`
		AuthorizationReference string    `json:"authorization_reference"`
		AuthorizedAt           time.Time `json:"authorized_at"`
	}
	if err := decodeJSON(w, r, 32<<10, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "Invalid caller ID payload.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	view, err := a.store.RegisterCallerID(ctx, input.Phone, input.AuthorizationReference,
		principal(r).Actor, input.AuthorizedAt)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (a *API) listCallerIDs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	items, err := a.store.ListCallerIDs(ctx)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []CallerIDView{}
	}
	writeJSON(w, http.StatusOK, items)
}
