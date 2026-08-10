package main

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
)

type Metrics struct {
	sipAccepted               atomic.Uint64
	sipFailed                 atomic.Uint64
	cpsThrottled              atomic.Uint64
	optOutPersistenceFailures atomic.Uint64
	ariEvents                 atomic.Uint64
	quarantined               atomic.Uint64
	leader                    atomic.Bool
	schedulerEnabled          atomic.Bool
}

func NewMetricsAPI(store *Store, gate *DependencyGate, metrics *Metrics) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.Handler(store, gate))
	return mux
}

func (m *Metrics) Handler(store *Store, gate *DependencyGate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var queued, active, slotMismatch int64
		ctx, cancel := context.WithTimeout(r.Context(), defaultDBTimeout)
		defer cancel()
		err := store.pool.QueryRow(ctx, metricSnapshotSQL, store.config.MaxConcurrency).
			Scan(&queued, &active, &slotMismatch)
		if err != nil {
			slotMismatch = 1
		}
		m.write(w, gate, queued, active, slotMismatch)
	}
}

func (m *Metrics) write(
	w http.ResponseWriter, gate *DependencyGate, queued, active, slotMismatch int64,
) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	writeSIPCounters(w, m.sipAccepted.Load(), m.sipFailed.Load())
	writeMetric(w, "dialer_cps_throttled_total", m.cpsThrottled.Load())
	writeMetric(w, "dialer_opt_out_persistence_failures_total",
		m.optOutPersistenceFailures.Load())
	writeMetric(w, "dialer_ari_events_total", m.ariEvents.Load())
	writeMetric(w, "dialer_quarantined_total", m.quarantined.Load())
	writeGauge(w, "dialer_scheduler_leader", boolFloat(m.leader.Load()))
	writeGauge(w, "dialer_scheduler_enabled", boolFloat(m.schedulerEnabled.Load()))
	writeGauge(w, "dialer_slot_mismatch", float64(slotMismatch))
	writeGauge(w, "dialer_ari_connected", boolFloat(gate.ariConnected.Load()))
	writeGauge(w, "dialer_ari_journal_ready", boolFloat(gate.journalReady.Load()))
	writeGauge(w, "dialer_media_ready", boolFloat(gate.mediaReady.Load()))
	writeGauge(w, "dialer_queue_recipients", float64(queued))
	writeGauge(w, "dialer_active_recipients", float64(active))
}

func writeMetric(w http.ResponseWriter, name string, value uint64) {
	_, _ = fmt.Fprintf(w, "# TYPE %s counter\n%s %d\n", name, name, value)
}

func writeSIPCounters(w http.ResponseWriter, accepted, failed uint64) {
	_, _ = fmt.Fprintf(w, "# TYPE dialer_sip_attempts_total counter\n"+
		"dialer_sip_attempts_total{result=\"accepted\"} %d\n"+
		"dialer_sip_attempts_total{result=\"failed\"} %d\n", accepted, failed)
}

func writeGauge(w http.ResponseWriter, name string, value float64) {
	_, _ = fmt.Fprintf(w, "# TYPE %s gauge\n%s %.0f\n", name, name, value)
}

const metricSnapshotSQL = `WITH active_attempts AS (
	SELECT id,slot_no,campaign_recipient_id FROM call_attempts WHERE state IN
	('CLAIMED','ORIGINATING','RINGING','ANSWERED','MESSAGE_STARTED',
	 'TERMINATING','UNCERTAIN')
), bad_slots AS (
	SELECT 1 FROM active_attempts a LEFT JOIN dialer_slots ds ON ds.attempt_id=a.id
	WHERE ds.slot_no IS NULL OR ds.slot_no<>a.slot_no OR ds.slot_no>$1
	UNION ALL
	SELECT 1 FROM dialer_slots ds LEFT JOIN active_attempts a ON a.id=ds.attempt_id
	WHERE ds.attempt_id IS NOT NULL AND a.id IS NULL
	UNION ALL
	SELECT 1 FROM active_attempts a JOIN campaign_recipients cr
	  ON cr.id=a.campaign_recipient_id WHERE cr.status<>'ACTIVE'
	UNION ALL
	SELECT 1 FROM campaign_recipients cr LEFT JOIN active_attempts a
	  ON a.campaign_recipient_id=cr.id WHERE cr.status='ACTIVE' AND a.id IS NULL
)
SELECT count(*) FILTER (WHERE status='QUEUED'),
	count(*) FILTER (WHERE status='ACTIVE'),
	CASE WHEN EXISTS(SELECT 1 FROM bad_slots)
	  OR (SELECT count(*) FROM dialer_slots WHERE slot_no<=$1)<>$1
	THEN 1 ELSE 0 END
FROM campaign_recipients`

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
