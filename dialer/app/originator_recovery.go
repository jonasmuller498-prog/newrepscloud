package main

import (
	"context"
	"time"
)

func (o *Originator) recoverOriginate(
	ctx context.Context, item OutboxItem, recovery OriginateRecovery,
) error {
	result := OriginateResult{Accepted: recovery.State != "ORIGINATING"}
	if !result.Accepted {
		lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		exists, err := o.client.ChannelExists(lookupCtx, recovery.ChannelID)
		cancel()
		if err != nil {
			return err
		}
		if exists {
			result.Accepted = true
		} else {
			result.Outcome = "ambiguous"
			result.Uncertain = true
		}
	}
	dbCtx, cancel := context.WithTimeout(ctx, defaultDBTimeout)
	compensate, err := o.store.CompleteOriginate(dbCtx, item, result)
	cancel()
	if err != nil {
		return err
	}
	if compensate && result.Accepted {
		hangCtx, hangCancel := context.WithTimeout(ctx, 10*time.Second)
		_, _ = o.client.Hangup(hangCtx, recovery.ChannelID)
		hangCancel()
	}
	return nil
}
