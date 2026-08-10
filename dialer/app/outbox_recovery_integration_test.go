package main

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestHangupDuplicatePreservesClaimAndLegacyNullRecovers(t *testing.T) {
	store, ctx, attemptID, actionID := pendingHangup(t)
	if err := store.RequestReconcileHangup(ctx, attemptID); err != nil {
		t.Fatal(err)
	}
	assertPendingHangup(t, store, ctx, actionID, attemptID)

	item, err := store.ClaimOutbox(ctx)
	if err != nil || item == nil || item.ID != actionID || item.Kind != "ARI_HANGUP" {
		t.Fatalf("claimed=%+v err=%v", item, err)
	}
	var before time.Time
	if err = store.pool.QueryRow(ctx,
		`SELECT processing_at FROM outbox WHERE id=$1`, actionID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = store.RequestReconcileHangup(ctx, attemptID); err != nil {
		t.Fatal(err)
	}
	var state string
	var after time.Time
	var count int
	err = store.pool.QueryRow(ctx, `SELECT state,processing_at,
		(SELECT count(*) FROM outbox WHERE aggregate_id=$2 AND kind='ARI_HANGUP')
		FROM outbox WHERE id=$1`, actionID, attemptID).Scan(&state, &after, &count)
	if err != nil || state != "PROCESSING" || !after.Equal(before) || count != 1 {
		t.Fatalf("state=%s before=%v after=%v count=%d err=%v",
			state, before, after, count, err)
	}

	if _, err = store.pool.Exec(ctx,
		`UPDATE outbox SET processing_at=NULL WHERE id=$1`, actionID); err != nil {
		t.Fatal(err)
	}
	if err = store.ReconcileDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	assertPendingHangup(t, store, ctx, actionID, attemptID)
	item, err = store.ClaimOutbox(ctx)
	if err != nil || item == nil || item.ID != actionID {
		t.Fatalf("legacy row was not claimable: item=%+v err=%v", item, err)
	}
	if err = store.pool.QueryRow(ctx, `SELECT count(*) FROM outbox
		WHERE aggregate_id=$1 AND kind='ARI_HANGUP'`, attemptID).Scan(&count); err != nil ||
		count != 1 {
		t.Fatalf("hangup actions=%d err=%v", count, err)
	}
}

func TestHangupTerminalActionsRequeueWithResetTimestamps(t *testing.T) {
	store, ctx, attemptID, actionID := pendingHangup(t)
	for _, terminal := range []string{"DONE", "CANCELLED"} {
		_, err := store.pool.Exec(ctx, `UPDATE outbox SET state=$2,
			processing_at=now(),processed_at=now(),
			available_at=now()+interval '1 hour' WHERE id=$1`, actionID, terminal)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.RequestReconcileHangup(ctx, attemptID); err != nil {
			t.Fatal(err)
		}
		assertPendingHangup(t, store, ctx, actionID, attemptID)
	}
}

func TestAbandonedClaimReleasesRecipientAndSlot(t *testing.T) {
	store, ctx := integrationStore(t, 1)
	_, _ = seedQueue(t, ctx, store)
	attempt, err := store.AllocateAttempt(ctx)
	if err != nil || attempt == nil {
		t.Fatalf("allocate=%+v err=%v", attempt, err)
	}
	_, err = store.pool.Exec(ctx, `UPDATE outbox SET state='DONE',processed_at=now()
		WHERE aggregate_id=$1 AND kind='ARI_ORIGINATE'`,
		attempt.ID)
	if err == nil {
		_, err = store.pool.Exec(ctx, `UPDATE call_attempts
			SET updated_at=now()-interval '3 minutes' WHERE id=$1`, attempt.ID)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ReconcileDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	var state, status string
	var attemptCount, occupied int
	err = store.pool.QueryRow(ctx, `SELECT a.state,cr.status,cr.attempt_count,
		(SELECT count(*) FROM dialer_slots WHERE attempt_id=a.id)
		FROM call_attempts a JOIN campaign_recipients cr
		  ON cr.id=a.campaign_recipient_id WHERE a.id=$1`, attempt.ID).
		Scan(&state, &status, &attemptCount, &occupied)
	if err != nil || state != "CANCELLED" || status != "QUEUED" ||
		attemptCount != 0 || occupied != 0 {
		t.Fatalf("state=%s status=%s count=%d slot=%d err=%v",
			state, status, attemptCount, occupied, err)
	}
}

func pendingHangup(t *testing.T) (*Store, context.Context, string, string) {
	t.Helper()
	store, ctx := integrationStore(t, 1)
	_, _ = seedQueue(t, ctx, store)
	attempt, err := store.AllocateAttempt(ctx)
	if err != nil || attempt == nil {
		t.Fatalf("allocate=%+v err=%v", attempt, err)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE call_attempts
		SET state='ANSWERED',updated_at=now() WHERE id=$1`, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.RequestReconcileHangup(ctx, attempt.ID); err != nil {
		t.Fatal(err)
	}
	return store, ctx, attempt.ID, attemptUUID(attempt.ID, 2)
}

func assertPendingHangup(
	t *testing.T, store *Store, ctx context.Context, actionID, attemptID string,
) {
	t.Helper()
	var state string
	var processing, processed sql.NullTime
	var available bool
	var count int
	err := store.pool.QueryRow(ctx, `SELECT state,processing_at,processed_at,
		available_at<=clock_timestamp(),
		(SELECT count(*) FROM outbox WHERE aggregate_id=$2 AND kind='ARI_HANGUP')
		FROM outbox WHERE id=$1`, actionID, attemptID).
		Scan(&state, &processing, &processed, &available, &count)
	if err != nil || state != "PENDING" || processing.Valid || processed.Valid ||
		!available || count != 1 {
		t.Fatalf("state=%s processing=%v processed=%v available=%v count=%d err=%v",
			state, processing, processed, available, count, err)
	}
}
