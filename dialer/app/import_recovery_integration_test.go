package main

import (
	"testing"
	"time"
)

func TestCorruptImportPayloadFailsWithoutInfiniteRetry(t *testing.T) {
	store, ctx := integrationStore(t, 1)
	campaign, err := store.CreateCampaign(
		ctx, CampaignInput{Name: "poison-import"}, "v1:operator")
	if err != nil {
		t.Fatal(err)
	}
	rows := []ImportRow{{
		Phone: "+14155552671", Timezone: "America/New_York",
		ConsentSource: "form", ConsentAt: time.Now().Add(-time.Hour), Line: 2,
	}}
	job, err := store.CreateImportJob(
		ctx, campaign.ID, "v1:operator", "poison-request", rows)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimImportJob(ctx)
	if err != nil || claimed != job.ID {
		t.Fatalf("claimed=%s err=%v", claimed, err)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE import_jobs SET payload_cipher=$2
		WHERE id=$1`, job.ID, []byte("corrupt")); err != nil {
		t.Fatal(err)
	}
	if err = store.ProcessImportJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	failed, err := store.GetImportJob(ctx, job.ID)
	if err != nil || failed.State != "FAILED" || failed.ErrorCode == nil ||
		*failed.ErrorCode != "payload_decrypt" {
		t.Fatalf("job=%+v err=%v", failed, err)
	}
}
