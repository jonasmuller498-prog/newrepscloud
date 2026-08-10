package main

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
)

type Metrics struct {
	originatesAccepted atomic.Uint64
	originatesFailed   atomic.Uint64
	ariEvents          atomic.Uint64
	quarantined        atomic.Uint64
	leader             atomic.Bool
}

func (m *Metrics) Handler(store *Store, gate *DependencyGate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var queued, active int64
		ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
		defer cancel()
		_ = store.pool.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE status='QUEUED'),
			count(*) FILTER (WHERE status='ACTIVE') FROM campaign_recipients`).
			Scan(&queued, &active)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		writeMetric(w, "dialer_originates_accepted_total", m.originatesAccepted.Load())
		writeMetric(w, "dialer_originates_failed_total", m.originatesFailed.Load())
		writeMetric(w, "dialer_ari_events_total", m.ariEvents.Load())
		writeMetric(w, "dialer_quarantined_total", m.quarantined.Load())
		writeGauge(w, "dialer_scheduler_leader", boolFloat(m.leader.Load()))
		writeGauge(w, "dialer_ari_connected", boolFloat(gate.ariConnected.Load()))
		writeGauge(w, "dialer_media_ready", boolFloat(gate.mediaReady.Load()))
		writeGauge(w, "dialer_queue_recipients", float64(queued))
		writeGauge(w, "dialer_active_recipients", float64(active))
	}
}

func writeMetric(w http.ResponseWriter, name string, value uint64) {
	_, _ = fmt.Fprintf(w, "# TYPE %s counter\n%s %d\n", name, name, value)
}

func writeGauge(w http.ResponseWriter, name string, value float64) {
	_, _ = fmt.Fprintf(w, "# TYPE %s gauge\n%s %.0f\n", name, name, value)
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
