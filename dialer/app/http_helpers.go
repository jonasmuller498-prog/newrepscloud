package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultDBTimeout = 5 * time.Second

type problem struct {
	Error   string        `json:"error"`
	Message string        `json:"message"`
	Blocks  []SafetyBlock `json:"blocks,omitempty"`
}

func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeProblem(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, problem{Error: code, Message: message})
}

func writeSafetyProblem(w http.ResponseWriter, blocks []SafetyBlock) {
	writeJSON(w, http.StatusConflict, problem{
		Error: "safety_blocked", Message: "Campaign safety requirements are not satisfied.", Blocks: blocks,
	})
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotFound):
		writeProblem(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
	case errors.Is(err, errConflict):
		writeProblem(w, http.StatusConflict, "state_conflict", "The operation is invalid in the current state.")
	case errors.Is(err, errSafetyBlocked):
		writeProblem(w, http.StatusConflict, "safety_blocked", "A compliance or safety requirement failed.")
	case errors.Is(err, errForbidden):
		writeProblem(w, http.StatusForbidden, "forbidden", "The operation is forbidden.")
	default:
		writeProblem(w, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; connect-src 'self'; img-src 'self'; style-src 'self'; script-src 'self'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}
