package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSIPAttemptMetricLabels(t *testing.T) {
	response := httptest.NewRecorder()
	writeSIPCounters(response, 3, 2)
	body := response.Body.String()
	for _, sample := range []string{
		`dialer_sip_attempts_total{result="accepted"} 3`,
		`dialer_sip_attempts_total{result="failed"} 2`,
	} {
		if !strings.Contains(body, sample) {
			t.Fatalf("missing metric sample %q in %q", sample, body)
		}
	}
	if strings.Count(body, "# TYPE dialer_sip_attempts_total counter") != 1 {
		t.Fatalf("metric type declaration must occur once: %q", body)
	}
}

func TestRequiredSafetyMetricsAreExported(t *testing.T) {
	response := httptest.NewRecorder()
	metrics := &Metrics{}
	metrics.schedulerEnabled.Store(true)
	metrics.cpsThrottled.Store(2)
	metrics.optOutPersistenceFailures.Store(1)
	metrics.ariEventsRejected.Store(1)
	metrics.write(response, &DependencyGate{}, 4, 3, 1)
	body := response.Body.String()
	for _, name := range []string{
		"dialer_scheduler_enabled", "dialer_slot_mismatch",
		"dialer_cps_throttled_total", "dialer_sip_attempts_total",
		"dialer_opt_out_persistence_failures_total",
		"dialer_ari_events_rejected_total",
	} {
		if !strings.Contains(body, name) {
			t.Fatalf("missing required metric %q in %q", name, body)
		}
	}
}
