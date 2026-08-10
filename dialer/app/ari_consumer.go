package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/gorilla/websocket"
)

type ARIConsumer struct {
	store     ariEventStore
	client    *ARIClient
	processor *ARIEventProcessor
	config    Config
	gate      *DependencyGate
	log       *slog.Logger
}

func (c *ARIConsumer) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if !c.config.DialingEnabled {
			c.gate.ariConnected.Store(false)
			c.gate.journalReady.Store(false)
			waitContext(ctx, time.Second)
			continue
		}
		c.gate.ariConnected.Store(false)
		c.gate.journalReady.Store(false)
		if err := c.processor.journal.Probe(); err != nil {
			c.log.Error("ARI journal unavailable", "error", err)
			waitContext(ctx, backoff)
			backoff = min(backoff*2, 15*time.Second)
			continue
		}
		if err := c.processor.Replay(ctx); err != nil {
			if ctx.Err() == nil {
				c.log.Error("ARI journal replay blocked", "error", err)
			}
			waitContext(ctx, backoff)
			backoff = min(backoff*2, 15*time.Second)
			continue
		}
		conn, err := c.client.ConnectEvents(ctx)
		if err != nil {
			_ = c.markDisconnected(ctx)
			c.log.Warn("ARI event connection unavailable", "error", err)
			waitContext(ctx, backoff)
			backoff = min(backoff*2, 15*time.Second)
			continue
		}
		backoff = time.Second
		if err = c.markDisconnected(ctx); err != nil {
			_ = conn.Close()
			waitContext(ctx, backoff)
			continue
		}
		c.gate.journalReady.Store(true)
		c.gate.ariConnected.Store(true)
		c.readEvents(ctx, conn)
		c.gate.ariConnected.Store(false)
		_ = c.markDisconnected(ctx)
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
		if err = c.processor.Handle(ctx, event, raw); err != nil {
			if ctx.Err() == nil && !errors.Is(err, errARIReconnect) {
				c.log.Error("ARI event handling stopped", "event_type", event.Type, "error", err)
			}
			return
		}
	}
}

func (c *ARIConsumer) markDisconnected(ctx context.Context) error {
	dbCtx, cancel := context.WithTimeout(ctx, defaultDBTimeout)
	defer cancel()
	if err := c.store.MarkActiveUncertain(dbCtx); err != nil && ctx.Err() == nil {
		c.log.Error("failed to mark disconnected ARI calls uncertain", "error", err)
		return err
	}
	return ctx.Err()
}
