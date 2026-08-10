package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/gorilla/websocket"
)

type ARIConsumer struct {
	store   *Store
	client  *ARIClient
	gate    *DependencyGate
	metrics *Metrics
	log     *slog.Logger
}

func (c *ARIConsumer) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if !c.store.config.DialingEnabled {
			c.gate.ariConnected.Store(false)
			waitContext(ctx, time.Second)
			continue
		}
		conn, err := c.client.ConnectEvents(ctx)
		if err != nil {
			c.gate.ariConnected.Store(false)
			c.log.Warn("ARI event connection unavailable", "error", err)
			waitContext(ctx, backoff)
			backoff = min(backoff*2, 15*time.Second)
			continue
		}
		backoff = time.Second
		c.gate.ariConnected.Store(true)
		c.readEvents(ctx, conn)
		c.gate.ariConnected.Store(false)
		_ = conn.Close()
	}
}

func (c *ARIConsumer) readEvents(ctx context.Context, conn *websocket.Conn) {
	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})
	for ctx.Err() == nil {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				c.log.Warn("ARI event stream disconnected", "error", err)
			}
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		event, err := parseARIEvent(raw)
		if err != nil {
			c.log.Warn("invalid ARI event JSON")
			continue
		}
		if err = c.handleEvent(ctx, event, raw); err != nil && !errors.Is(err, errNotFound) {
			c.log.Error("ARI event persistence failed", "event_type", event.Type, "error", err)
		}
	}
}

func (c *ARIConsumer) handleEvent(ctx context.Context, event ARIEvent, raw []byte) error {
	eventCtx, cancel := context.WithTimeout(ctx, defaultDBTimeout)
	defer cancel()
	attemptID, previous, inserted, err := c.store.PersistARIEvent(eventCtx, event, raw)
	if err != nil {
		return err
	}
	if inserted {
		c.metrics.ariEvents.Add(1)
	}
	if event.Type == "ChannelDtmfReceived" && event.Digit == "9" {
		return c.store.OptOutAttempt(eventCtx, attemptID, "ari_dtmf_9", "ari:"+c.store.config.ARIApp)
	}
	if state := eventState(event); state != "" {
		return c.store.UpdateAttemptState(eventCtx, attemptID, state)
	}
	switch event.Type {
	case "PlaybackFinished":
		return c.store.FinishAttempt(eventCtx, attemptID, "completed")
	case "ChannelDestroyed", "StasisEnd":
		outcome := destroyedOutcome(event, previous)
		if outcome == "ambiguous" {
			c.metrics.quarantined.Add(1)
		}
		return c.store.FinishAttempt(eventCtx, attemptID, outcome)
	}
	return nil
}
