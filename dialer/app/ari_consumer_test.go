package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestARIConsumerMarksOnlyRealStartupGap(t *testing.T) {
	connected := make(chan struct{})
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		close(connected)
		for {
			if _, _, err = conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	config := ariTestConfig(server.URL + "/ari")
	config.DialingEnabled = true
	store, gate := &fakeARIEventStore{}, &DependencyGate{}
	processor := newTestProcessor(
		t.TempDir(), store, fakeARIHangupper{}, gate, &Metrics{})
	consumer := &ARIConsumer{
		store: store, client: NewARIClient(config), processor: processor,
		config: config, gate: gate, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		consumer.Run(ctx)
		close(done)
	}()
	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("ARI consumer did not connect")
	}
	time.Sleep(25 * time.Millisecond)
	store.mu.Lock()
	before := store.uncertain
	store.mu.Unlock()
	if before != 1 {
		t.Fatalf("healthy startup marked active calls %d times", before)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ARI consumer did not stop after cancellation")
	}
	store.mu.Lock()
	after := store.uncertain
	store.mu.Unlock()
	if after != 1 {
		t.Fatalf("clean shutdown marked another gap: %d", after)
	}
}
