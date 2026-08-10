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
			c.markDisconnected(ctx)
			c.log.Warn("ARI event connection unavailable", "error", err)
			waitContext(ctx, backoff)
			backoff = min(backoff*2, 15*time.Second)
			continue
		}
		backoff = time.Second
		c.markDisconnected(ctx)
		c.gate.ariConnected.Store(true)
		c.readEvents(ctx, conn)
		c.gate.ariConnected.Store(false)
		c.markDisconnected(ctx)
		_ = conn.Close()
	}
}

func (c *ARIConsumer) readEvents(ctx context.Context, conn *websocket.Conn) {
	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
					_ = conn.Close()
					return
				}
			}
		}
	}()
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
	attemptID, inserted, err := c.store.ApplyARIEvent(eventCtx, event, raw)
	if err != nil {
		return err
	}
	if inserted {
		c.metrics.ariEvents.Add(1)
	}
	if inserted && (event.Type == "PlaybackFinished" ||
		(event.Type == "ChannelDtmfReceived" && event.Digit == "9")) {
		pending, pendingErr := c.store.AttemptTerminationPending(eventCtx, attemptID)
		if pendingErr != nil {
			return pendingErr
		}
		if pending {
			hangCtx, hangCancel := context.WithTimeout(ctx, 10*time.Second)
			_, _ = c.client.Hangup(hangCtx, event.ChannelID())
			hangCancel()
		}
	}
	return nil
}

func (c *ARIConsumer) markDisconnected(ctx context.Context) {
	dbCtx, cancel := context.WithTimeout(ctx, defaultDBTimeout)
	defer cancel()
	if err := c.store.MarkActiveUncertain(dbCtx); err != nil && ctx.Err() == nil {
		c.log.Error("failed to mark disconnected ARI calls uncertain", "error", err)
	}
}
