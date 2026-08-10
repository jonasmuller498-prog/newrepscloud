package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

func (a *API) importRecipients(w http.ResponseWriter, r *http.Request) {
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if len(idempotencyKey) < 8 || len(idempotencyKey) > 200 {
		writeProblem(w, http.StatusBadRequest, "idempotency_key_required",
			"Idempotency-Key must contain 8 to 200 characters.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.config.MaxBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeProblem(w, http.StatusRequestEntityTooLarge, "payload_too_large", "CSV payload is too large.")
		return
	}
	rows, err := parseRecipientCSV(bytes.NewReader(data), nowUTC())
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_csv", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	job, err := a.store.CreateImportJob(ctx, r.PathValue("id"),
		principal(r).Actor, idempotencyKey, rows)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/imports/"+job.ID)
	writeJSON(w, http.StatusAccepted, job)
}

func (a *API) getImportJob(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	job, err := a.store.GetImportJob(ctx, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (a *API) uploadAsset(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, a.config.MaxBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeProblem(w, http.StatusRequestEntityTooLarge, "payload_too_large", "WAV payload is too large.")
		return
	}
	if _, err = validateWAV(data, a.config.AssetMaxDuration); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_wav", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	id, err := a.store.SaveAsset(ctx, r.PathValue("id"), principal(r).Actor, data)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"asset_id": id})
}
