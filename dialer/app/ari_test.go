package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func ariTestConfig(serverURL string) Config {
	return Config{
		ARIURL: serverURL, ARIApp: "broadcast", ARIUser: "ari-user",
		ARIPassword: "ari-pass", ARIEndpointTemplate: "PJSIP/%s@carrier",
		ARIContext: "outbound", ARIExtension: "s",
	}
}

func TestARIOriginateRequest(t *testing.T) {
	var checked bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ari/channels" {
			t.Errorf("unexpected ARI path %q", r.URL.Path)
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "ari-user" || password != "ari-pass" {
			t.Error("missing ARI basic authentication")
		}
		query := r.URL.Query()
		if query.Get("endpoint") != "PJSIP/+14155552671@carrier" ||
			query.Get("channelId") != "dialer-channel" ||
			query.Get("context") != "outbound" || query.Get("app") != "broadcast" {
			t.Errorf("unexpected query: %v", query)
		}
		var payload struct {
			Variables map[string]string `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Variables["DIALER_ATTEMPT_ID"] != "attempt-id" ||
			payload.Variables["DIALER_MEDIA"] != "asset.wav" {
			t.Errorf("unexpected variables: %v", payload.Variables)
		}
		checked = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := NewARIClient(ariTestConfig(server.URL + "/ari"))
	result, err := client.Originate(context.Background(), OriginateCommand{
		AttemptID: "attempt-id", ChannelID: "dialer-channel", Phone: "+14155552671",
		CallerID: "+14155550100", Media: "asset.wav",
	})
	if err != nil || !result.Accepted || !checked {
		t.Fatalf("result=%+v checked=%v err=%v", result, checked, err)
	}
}

func TestARIOriginateOutcomeMapping(t *testing.T) {
	tests := []struct {
		status    int
		outcome   string
		uncertain bool
	}{
		{http.StatusForbidden, "forbidden", false},
		{http.StatusBadRequest, "invalid", false},
		{http.StatusTooManyRequests, "temporary", false},
		{http.StatusServiceUnavailable, "temporary", false},
	}
	for _, test := range tests {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(test.status)
		}))
		client := NewARIClient(ariTestConfig(server.URL))
		result, err := client.Originate(context.Background(), OriginateCommand{})
		server.Close()
		if err == nil || result.Outcome != test.outcome || result.Uncertain != test.uncertain {
			t.Errorf("status %d: result=%+v err=%v", test.status, result, err)
		}
	}
}

func TestARITransportFailureIsAmbiguous(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()
	result, err := NewARIClient(ariTestConfig(url)).
		Originate(context.Background(), OriginateCommand{})
	if err == nil || !result.Uncertain || result.Outcome != "ambiguous" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestConservativeARIEventMapping(t *testing.T) {
	event := ARIEvent{Cause: 17}
	if got := destroyedOutcome(event, "RINGING"); got != "busy" {
		t.Fatalf("busy mapped to %q", got)
	}
	event.Cause = 0
	if got := destroyedOutcome(event, "RINGING"); got != "ambiguous" {
		t.Fatalf("unknown mapped to %q", got)
	}
	if got := destroyedOutcome(event, "ANSWERED"); got != "completed" {
		t.Fatalf("answered mapped to %q", got)
	}
	event.Type, event.Digit = "ChannelDtmfReceived", "9"
	if event.Digit != "9" {
		t.Fatal("DTMF opt-out digit not retained")
	}
}
