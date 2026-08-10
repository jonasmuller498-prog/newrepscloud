package main

import (
	"context"
	"time"
)

func (s *Store) ValidateCampaign(ctx context.Context, id, actor string, now time.Time) ([]SafetyBlock, error) {
	blocks, err := s.CampaignSafetyBlocks(ctx, id, false, now)
	if err != nil || len(blocks) > 0 {
		return blocks, err
	}
	err = s.transitionCampaign(ctx, id, "VALIDATING", actor, "campaign.validate", "DRAFT")
	return blocks, err
}

func (s *Store) ApproveCampaign(ctx context.Context, id, actor string, now time.Time) ([]SafetyBlock, error) {
	blocks, err := s.CampaignSafetyBlocks(ctx, id, false, now)
	if err != nil || len(blocks) > 0 {
		return blocks, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var state string
	var assetID, callerID *string
	err = tx.QueryRow(ctx, `SELECT state,message_asset_id,caller_id_id
		FROM campaigns WHERE id=$1 FOR UPDATE`, id).Scan(&state, &assetID, &callerID)
	if err != nil {
		return nil, dbError(err)
	}
	if state != "VALIDATING" || assetID == nil || callerID == nil ||
		!canTransition(state, "APPROVED") {
		return nil, errConflict
	}
	approvalID, err := newUUID()
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO campaign_approvals
		(id,campaign_id,message_asset_id,caller_id_id,approver)
		VALUES($1,$2,$3,$4,$5)`, approvalID, id, *assetID, *callerID, actor)
	if err == nil {
		_, err = tx.Exec(ctx, "UPDATE campaigns SET state='APPROVED',updated_at=now() WHERE id=$1", id)
	}
	if err == nil {
		err = audit(ctx, tx, actor, "campaign.approve", "campaign", id, nil)
	}
	if err != nil {
		return nil, err
	}
	return nil, tx.Commit(ctx)
}

func (s *Store) ScheduleCampaign(ctx context.Context, id, actor string, at time.Time) error {
	if at.Before(time.Now().Add(-time.Minute)) {
		return errSafetyBlocked
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE campaigns SET state='SCHEDULED',scheduled_at=$2,updated_at=now()
		WHERE id=$1 AND state='APPROVED'`, id, at.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errConflict
	}
	if err = audit(ctx, tx, actor, "campaign.schedule", "campaign", id, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) StartCampaign(
	ctx context.Context, id, actor string, now time.Time, systemReady bool,
) ([]SafetyBlock, error) {
	blocks, err := s.CampaignSafetyBlocks(ctx, id, true, now)
	if err != nil {
		return nil, err
	}
	if !s.config.DialingEnabled {
		blocks = append(blocks, SafetyBlock{"dialing_disabled", "DIALING_ENABLED is false.", 0})
	}
	if s.config.CPS <= 0 {
		blocks = append(blocks, SafetyBlock{"cps_zero", "CPS must be greater than zero.", 0})
	}
	if !systemReady {
		blocks = append(blocks, SafetyBlock{"dependencies_unready", "ARI or media storage is unavailable.", 0})
	}
	if len(blocks) > 0 {
		return blocks, errSafetyBlocked
	}
	var scheduledAt *time.Time
	var state string
	err = s.pool.QueryRow(ctx, "SELECT state,scheduled_at FROM campaigns WHERE id=$1", id).
		Scan(&state, &scheduledAt)
	if err != nil {
		return nil, dbError(err)
	}
	if state == "SCHEDULED" && (scheduledAt == nil || scheduledAt.After(now)) {
		return []SafetyBlock{{"schedule_pending", "The scheduled time has not arrived.", 0}}, errSafetyBlocked
	}
	err = s.transitionCampaign(ctx, id, "RUNNING", actor, "campaign.start", "SCHEDULED", "PAUSED")
	return nil, err
}

func (s *Store) PauseCampaign(ctx context.Context, id, actor string) error {
	return s.stopCampaignDelivery(ctx, id, actor, true)
}

func (s *Store) CancelCampaign(ctx context.Context, id, actor string) error {
	return s.stopCampaignDelivery(ctx, id, actor, false)
}
