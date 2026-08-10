package main

import (
	"context"
	"testing"
	"time"
)

type fakeDeliveryStore struct {
	permitted  bool
	compensate bool
}

func (*fakeDeliveryStore) ClaimOutbox(context.Context) (*OutboxItem, error) { return nil, nil }
func (*fakeDeliveryStore) ResetOutbox(context.Context, string) error        { return nil }
func (s *fakeDeliveryStore) PrepareOriginate(
	context.Context, OutboxItem,
) (OriginateCommand, bool, time.Time, error) {
	return OriginateCommand{
		AttemptID: "attempt", ChannelID: "dialer-attempt", Phone: "+14155552671",
		CallerID: "+14155550100", MediaSHA: "abc",
	}, s.permitted, time.Time{}, nil
}
func (s *fakeDeliveryStore) CompleteOriginate(
	context.Context, OutboxItem, OriginateResult,
) (bool, error) {
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
	originates, hangups int
}

func (f *fakeARI) Originate(context.Context, OriginateCommand) (OriginateResult, error) {
	f.originates++
	return OriginateResult{Accepted: true}, nil
}
func (*fakeARI) Play(context.Context, string, string, string) (OriginateResult, error) {
	return OriginateResult{Accepted: true}, nil
}
func (f *fakeARI) Hangup(context.Context, string) (OriginateResult, error) {
	f.hangups++
	return OriginateResult{Accepted: true}, nil
}
func (*fakeARI) ChannelExists(context.Context, string) (bool, error) { return false, nil }

func TestCPSPermitIsRequiredAtDelivery(t *testing.T) {
	store, ari := &fakeDeliveryStore{permitted: false}, &fakeARI{}
	originator := &Originator{store: store, client: ari, metrics: &Metrics{}}
	if err := originator.processOriginate(context.Background(),
		OutboxItem{Kind: "ARI_ORIGINATE"}); err != nil {
		t.Fatal(err)
	}
	if ari.originates != 0 {
		t.Fatal("ARI originate called without a delivery-time CPS permit")
	}
}

func TestCancellationRaceCompensatesWithHangup(t *testing.T) {
	store := &fakeDeliveryStore{permitted: true, compensate: true}
	ari := &fakeARI{}
	originator := &Originator{store: store, client: ari, metrics: &Metrics{}}
	if err := originator.processOriginate(context.Background(),
		OutboxItem{Kind: "ARI_ORIGINATE"}); err != nil {
		t.Fatal(err)
	}
	if ari.originates != 1 || ari.hangups != 1 {
		t.Fatalf("originates=%d hangups=%d", ari.originates, ari.hangups)
	}
}
