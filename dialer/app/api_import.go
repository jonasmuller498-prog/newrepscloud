package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

func (a *API) importRecipients(w http.ResponseWriter, r *http.Request) {
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
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	imported, err := a.store.ImportRecipients(ctx, r.PathValue("id"), principal(r).Actor, rows)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"received": len(rows), "imported": imported})
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
