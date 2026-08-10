package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentSlotAllocationAndIdempotency(t *testing.T) {
	store, ctx := integrationStore(t, 2)
	campaignID, recipientIDs := seedQueue(t, ctx, store)
	start := make(chan struct{})
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			item, allocateErr := store.AllocateAttempt(ctx)
			if allocateErr != nil {
				t.Errorf("allocate: %v", allocateErr)
			} else if item != nil {
				accepted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := accepted.Load(); got != 2 {
		t.Fatalf("allocated %d attempts with two fixed slots", got)
	}
	var attempts, occupied, maxPerRecipient int
	err := store.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM call_attempts),
		(SELECT count(*) FROM dialer_slots WHERE attempt_id IS NOT NULL),
		(SELECT COALESCE(max(n),0) FROM (SELECT count(*) n FROM call_attempts
		  GROUP BY recipient_id) grouped)`).
		Scan(&attempts, &occupied, &maxPerRecipient)
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || occupied != 2 || maxPerRecipient != 1 {
		t.Fatalf("attempts=%d occupied=%d per_recipient=%d", attempts, occupied, maxPerRecipient)
	}
	var attemptID, campaignRecipientID string
	var attemptNo int
	err = store.pool.QueryRow(ctx, `SELECT id,campaign_recipient_id,attempt_no
		FROM call_attempts`).Scan(&attemptID, &campaignRecipientID, &attemptNo)
	if err != nil || attemptID != attemptUUID(campaignRecipientID, attemptNo) {
		t.Fatalf("non-deterministic attempt id %s: %v", attemptID, err)
	}
	if campaignID == "" || len(recipientIDs) != 3 {
		t.Fatal("invalid queue fixture")
	}
}

func seedQueue(t *testing.T, ctx context.Context, store *Store) (string, []string) {
	t.Helper()
	media := testWAV(16000, 1, 8000, 16)
	sum := sha256.Sum256(media)
	storage := hex.EncodeToString(sum[:]) + ".wav"
	if err := writeMediaFile(store.config.MediaDir, storage, media); err != nil {
		t.Fatal(err)
	}
	assetID, _ := newUUID()
	callerID, _ := newUUID()
	campaignID, _ := newUUID()
	approvalID, _ := newUUID()
	_, err := store.pool.Exec(ctx, `INSERT INTO message_assets
		(id,sha256,storage_name,byte_size,duration_ms,format,created_by)
		VALUES($1,$2,$3,$4,1000,'pcm_s16le_mono_8000','test')`,
		assetID, sum[:], storage, len(media))
	if err == nil {
		_, err = store.pool.Exec(ctx, `INSERT INTO caller_ids
			(id,phone_cipher,phone_hash,authorization_reference,authorized_at,created_by)
			VALUES($1,$2,$3,'test',now(),'test')`, callerID, []byte{2}, []byte{3})
	}
	if err == nil {
		_, err = store.pool.Exec(ctx, `INSERT INTO campaigns
			(id,name,state,message_asset_id,caller_id_id,dnc_attested_at,
			window_start,window_end,created_by) VALUES($1,'test','RUNNING',$2,$3,now(),
			'00:00','23:59:59','test')`, campaignID, assetID, callerID)
	}
	if err == nil {
		_, err = store.pool.Exec(ctx, `INSERT INTO campaign_approvals
			(id,campaign_id,message_asset_id,caller_id_id,approver)
			VALUES($1,$2,$3,$4,'test')`, approvalID, campaignID, assetID, callerID)
	}
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	var firstRecipient, firstConsent string
	for i := range 2 {
		recipientID, _ := newUUID()
		consentID, _ := newUUID()
		crID, _ := newUUID()
		_, err = store.pool.Exec(ctx, `INSERT INTO recipients
			(id,phone_cipher,phone_hash,timezone) VALUES($1,$2,$3,'UTC')`,
			recipientID, []byte{byte(10 + i)}, []byte{byte(20 + i)})
		if err == nil {
			_, err = store.pool.Exec(ctx, `INSERT INTO consent_evidence
				(id,recipient_id,consent_at,source,imported_by)
				VALUES($1,$2,now()-interval '1 day','test','test')`, consentID, recipientID)
		}
		if err == nil {
			_, err = store.pool.Exec(ctx, `INSERT INTO campaign_recipients
				(id,campaign_id,recipient_id,consent_evidence_id,timezone)
				VALUES($1,$2,$3,$4,'UTC')`, crID, campaignID, recipientID, consentID)
		}
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			firstRecipient, firstConsent = recipientID, consentID
		}
		ids = append(ids, crID)
	}
	secondCampaign, _ := newUUID()
	secondApproval, _ := newUUID()
	sharedCR, _ := newUUID()
	_, err = store.pool.Exec(ctx, `INSERT INTO campaigns
		(id,name,state,message_asset_id,caller_id_id,dnc_attested_at,
		window_start,window_end,created_by) VALUES($1,'shared','RUNNING',$2,$3,now(),
		'00:00','23:59:59','test')`, secondCampaign, assetID, callerID)
	if err == nil {
		_, err = store.pool.Exec(ctx, `INSERT INTO campaign_approvals
			(id,campaign_id,message_asset_id,caller_id_id,approver)
			VALUES($1,$2,$3,$4,'test')`,
			secondApproval, secondCampaign, assetID, callerID)
	}
	if err == nil {
		_, err = store.pool.Exec(ctx, `INSERT INTO campaign_recipients
			(id,campaign_id,recipient_id,consent_evidence_id,timezone)
			VALUES($1,$2,$3,$4,'UTC')`, sharedCR, secondCampaign, firstRecipient, firstConsent)
	}
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, sharedCR)
	return campaignID, ids
}
