package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestARIEventDBFailureSurvivesRestartReplay(t *testing.T) {
	dir := t.TempDir()
	event := testARIEvent("StasisStart")
	raw := []byte(`{"type":"StasisStart","channel":{"id":"dialer-` +
		`0123456789abcdef0123456789abcdef","state":"Up"}}`)
	gate := &DependencyGate{}
	gate.ariConnected.Store(true)
	gate.journalReady.Store(true)
	failing := &fakeARIEventStore{alwaysFail: true}
	processor := newTestProcessor(
		dir, failing, fakeARIHangupper{}, gate, &Metrics{})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := processor.Handle(ctx, event, raw); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected retry to stop at context deadline, got %v", err)
	}
	entries, err := processor.journal.Entries()
	if err != nil || len(entries) != 1 {
		t.Fatalf("journal entries=%d err=%v", len(entries), err)
	}
	if gate.ariConnected.Load() || gate.journalReady.Load() {
		t.Fatal("persistence failure did not close the dialing gate")
	}

	replayed := &fakeARIEventStore{inserted: true}
	restarted := newTestProcessor(
		dir, replayed, fakeARIHangupper{}, gate, &Metrics{})
	if err = restarted.Replay(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(replayed.keys) != 1 || replayed.keys[0] != event.Key(raw) {
		t.Fatalf("replay keys=%v, want original %s", replayed.keys, event.Key(raw))
	}
	entries, err = restarted.journal.Entries()
	if err != nil || len(entries) != 0 {
		t.Fatalf("journal not cleared after commit: entries=%d err=%v", len(entries), err)
	}
}

func TestDTMFJournalIsDurableAndRedactedBeforeHangup(t *testing.T) {
	dir := t.TempDir()
	event := testARIEvent("ChannelDtmfReceived")
	raw := []byte(`{"type":"ChannelDtmfReceived","digit":"9","channel":{"id":"dialer-` +
		`0123456789abcdef0123456789abcdef","caller":{"number":"+14155552671"}}}`)
	order := []string{}
	store := &fakeARIEventStore{failures: 1, inserted: true, order: &order}
	journal := NewEventJournal(dir)
	hangupper := fakeARIHangupper{fn: func(channelID string) {
		order = append(order, "hangup")
		entries, err := journal.Entries()
		if err != nil || len(entries) != 1 {
			t.Fatalf("hangup preceded durable journal: entries=%d err=%v", len(entries), err)
		}
		data, err := os.ReadFile(entries[0].path)
		if err != nil {
			t.Fatal(err)
		}
		body := string(data)
		if strings.Contains(body, "+14155552671") || strings.Contains(body, "caller") {
			t.Fatalf("journal contains PII or raw headers: %s", body)
		}
		if channelID != event.ChannelID() {
			t.Fatalf("hangup channel=%q", channelID)
		}
	}}
	gate, metrics := &DependencyGate{}, &Metrics{}
	gate.ariConnected.Store(true)
	gate.journalReady.Store(true)
	processor := newTestProcessor(dir, store, hangupper, gate, metrics)
	if err := processor.Handle(context.Background(), event, raw); !errors.Is(err, errARIReconnect) {
		t.Fatalf("expected safe reconnect after recovery, got %v", err)
	}
	want := []string{"hangup", "project", "project"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("operation order=%v want=%v", order, want)
	}
	if metrics.optOutPersistenceFailures.Load() != 1 {
		t.Fatalf("failure metric=%d", metrics.optOutPersistenceFailures.Load())
	}
}

func TestDuplicateARIEventClearsJournalWithoutDoubleCount(t *testing.T) {
	store := &fakeARIEventStore{inserted: false}
	metrics := &Metrics{}
	dir := t.TempDir()
	gate := &DependencyGate{}
	gate.ariConnected.Store(true)
	gate.journalReady.Store(true)
	processor := newTestProcessor(
		dir, store, fakeARIHangupper{}, gate, metrics)
	event := testARIEvent("StasisEnd")
	raw := []byte(`{"type":"StasisEnd","channel":{"id":"dialer-` +
		`0123456789abcdef0123456789abcdef"}}`)
	if err := processor.Handle(context.Background(), event, raw); err != nil {
		t.Fatal(err)
	}
	entries, err := processor.journal.Entries()
	if err != nil || len(entries) != 0 || metrics.ariEvents.Load() != 0 {
		t.Fatalf("entries=%d events=%d err=%v",
			len(entries), metrics.ariEvents.Load(), err)
	}
}

func TestMalformedARIJournalBlocksReplay(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/bad.json"
	if err := os.WriteFile(path, []byte(
		`{"key":"bad","raw_number":"+14155552671"}`), 0600); err != nil {
		t.Fatal(err)
	}
	gate := &DependencyGate{}
	gate.ariConnected.Store(true)
	gate.journalReady.Store(true)
	store := &fakeARIEventStore{}
	processor := newTestProcessor(
		dir, store, fakeARIHangupper{}, gate, &Metrics{})
	err := processor.Replay(context.Background())
	if !errors.Is(err, errMalformedJournal) {
		t.Fatalf("expected operator-action error, got %v", err)
	}
	if gate.ariConnected.Load() || gate.journalReady.Load() || store.calls != 0 {
		t.Fatal("malformed journal did not block readiness and projection")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatalf("malformed record was discarded: %v", err)
	}
}
