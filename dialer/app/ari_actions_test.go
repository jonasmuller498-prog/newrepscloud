package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestARIPlayAndHangupRequests(t *testing.T) {
	var played, hungUp bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/ari/channels/dialer-1/play":
			if r.URL.Query().Get("media") != "sound:campaigns/abc123" ||
				r.URL.Query().Get("playbackId") != "play-1" {
				t.Errorf("unexpected play query: %v", r.URL.Query())
			}
			played = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete && r.URL.Path == "/ari/channels/dialer-1":
			hungUp = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected ARI request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	client := NewARIClient(ariTestConfig(server.URL))
	if result, err := client.Play(context.Background(), "dialer-1", "play-1", "abc123"); err != nil || !result.Accepted {
		t.Fatalf("play result=%+v err=%v", result, err)
	}
	if result, err := client.Hangup(context.Background(), "dialer-1"); err != nil || !result.Accepted {
		t.Fatalf("hangup result=%+v err=%v", result, err)
	}
	if !played || !hungUp {
		t.Fatalf("played=%v hungUp=%v", played, hungUp)
	}
}

func TestStoredARIEventIsRedacted(t *testing.T) {
	raw := []byte(`{"type":"StasisStart","channel":{"id":"dialer-safe","state":"Up",` +
		`"caller":{"number":"+14155552671"}}}`)
	event, err := parseARIEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	safe := string(event.SafeJSON())
	if strings.Contains(safe, "+14155552671") || !strings.Contains(safe, "dialer-safe") {
		t.Fatalf("unsafe event projection: %s", safe)
	}
}
