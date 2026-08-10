package main

import (
	"context"
	"errors"
	"sync"
)

type fakeARIEventStore struct {
	mu         sync.Mutex
	failures   int
	alwaysFail bool
	inserted   bool
	calls      int
	keys       []string
	order      *[]string
}

func (s *fakeARIEventStore) ApplyARIEventWithKey(
	_ context.Context, _ ARIEvent, key string,
) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.keys = append(s.keys, key)
	if s.order != nil {
		*s.order = append(*s.order, "project")
	}
	if s.alwaysFail || s.failures > 0 {
		if s.failures > 0 {
			s.failures--
		}
		return "", false, errors.New("database unavailable")
	}
	return "attempt-id", s.inserted, nil
}

func (*fakeARIEventStore) AttemptTerminationPending(context.Context, string) (bool, error) {
	return false, nil
}

func (*fakeARIEventStore) MarkActiveUncertain(context.Context) error {
	return nil
}

type fakeARIHangupper struct {
	fn func(string)
}

func (h fakeARIHangupper) Hangup(
	_ context.Context, channelID string,
) (OriginateResult, error) {
	if h.fn != nil {
		h.fn(channelID)
	}
	return OriginateResult{Accepted: true}, nil
}

func testARIEvent(eventType string) ARIEvent {
	event := ARIEvent{Type: eventType}
	event.Channel.ID = "dialer-0123456789abcdef0123456789abcdef"
	event.Channel.State = "Up"
	if eventType == "PlaybackStarted" || eventType == "PlaybackFinished" {
		event.Playback.ID = "play-0123456789abcdef0123456789abcdef"
	}
	if eventType == "ChannelDtmfReceived" {
		event.Digit = "9"
	}
	return event
}

func newTestProcessor(
	dir string, store ariEventStore, hangupper ariHangupper, gate *DependencyGate,
	metrics *Metrics,
) *ARIEventProcessor {
	return &ARIEventProcessor{
		store: store, client: hangupper, journal: NewEventJournal(dir),
		gate: gate, metrics: metrics,
	}
}
