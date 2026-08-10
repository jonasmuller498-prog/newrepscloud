package main

import (
	"context"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConcurrentSlotAllocationAndIdempotency(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	suffix, _ := newUUID()
	schema := "dialer_test_" + strings.ReplaceAll(suffix, "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	config := Config{
		DatabaseURL: parsed.String(), MediaDir: t.TempDir(), MaxConcurrency: 1,
		CPS: 100, WindowStart: 8 * time.Hour, WindowEnd: 21 * time.Hour,
		HMACKey: []byte(strings.Repeat("k", 32)),
	}
	protector, _ := NewProtector(config.HMACKey)
	store, err := openStore(ctx, config, protector)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		_, _ = admin.Exec(context.Background(),
			"DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	})
	if err = runMigrations(ctx, store.pool); err != nil {
		t.Fatal(err)
	}
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
	if got := accepted.Load(); got != 1 {
		t.Fatalf("allocated %d attempts with one fixed slot", got)
	}
	var attempts, occupied, maxPerRecipient int
	err = store.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM call_attempts),
		(SELECT count(*) FROM dialer_slots WHERE attempt_id IS NOT NULL),
		(SELECT COALESCE(max(n),0) FROM (SELECT count(*) n FROM call_attempts
		  GROUP BY campaign_recipient_id) grouped)`).
		Scan(&attempts, &occupied, &maxPerRecipient)
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || occupied != 1 || maxPerRecipient != 1 {
		t.Fatalf("attempts=%d occupied=%d per_recipient=%d", attempts, occupied, maxPerRecipient)
	}
	var attemptID, campaignRecipientID string
	var attemptNo int
	err = store.pool.QueryRow(ctx, `SELECT id,campaign_recipient_id,attempt_no
		FROM call_attempts`).Scan(&attemptID, &campaignRecipientID, &attemptNo)
	if err != nil || attemptID != attemptUUID(campaignRecipientID, attemptNo) {
		t.Fatalf("non-deterministic attempt id %s: %v", attemptID, err)
	}
	if campaignID == "" || len(recipientIDs) != 2 {
		t.Fatal("invalid queue fixture")
	}
}

func seedQueue(t *testing.T, ctx context.Context, store *Store) (string, []string) {
	t.Helper()
	assetID, _ := newUUID()
	callerID, _ := newUUID()
	campaignID, _ := newUUID()
	approvalID, _ := newUUID()
	_, err := store.pool.Exec(ctx, `INSERT INTO message_assets
		(id,sha256,storage_name,byte_size,duration_ms,format,created_by)
		VALUES($1,$2,'asset.wav',16000,1000,'pcm_s16le_mono_8000','test')`,
		assetID, []byte{1})
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
		ids = append(ids, crID)
	}
	return campaignID, ids
}
