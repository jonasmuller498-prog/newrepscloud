package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

const campaignSelect = `SELECT id, name, state, message_asset_id, caller_id_id,
	dnc_attested_at, scheduled_at, created_at FROM campaigns`

type CampaignInput struct {
	Name          string     `json:"name"`
	CallerID      *string    `json:"caller_id_id"`
	DNCAttestedAt *time.Time `json:"dnc_attested_at"`
}

func (s *Store) CreateCampaign(ctx context.Context, in CampaignInput, actor string) (Campaign, error) {
	var c Campaign
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 200 {
		return c, errSafetyBlocked
	}
	id, err := newUUID()
	if err != nil {
		return c, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer tx.Rollback(ctx)
	err = scanCampaign(tx.QueryRow(ctx, `INSERT INTO campaigns
		(id,name,caller_id_id,dnc_attested_at,window_start,window_end,created_by)
		VALUES($1,$2,$3,$4,$5::time,$6::time,$7)
		RETURNING id,name,state,message_asset_id,caller_id_id,dnc_attested_at,scheduled_at,created_at`,
		id, in.Name, in.CallerID, in.DNCAttestedAt,
		clockString(s.config.WindowStart), clockString(s.config.WindowEnd), actor), &c)
	if err == nil {
		detail, _ := json.Marshal(map[string]string{"name": in.Name})
		err = audit(ctx, tx, actor, "campaign.create", "campaign", id, detail)
	}
	if err != nil {
		return c, err
	}
	return c, tx.Commit(ctx)
}

func (s *Store) GetCampaign(ctx context.Context, id string) (Campaign, error) {
	var c Campaign
	err := scanCampaign(s.pool.QueryRow(ctx, campaignSelect+" WHERE id=$1", id), &c)
	return c, dbError(err)
}

func (s *Store) ListCampaigns(ctx context.Context) ([]Campaign, error) {
	rows, err := s.pool.Query(ctx, campaignSelect+" ORDER BY created_at DESC LIMIT 200")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Campaign
	for rows.Next() {
		var c Campaign
		if err := scanCampaign(rows, &c); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (s *Store) UpdateCampaign(ctx context.Context, id string, in CampaignInput, actor string) (Campaign, error) {
	var c Campaign
	if in.Name != "" {
		in.Name = strings.TrimSpace(in.Name)
		if len(in.Name) > 200 {
			return c, errSafetyBlocked
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer tx.Rollback(ctx)
	err = scanCampaign(tx.QueryRow(ctx, `UPDATE campaigns SET
		name=CASE WHEN $2='' THEN name ELSE $2 END,
		caller_id_id=COALESCE($3,caller_id_id),
		dnc_attested_at=COALESCE($4,dnc_attested_at), updated_at=now()
		WHERE id=$1 AND state='DRAFT'
		RETURNING id,name,state,message_asset_id,caller_id_id,dnc_attested_at,scheduled_at,created_at`,
		id, in.Name, in.CallerID, in.DNCAttestedAt), &c)
	if err == nil {
		err = audit(ctx, tx, actor, "campaign.update", "campaign", id, nil)
	}
	if err != nil {
		return c, dbError(err)
	}
	return c, tx.Commit(ctx)
}

func (s *Store) transitionCampaign(
	ctx context.Context, id, to, actor, action string, from ...string,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current string
	if err = tx.QueryRow(ctx, "SELECT state FROM campaigns WHERE id=$1 FOR UPDATE", id).Scan(&current); err != nil {
		return dbError(err)
	}
	accepted := false
	for _, state := range from {
		accepted = accepted || current == state
	}
	if !accepted || !canTransition(current, to) {
		return errConflict
	}
	if _, err = tx.Exec(ctx, "UPDATE campaigns SET state=$2,updated_at=now() WHERE id=$1", id, to); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, action, "campaign", id, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type rowScanner interface{ Scan(...any) error }

func scanCampaign(row rowScanner, c *Campaign) error {
	return row.Scan(&c.ID, &c.Name, &c.State, &c.MessageAsset, &c.CallerID,
		&c.DNCAttestedAt, &c.ScheduledAt, &c.CreatedAt)
}
