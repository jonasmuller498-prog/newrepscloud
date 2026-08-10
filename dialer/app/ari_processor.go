package main

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

var errARIReconnect = errors.New("ARI persistence recovered; reconnect required")

type ariEventStore interface {
	ApplyARIEventWithKey(context.Context, ARIEvent, string) (string, bool, error)
	AttemptTerminationPending(context.Context, string) (bool, error)
	MarkActiveUncertain(context.Context) error
}

type ariHangupper interface {
	Hangup(context.Context, string) (OriginateResult, error)
}

type ARIEventProcessor struct {
	store   ariEventStore
	client  ariHangupper
	journal *EventJournal
	gate    *DependencyGate
	metrics *Metrics
	log     *slog.Logger
}

func (p *ARIEventProcessor) Handle(
	ctx context.Context, event ARIEvent, raw []byte,
) error {
	if !event.StateChanging() {
		return nil
	}
	record, err := newARIJournalRecord(event, event.Key(raw))
	if err != nil {
		p.metrics.ariEventsRejected.Add(1)
		if p.log != nil {
			p.log.Warn("unsafe ARI event ignored", "event_type", event.Type, "error", err)
		}
		return nil
	}
	entry, err := p.putUntilDurable(ctx, record)
	if err != nil {
		return err
	}
	if record.IsOptOut() {
		p.hangup(ctx, record.ChannelID)
	}
	if err = p.projectUntilCommitted(ctx, entry); err != nil {
		return err
	}
	needsReconnect := !p.gate.ariConnected.Load()
	p.gate.journalReady.Store(true)
	if needsReconnect {
		return errARIReconnect
	}
	return nil
}

func (p *ARIEventProcessor) Replay(ctx context.Context) error {
	p.gate.ariConnected.Store(false)
	p.gate.journalReady.Store(false)
	entries, err := p.journal.Entries()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Record.IsOptOut() {
			p.hangup(ctx, entry.Record.ChannelID)
		}
		if err = p.projectUntilCommitted(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}

func (p *ARIEventProcessor) putUntilDurable(
	ctx context.Context, record ARIJournalRecord,
) (journalEntry, error) {
	delay := 100 * time.Millisecond
	for {
		entry, err := p.journal.Put(record)
		if err == nil {
			return entry, nil
		}
		p.markFailed()
		p.logFailure("ARI journal write failed", record.Type, err)
		if !waitForRetry(ctx, delay) {
			return journalEntry{}, ctx.Err()
		}
		delay = min(delay*2, 5*time.Second)
	}
}

func (p *ARIEventProcessor) projectUntilCommitted(
	ctx context.Context, entry journalEntry,
) error {
	delay := 100 * time.Millisecond
	var attemptID string
	var inserted bool
	for {
		dbCtx, cancel := context.WithTimeout(ctx, defaultDBTimeout)
		id, added, err := p.store.ApplyARIEventWithKey(
			dbCtx, entry.Record.Event(), entry.Record.Key)
		cancel()
		if err == nil {
			attemptID, inserted = id, added
			break
		}
		p.markFailed()
		if entry.Record.IsOptOut() {
			p.metrics.optOutPersistenceFailures.Add(1)
		}
		p.logFailure("ARI event projection failed", entry.Record.Type, err)
		if !waitForRetry(ctx, delay) {
			return ctx.Err()
		}
		delay = min(delay*2, 5*time.Second)
	}
	if inserted {
		p.metrics.ariEvents.Add(1)
		p.finishBestEffort(ctx, entry.Record, attemptID)
	}
	return p.deleteUntilDurable(ctx, entry)
}

func (p *ARIEventProcessor) deleteUntilDurable(
	ctx context.Context, entry journalEntry,
) error {
	delay := 100 * time.Millisecond
	for {
		if err := p.journal.Delete(entry); err == nil {
			return nil
		} else {
			p.markFailed()
			p.logFailure("ARI journal cleanup failed", entry.Record.Type, err)
		}
		if !waitForRetry(ctx, delay) {
			return ctx.Err()
		}
		delay = min(delay*2, 5*time.Second)
	}
}

func (p *ARIEventProcessor) finishBestEffort(
	ctx context.Context, record ARIJournalRecord, attemptID string,
) {
	if record.Type != "PlaybackFinished" {
		return
	}
	dbCtx, cancel := context.WithTimeout(ctx, defaultDBTimeout)
	pending, err := p.store.AttemptTerminationPending(dbCtx, attemptID)
	cancel()
	if err == nil && pending {
		p.hangup(ctx, record.ChannelID)
	}
}

func (p *ARIEventProcessor) hangup(ctx context.Context, channelID string) {
	hangCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, err := p.client.Hangup(hangCtx, channelID)
	cancel()
	if err != nil {
		p.logFailure("immediate ARI hangup failed", "ChannelDtmfReceived", err)
	}
}

func (p *ARIEventProcessor) markFailed() {
	p.gate.ariConnected.Store(false)
	p.gate.journalReady.Store(false)
}

func (p *ARIEventProcessor) logFailure(message, eventType string, err error) {
	if p.log != nil {
		p.log.Error(message, "event_type", eventType, "error", err)
	}
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
