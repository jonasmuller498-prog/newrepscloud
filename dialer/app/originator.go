package main

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type deliveryStore interface {
	ClaimOutbox(context.Context) (*OutboxItem, error)
	ResetOutbox(context.Context, string) error
	LoadOriginateRecovery(context.Context, OutboxItem) (*OriginateRecovery, error)
	PrepareOriginate(context.Context, OutboxItem) (OriginateCommand, bool, time.Time, error)
	CompleteOriginate(context.Context, OutboxItem, OriginateResult) (bool, error)
	PreparePlay(context.Context, OutboxItem) (PlayCommand, error)
	CompletePlay(context.Context, OutboxItem, OriginateResult) error
	LoadHangupChannel(context.Context, OutboxItem) (string, error)
	CompleteHangup(context.Context, OutboxItem, OriginateResult) error
	DeferOutbox(context.Context, string, time.Time) error
}

type Originator struct {
	store   deliveryStore
	config  Config
	client  ARICommands
	gate    *DependencyGate
	metrics *Metrics
	log     *slog.Logger
}

func (o *Originator) Run(ctx context.Context) {
	var workers sync.WaitGroup
	for i := 0; i < o.config.MaxConcurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			o.worker(ctx)
		}()
	}
	workers.Wait()
}

func (o *Originator) worker(ctx context.Context) {
	for ctx.Err() == nil {
		if !o.config.DialingEnabled || !o.gate.ReadyForDial() {
			waitContext(ctx, 250*time.Millisecond)
			continue
		}
		claimCtx, claimCancel := context.WithTimeout(ctx, defaultDBTimeout)
		item, err := o.store.ClaimOutbox(claimCtx)
		claimCancel()
		if err != nil {
			o.log.Error("outbox claim failed", "error", err)
			waitContext(ctx, 250*time.Millisecond)
			continue
		}
		if item == nil {
			waitContext(ctx, 25*time.Millisecond)
			continue
		}
		if !o.gate.ReadyForDial() {
			_ = o.store.ResetOutbox(ctx, item.ID)
			continue
		}
		if err = o.process(ctx, *item); err != nil {
			o.log.Error("ARI action failed", "kind", item.Kind, "error", err)
			_ = o.store.DeferOutbox(ctx, item.ID, time.Now().Add(time.Second))
		}
	}
}

func (o *Originator) process(ctx context.Context, item OutboxItem) error {
	switch item.Kind {
	case "ARI_ORIGINATE":
		return o.processOriginate(ctx, item)
	case "ARI_PLAY":
		return o.processPlay(ctx, item)
	case "ARI_HANGUP":
		return o.processHangup(ctx, item)
	default:
		return errConflict
	}
}

func (o *Originator) processOriginate(ctx context.Context, item OutboxItem) error {
	dbCtx, cancel := context.WithTimeout(ctx, defaultDBTimeout)
	recovery, err := o.store.LoadOriginateRecovery(dbCtx, item)
	cancel()
	if err != nil {
		return err
	}
	if recovery != nil {
		return o.recoverOriginate(ctx, item, *recovery)
	}
	dbCtx, cancel = context.WithTimeout(ctx, defaultDBTimeout)
	command, permitted, retryAt, err := o.store.PrepareOriginate(dbCtx, item)
	cancel()
	if err != nil {
		return err
	}
	if !permitted {
		if !retryAt.IsZero() {
			o.metrics.cpsThrottled.Add(1)
		}
		return nil
	}
	callCtx, callCancel := context.WithTimeout(ctx, 15*time.Second)
	result, callErr := o.client.Originate(callCtx, command)
	callCancel()
	if result.Accepted {
		o.metrics.sipAccepted.Add(1)
	} else {
		o.metrics.sipFailed.Add(1)
		if result.Uncertain {
			o.metrics.quarantined.Add(1)
		}
	}
	dbCtx, cancel = context.WithTimeout(ctx, defaultDBTimeout)
	compensate, err := o.store.CompleteOriginate(dbCtx, item, result)
	cancel()
	if err != nil {
		return err
	}
	if compensate && result.Accepted {
		hangCtx, hangCancel := context.WithTimeout(ctx, 10*time.Second)
		_, _ = o.client.Hangup(hangCtx, command.ChannelID)
		hangCancel()
	}
	return callErr
}

func (o *Originator) processPlay(ctx context.Context, item OutboxItem) error {
	dbCtx, cancel := context.WithTimeout(ctx, defaultDBTimeout)
	command, err := o.store.PreparePlay(dbCtx, item)
	cancel()
	var result OriginateResult
	if err == nil {
		callCtx, callCancel := context.WithTimeout(ctx, 10*time.Second)
		result, err = o.client.Play(callCtx, command.ChannelID,
			command.PlaybackID, command.MediaSHA)
		callCancel()
	} else {
		result = OriginateResult{Outcome: "ambiguous"}
	}
	dbCtx, cancel = context.WithTimeout(ctx, defaultDBTimeout)
	completeErr := o.store.CompletePlay(dbCtx, item, result)
	cancel()
	if completeErr != nil {
		return completeErr
	}
	return err
}

func (o *Originator) processHangup(ctx context.Context, item OutboxItem) error {
	dbCtx, cancel := context.WithTimeout(ctx, defaultDBTimeout)
	channelID, err := o.store.LoadHangupChannel(dbCtx, item)
	cancel()
	if err != nil {
		return err
	}
	callCtx, callCancel := context.WithTimeout(ctx, 10*time.Second)
	result, callErr := o.client.Hangup(callCtx, channelID)
	callCancel()
	dbCtx, cancel = context.WithTimeout(ctx, defaultDBTimeout)
	err = o.store.CompleteHangup(dbCtx, item, result)
	cancel()
	if err != nil {
		return err
	}
	return callErr
}

func waitContext(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
