package main

import (
	"fmt"
	"testing"
	"time"
)

func TestClosedWindowDefersWithoutConsumingAttempt(t *testing.T) {
	store, ctx := integrationStore(t, 1)
	_, _ = seedQueue(t, ctx, store)
	attempt, err := store.AllocateAttempt(ctx)
	if err != nil || attempt == nil {
		t.Fatalf("allocate=%+v err=%v", attempt, err)
	}
	var campaignID string
	err = store.pool.QueryRow(ctx, `SELECT cr.campaign_id FROM call_attempts a
		JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		WHERE a.id=$1`, attempt.ID).Scan(&campaignID)
	if err != nil {
		t.Fatal(err)
	}
	local, _ := time.LoadLocation("America/New_York")
	start := 3
	if time.Now().In(local).Hour() >= 2 {
		start = 0
	}
	_, err = store.pool.Exec(ctx, `UPDATE campaigns SET window_start=$2,
		window_end=$3 WHERE id=$1`, campaignID,
		fmt.Sprintf("%02d:00", start), fmt.Sprintf("%02d:00", start+1))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.ClaimOutbox(ctx)
	if err != nil || item == nil {
		t.Fatalf("claim=%+v err=%v", item, err)
	}
	_, permitted, retryAt, err := store.PrepareOriginate(ctx, *item)
	if err != nil || permitted || !retryAt.IsZero() {
		t.Fatalf("permitted=%v retry=%v err=%v", permitted, retryAt, err)
	}
	var state, outcome, status string
	var attempts, claims, occupied int
	var deferred bool
	err = store.pool.QueryRow(ctx, `SELECT a.state,a.outcome,cr.status,
		cr.attempt_count,cr.claim_count,cr.next_attempt_at>clock_timestamp(),
		(SELECT count(*) FROM dialer_slots WHERE attempt_id=a.id)
		FROM call_attempts a JOIN campaign_recipients cr
		  ON cr.id=a.campaign_recipient_id WHERE a.id=$1`, attempt.ID).
		Scan(&state, &outcome, &status, &attempts, &claims, &deferred, &occupied)
	if err != nil || state != "CANCELLED" || outcome != "window_closed" ||
		status != "QUEUED" || attempts != 0 || claims != 1 || !deferred || occupied != 0 {
		t.Fatalf("state=%s outcome=%s status=%s attempts=%d claims=%d deferred=%v slot=%d err=%v",
			state, outcome, status, attempts, claims, deferred, occupied, err)
	}
}

func TestPauseReleasesClaimWithoutConsumingAttempt(t *testing.T) {
	store, ctx := integrationStore(t, 1)
	_, _ = seedQueue(t, ctx, store)
	attempt, err := store.AllocateAttempt(ctx)
	if err != nil || attempt == nil {
		t.Fatalf("allocate=%+v err=%v", attempt, err)
	}
	var campaignID string
	err = store.pool.QueryRow(ctx, `SELECT cr.campaign_id FROM call_attempts a
		JOIN campaign_recipients cr ON cr.id=a.campaign_recipient_id
		WHERE a.id=$1`, attempt.ID).Scan(&campaignID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.PauseCampaign(ctx, campaignID, "v1:operator"); err != nil {
		t.Fatal(err)
	}
	var state, status string
	var attempts, claims, occupied int
	err = store.pool.QueryRow(ctx, `SELECT a.state,cr.status,cr.attempt_count,
		cr.claim_count,(SELECT count(*) FROM dialer_slots WHERE attempt_id=a.id)
		FROM call_attempts a JOIN campaign_recipients cr
		  ON cr.id=a.campaign_recipient_id WHERE a.id=$1`, attempt.ID).
		Scan(&state, &status, &attempts, &claims, &occupied)
	if err != nil || state != "CANCELLED" || status != "QUEUED" ||
		attempts != 0 || claims != 1 || occupied != 0 {
		t.Fatalf("state=%s status=%s attempts=%d claims=%d slot=%d err=%v",
			state, status, attempts, claims, occupied, err)
	}
}
