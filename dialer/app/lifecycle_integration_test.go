package main

import (
	"testing"
	"time"
)

func TestARIEventReplayAndOptOutSlotRetention(t *testing.T) {
	store, ctx := integrationStore(t, 1)
	_, _ = seedQueue(t, ctx, store)
	attempt, err := store.AllocateAttempt(ctx)
	if err != nil || attempt == nil {
		t.Fatalf("allocate=%+v err=%v", attempt, err)
	}
	startRaw := []byte(`{"type":"StasisStart","channel":{"id":"` +
		attempt.ChannelID + `","state":"Up"}}`)
	start, _ := parseARIEvent(startRaw)
	key := start.Key(startRaw)
	if _, inserted, applyErr := store.ApplyARIEventWithKey(
		ctx, start, key); applyErr != nil || !inserted {
		t.Fatalf("first event inserted=%v err=%v", inserted, applyErr)
	}
	if _, inserted, applyErr := store.ApplyARIEventWithKey(
		ctx, start, key); applyErr != nil || inserted {
		t.Fatalf("replay inserted=%v err=%v", inserted, applyErr)
	}
	var state, playbackID string
	var playJobs, processed int
	err = store.pool.QueryRow(ctx, `SELECT state,playback_id,
		(SELECT count(*) FROM outbox WHERE aggregate_id=$1 AND kind='ARI_PLAY'),
		(SELECT count(*) FROM call_events WHERE attempt_id=$1 AND processed_at IS NOT NULL)
		FROM call_attempts WHERE id=$1`, attempt.ID).
		Scan(&state, &playbackID, &playJobs, &processed)
	if err != nil || state != "ANSWERED" || playbackID == "" ||
		playJobs != 1 || processed != 1 {
		t.Fatalf("state=%s playback=%s jobs=%d processed=%d err=%v",
			state, playbackID, playJobs, processed, err)
	}
	dtmfRaw := []byte(`{"type":"ChannelDtmfReceived","digit":"9","channel":{"id":"` +
		attempt.ChannelID + `"}}`)
	dtmf, _ := parseARIEvent(dtmfRaw)
	if _, _, err = store.ApplyARIEvent(ctx, dtmf, dtmfRaw); err != nil {
		t.Fatal(err)
	}
	var occupied, suppressions, hangups int
	err = store.pool.QueryRow(ctx, `SELECT a.state,
		(SELECT count(*) FROM dialer_slots WHERE attempt_id=a.id),
		(SELECT count(*) FROM suppressions),
		(SELECT count(*) FROM outbox WHERE aggregate_id=a.id AND kind='ARI_HANGUP')
		FROM call_attempts a WHERE a.id=$1`, attempt.ID).
		Scan(&state, &occupied, &suppressions, &hangups)
	if err != nil || state != "TERMINATING" || occupied != 1 ||
		suppressions != 1 || hangups != 1 {
		t.Fatalf("state=%s slot=%d suppressions=%d hangups=%d err=%v",
			state, occupied, suppressions, hangups, err)
	}
	endRaw := []byte(`{"type":"StasisEnd","channel":{"id":"` + attempt.ChannelID + `"}}`)
	end, _ := parseARIEvent(endRaw)
	if _, _, err = store.ApplyARIEvent(ctx, end, endRaw); err != nil {
		t.Fatal(err)
	}
	err = store.pool.QueryRow(ctx, `SELECT state,
		(SELECT count(*) FROM dialer_slots WHERE attempt_id=$1)
		FROM call_attempts WHERE id=$1`, attempt.ID).Scan(&state, &occupied)
	if err != nil || state != "OPT_OUT" || occupied != 0 {
		t.Fatalf("terminal state=%s slot=%d err=%v", state, occupied, err)
	}
}

func TestAsyncImportIdempotency(t *testing.T) {
	store, ctx := integrationStore(t, 1)
	campaign, err := store.CreateCampaign(ctx,
		CampaignInput{Name: "import-test"}, "v1:test-actor")
	if err != nil {
		t.Fatal(err)
	}
	rows := []ImportRow{{
		Phone: "+14155552671", Timezone: "UTC", ConsentSource: "form",
		ConsentAt: time.Now().Add(-time.Hour).UTC(), Line: 2,
	}}
	first, err := store.CreateImportJob(ctx, campaign.ID, "v1:test-actor", "request-123", rows)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateImportJob(ctx, campaign.ID, "v1:test-actor", "request-123", rows)
	if err != nil || second.ID != first.ID {
		t.Fatalf("idempotent job first=%s second=%s err=%v", first.ID, second.ID, err)
	}
	claimed, err := store.ClaimImportJob(ctx)
	if err != nil || claimed != first.ID {
		t.Fatalf("claimed=%s err=%v", claimed, err)
	}
	if err = store.ProcessImportJob(ctx, claimed); err != nil {
		t.Fatal(err)
	}
	job, err := store.GetImportJob(ctx, first.ID)
	if err != nil || job.State != "COMPLETED" || job.Inserted != 1 {
		t.Fatalf("job=%+v err=%v", job, err)
	}
}
