package main

import (
	"context"
	"testing"
	"time"
)

func TestOriginatingOutboxRecoversWithoutRedial(t *testing.T) {
	store, ctx, attempt, item, _ := prepareIntegrationOriginate(t)
	if err := store.DeferOutbox(ctx, item.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := store.ClaimOutbox(ctx)
	if err != nil || reclaimed == nil || reclaimed.ID != item.ID {
		t.Fatalf("reclaimed=%+v err=%v", reclaimed, err)
	}
	recovery, err := store.LoadOriginateRecovery(ctx, *reclaimed)
	if err != nil || recovery == nil || recovery.State != "ORIGINATING" ||
		recovery.ChannelID != attempt.ChannelID {
		t.Fatalf("recovery=%+v err=%v", recovery, err)
	}
	_, err = store.CompleteOriginate(ctx, *reclaimed, OriginateResult{
		Outcome: "ambiguous", Uncertain: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var attemptState, outboxState string
	var attemptCount int
	err = store.pool.QueryRow(ctx, `SELECT a.state,o.state,cr.attempt_count
		FROM call_attempts a JOIN outbox o ON o.aggregate_id=a.id
		  AND o.kind='ARI_ORIGINATE'
		JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		WHERE a.id=$1`, attempt.ID).
		Scan(&attemptState, &outboxState, &attemptCount)
	if err != nil || attemptState != "UNCERTAIN" || outboxState != "DONE" ||
		attemptCount != 1 {
		t.Fatalf("attempt=%s outbox=%s count=%d err=%v",
			attemptState, outboxState, attemptCount, err)
	}
}

func TestPauseStopsAnsweredCallAndQuarantinesRetry(t *testing.T) {
	store, ctx, attempt, item, campaignID := prepareIntegrationOriginate(t)
	if _, err := store.CompleteOriginate(ctx, *item, OriginateResult{Accepted: true}); err != nil {
		t.Fatal(err)
	}
	_, err := store.pool.Exec(ctx, `UPDATE call_attempts SET state='ANSWERED',
		answered_at=now(),updated_at=now() WHERE id=$1`, attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.PauseCampaign(ctx, campaignID, "v1:operator"); err != nil {
		t.Fatal(err)
	}
	var state, pending string
	var hangups int
	err = store.pool.QueryRow(ctx, `SELECT state,pending_outcome,
		(SELECT count(*) FROM outbox WHERE aggregate_id=$1 AND kind='ARI_HANGUP')
		FROM call_attempts WHERE id=$1`, attempt.ID).Scan(&state, &pending, &hangups)
	if err != nil || state != "TERMINATING" || pending != "cancelled" || hangups != 1 {
		t.Fatalf("state=%s pending=%s hangups=%d err=%v", state, pending, hangups, err)
	}
	if err = store.ConfirmChannelAbsent(ctx, attempt.ID); err != nil {
		t.Fatal(err)
	}
	var status string
	err = store.pool.QueryRow(ctx, `SELECT status FROM campaign_recipients
		WHERE id=$1`, attempt.CampaignRecipientID).Scan(&status)
	if err != nil || status != "QUARANTINED" {
		t.Fatalf("status=%s err=%v", status, err)
	}
}

func prepareIntegrationOriginate(
	t *testing.T,
) (*Store, context.Context, *QueuedAttempt, *OutboxItem, string) {
	t.Helper()
	store, ctx := integrationStore(t, 1)
	_, _ = seedQueue(t, ctx, store)
	attempt, err := store.AllocateAttempt(ctx)
	if err != nil || attempt == nil {
		t.Fatalf("allocate=%+v err=%v", attempt, err)
	}
	phone, _ := store.protector.Encrypt("+14155552671")
	caller, _ := store.protector.Encrypt("+14155550100")
	var campaignID string
	err = store.pool.QueryRow(ctx, `SELECT cr.campaign_id FROM call_attempts a
		JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		WHERE a.id=$1`, attempt.ID).Scan(&campaignID)
	if err == nil {
		_, err = store.pool.Exec(ctx, `UPDATE recipients r SET phone_cipher=$2
			FROM call_attempts a WHERE a.recipient_id=r.id AND a.id=$1`,
			attempt.ID, phone)
	}
	if err == nil {
		_, err = store.pool.Exec(ctx, `UPDATE caller_ids ci SET phone_cipher=$2
			FROM campaigns c WHERE c.caller_id_id=ci.id AND c.id=$1`,
			campaignID, caller)
	}
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.ClaimOutbox(ctx)
	if err != nil || item == nil {
		t.Fatalf("claim=%+v err=%v", item, err)
	}
	_, permitted, _, err := store.PrepareOriginate(ctx, *item)
	if err != nil || !permitted {
		t.Fatalf("prepare permitted=%v err=%v", permitted, err)
	}
	return store, ctx, attempt, item, campaignID
}
