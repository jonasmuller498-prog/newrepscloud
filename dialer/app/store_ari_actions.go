package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type PlayCommand struct {
	ChannelID, PlaybackID, MediaSHA string
}

func (s *Store) PreparePlay(ctx context.Context, item OutboxItem) (PlayCommand, error) {
	var cmd PlayCommand
	var media MediaSpec
	err := s.pool.QueryRow(ctx, `SELECT a.channel_id,a.playback_id,ma.storage_name,
		ma.sha256,ma.byte_size,ma.duration_ms FROM outbox o
		JOIN call_attempts a ON a.id=o.aggregate_id
		JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		JOIN campaigns c ON c.id=cr.campaign_id
		JOIN message_assets ma ON ma.id=c.message_asset_id
		JOIN campaign_approvals ca ON ca.campaign_id=c.id
		  AND ca.message_asset_id=ma.id AND ca.caller_id_id=c.caller_id_id
		WHERE o.id=$1 AND o.state='PROCESSING' AND o.kind='ARI_PLAY'
		  AND a.id=$2 AND a.state='ANSWERED' AND a.pending_outcome IS NULL`,
		item.ID, item.AttemptID).Scan(&cmd.ChannelID, &cmd.PlaybackID,
		&media.StorageName, &media.SHA256, &media.ByteSize, &media.DurationMS)
	if err != nil {
		return cmd, dbError(err)
	}
	cmd.MediaSHA, err = verifyMediaFile(s.config.MediaDir, media,
		s.config.AssetMaxDuration, s.config.MaxBodyBytes)
	return cmd, err
}

func (s *Store) CompletePlay(
	ctx context.Context, item OutboxItem, result OriginateResult,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if !result.Accepted {
		err = requestTerminationTx(ctx, tx, item.AttemptID, "ambiguous")
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE outbox SET state='DONE',processed_at=now(),
			processing_at=NULL WHERE id=$1`, item.ID)
	}
	return commitResult(ctx, tx, err)
}

func (s *Store) LoadHangupChannel(ctx context.Context, item OutboxItem) (string, error) {
	var channelID string
	err := s.pool.QueryRow(ctx, `SELECT a.channel_id FROM outbox o
		JOIN call_attempts a ON a.id=o.aggregate_id
		WHERE o.id=$1 AND o.state='PROCESSING' AND o.kind='ARI_HANGUP'
		  AND a.id=$2 AND a.state IN ('TERMINATING','UNCERTAIN')`,
		item.ID, item.AttemptID).Scan(&channelID)
	return channelID, dbError(err)
}

func (s *Store) CompleteHangup(
	ctx context.Context, item OutboxItem, result OriginateResult,
) error {
	if result.Uncertain || (!result.Accepted && !result.NotFound) {
		return s.DeferOutbox(ctx, item.ID, time.Now().Add(time.Second))
	}
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET state='DONE',processed_at=now(),
		processing_at=NULL WHERE id=$1 AND state='PROCESSING'`, item.ID)
	return err
}

func (s *Store) DeferOutbox(ctx context.Context, id string, until time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET state='PENDING',processing_at=NULL,
		available_at=$2 WHERE id=$1 AND state='PROCESSING'`, id, until)
	return err
}

func setPlaybackIDTx(ctx context.Context, tx pgx.Tx, attemptID string) (string, error) {
	playbackID := deterministicPlaybackID(attemptID)
	_, err := tx.Exec(ctx, `UPDATE call_attempts SET playback_id=COALESCE(playback_id,$2)
		WHERE id=$1`, attemptID, playbackID)
	return playbackID, err
}
