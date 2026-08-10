package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

type suppressionInput struct {
	Phone     string `json:"phone_e164"`
	Reason    string `json:"reason"`
	Source    string `json:"source"`
	AttemptID string `json:"attempt_id,omitempty"`
}

func (a *API) createSuppression(w http.ResponseWriter, r *http.Request) {
	var input suppressionInput
	if err := decodeJSON(w, r, 32<<10, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "Invalid suppression payload.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	if err := a.store.SuppressPhone(ctx, input.Phone, input.Reason, input.Source,
		principal(r).Actor); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"suppressed": true})
}

func (a *API) listSuppressions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	items, err := a.store.ListSuppressions(ctx)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []SuppressionView{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (a *API) optOut(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeProblem(w, http.StatusRequestEntityTooLarge, "payload_too_large", "Payload is too large.")
		return
	}
	who, bearerOK := a.authenticate(r)
	signatureOK := a.store.protector.VerifySignature(body, r.Header.Get("X-Dialer-Signature"))
	if (!bearerOK || who.Role != "operator") && !signatureOK {
		writeProblem(w, http.StatusUnauthorized, "unauthorized", "Operator token or HMAC signature required.")
		return
	}
	var input suppressionInput
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "Invalid opt-out payload.")
		return
	}
	if err = decoder.Decode(&struct{}{}); err != io.EOF {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "Invalid opt-out payload.")
		return
	}
	actor := "internal:hmac"
	if bearerOK && who.Role == "operator" {
		actor = who.Actor
	}
	ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
	defer cancel()
	if input.AttemptID != "" {
		err = a.store.OptOutAttempt(ctx, input.AttemptID, "internal_api", actor)
	} else {
		err = a.store.SuppressPhone(ctx, input.Phone, "recipient_opt_out", "internal_api", actor)
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"suppressed": true})
}
