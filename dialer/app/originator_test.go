package main

import (
	"context"
	"testing"
	"time"
)

type fakeDeliveryStore struct {
	permitted  bool
	compensate bool
	retryAt    time.Time
	recovery   *OriginateRecovery
	completed  OriginateResult
}

func (*fakeDeliveryStore) ClaimOutbox(context.Context) (*OutboxItem, error) { return nil, nil }
func (*fakeDeliveryStore) ResetOutbox(context.Context, string) error        { return nil }
func (s *fakeDeliveryStore) LoadOriginateRecovery(
	context.Context, OutboxItem,
) (*OriginateRecovery, error) {
	return s.recovery, nil
}
func (s *fakeDeliveryStore) PrepareOriginate(
	context.Context, OutboxItem,
) (OriginateCommand, bool, time.Time, error) {
	return OriginateCommand{
		AttemptID: "attempt", ChannelID: "dialer-attempt", Phone: "+14155552671",
		CallerID: "+14155550100", MediaSHA: "abc",
	}, s.permitted, s.retryAt, nil
}
func (s *fakeDeliveryStore) CompleteOriginate(
	_ context.Context, _ OutboxItem, result OriginateResult,
) (bool, error) {
	s.completed = result
	return s.compensate, nil
}
func (*fakeDeliveryStore) PreparePlay(context.Context, OutboxItem) (PlayCommand, error) {
	return PlayCommand{}, nil
}
func (*fakeDeliveryStore) CompletePlay(context.Context, OutboxItem, OriginateResult) error {
	return nil
}
func (*fakeDeliveryStore) LoadHangupChannel(context.Context, OutboxItem) (string, error) {
	return "dialer-attempt", nil
}
func (*fakeDeliveryStore) CompleteHangup(
	context.Context, OutboxItem, OriginateResult,
) error {
	return nil
}
func (*fakeDeliveryStore) DeferOutbox(context.Context, string, time.Time) error { return nil }

type fakeARI struct {
	originates, hangups, channelChecks int
	fail                               bool
	channelExists                      bool
}

func (f *fakeARI) Originate(context.Context, OriginateCommand) (OriginateResult, error) {
	f.originates++
	if f.fail {
		return OriginateResult{Outcome: "temporary"}, nil
	}
	return OriginateResult{Accepted: true}, nil
}
func (*fakeARI) Play(context.Context, string, string, string) (OriginateResult, error) {
	return OriginateResult{Accepted: true}, nil
}
func (f *fakeARI) Hangup(context.Context, string) (OriginateResult, error) {
	f.hangups++
	return OriginateResult{Accepted: true}, nil
}
func (f *fakeARI) ChannelExists(context.Context, string) (bool, error) {
	f.channelChecks++
	return f.channelExists, nil
}

func TestCPSPermitIsRequiredAtDelivery(t *testing.T) {
	store, ari := &fakeDeliveryStore{
		permitted: false, retryAt: time.Now().Add(time.Second),
	}, &fakeARI{}
	metrics := &Metrics{}
	originator := &Originator{store: store, client: ari, metrics: metrics}
	if err := originator.processOriginate(context.Background(),
		OutboxItem{Kind: "ARI_ORIGINATE"}); err != nil {
		t.Fatal(err)
	}
	if ari.originates != 0 {
		t.Fatal("ARI originate called without a delivery-time CPS permit")
	}
	if metrics.cpsThrottled.Load() != 1 {
		t.Fatal("CPS throttle was not counted at the delivery guard")
	}
}

func TestCancellationRaceCompensatesWithHangup(t *testing.T) {
	store := &fakeDeliveryStore{permitted: true, compensate: true}
	ari := &fakeARI{}
	metrics := &Metrics{}
	originator := &Originator{store: store, client: ari, metrics: metrics}
	if err := originator.processOriginate(context.Background(),
		OutboxItem{Kind: "ARI_ORIGINATE"}); err != nil {
		t.Fatal(err)
	}
	if ari.originates != 1 || ari.hangups != 1 {
		t.Fatalf("originates=%d hangups=%d", ari.originates, ari.hangups)
	}
	if metrics.sipAccepted.Load() != 1 {
		t.Fatal("accepted SIP attempt was not counted")
	}
}

func TestFailedSIPAttemptMetric(t *testing.T) {
	metrics := &Metrics{}
	originator := &Originator{
		store:  &fakeDeliveryStore{permitted: true},
		client: &fakeARI{fail: true}, metrics: metrics,
	}
	if err := originator.processOriginate(
		context.Background(), OutboxItem{Kind: "ARI_ORIGINATE"}); err != nil {
		t.Fatal(err)
	}
	if metrics.sipFailed.Load() != 1 || metrics.sipAccepted.Load() != 0 {
		t.Fatalf("accepted=%d failed=%d",
			metrics.sipAccepted.Load(), metrics.sipFailed.Load())
	}
}

func TestOriginatingRecoveryChecksChannelWithoutRedial(t *testing.T) {
	store := &fakeDeliveryStore{recovery: &OriginateRecovery{
		ChannelID: "dialer-attempt", State: "ORIGINATING",
	}}
	ari := &fakeARI{channelExists: true}
	originator := &Originator{store: store, client: ari, metrics: &Metrics{}}
	if err := originator.processOriginate(
		context.Background(), OutboxItem{Kind: "ARI_ORIGINATE"}); err != nil {
		t.Fatal(err)
	}
	if ari.originates != 0 || ari.channelChecks != 1 || !store.completed.Accepted {
		t.Fatalf("originates=%d checks=%d result=%+v",
			ari.originates, ari.channelChecks, store.completed)
	}
}
