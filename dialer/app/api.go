package main

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web/*
var webFiles embed.FS

type API struct {
	store   *Store
	config  Config
	gate    *DependencyGate
	metrics *Metrics
}

func NewAPI(store *Store, config Config, gate *DependencyGate, metrics *Metrics) http.Handler {
	api := &API{store: store, config: config, gate: gate, metrics: metrics}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", api.live)
	mux.HandleFunc("GET /health/ready", api.ready)
	mux.Handle("GET /metrics", metrics.Handler(store, gate))
	both := api.require("operator", "approver")
	operator := api.require("operator")
	approver := api.require("approver")
	mux.HandleFunc("GET /api/v1/status", both(api.status))
	mux.HandleFunc("GET /api/v1/campaigns", both(api.listCampaigns))
	mux.HandleFunc("POST /api/v1/campaigns", operator(api.createCampaign))
	mux.HandleFunc("GET /api/v1/campaigns/{id}", both(api.getCampaign))
	mux.HandleFunc("PATCH /api/v1/campaigns/{id}", operator(api.updateCampaign))
	mux.HandleFunc("DELETE /api/v1/campaigns/{id}", operator(api.cancelCampaign))
	mux.HandleFunc("POST /api/v1/campaigns/{id}/recipients", operator(api.importRecipients))
	mux.HandleFunc("POST /api/v1/campaigns/{id}/assets", operator(api.uploadAsset))
	mux.HandleFunc("POST /api/v1/campaigns/{id}/validate", operator(api.validateCampaign))
	mux.HandleFunc("POST /api/v1/campaigns/{id}/approve", approver(api.approveCampaign))
	mux.HandleFunc("POST /api/v1/campaigns/{id}/schedule", operator(api.scheduleCampaign))
	mux.HandleFunc("POST /api/v1/campaigns/{id}/start", operator(api.startCampaign))
	mux.HandleFunc("POST /api/v1/campaigns/{id}/pause", operator(api.pauseCampaign))
	mux.HandleFunc("POST /api/v1/campaigns/{id}/cancel", operator(api.cancelCampaign))
	mux.HandleFunc("GET /api/v1/caller-ids", both(api.listCallerIDs))
	mux.HandleFunc("POST /api/v1/caller-ids", operator(api.registerCallerID))
	mux.HandleFunc("GET /api/v1/suppressions", both(api.listSuppressions))
	mux.HandleFunc("POST /api/v1/suppressions", operator(api.createSuppression))
	mux.HandleFunc("POST /api/v1/opt-outs", api.optOut)
	mux.HandleFunc("GET /api/v1/attempts", both(api.listAttempts))
	mux.HandleFunc("GET /api/v1/events", both(api.listEvents))
	static, _ := fs.Sub(webFiles, "web")
	mux.Handle("GET /", http.FileServer(http.FS(static)))
	return securityHeaders(mux)
}
