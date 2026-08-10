package main

import (
	"testing"
	"time"
)

func TestCallerIDReregistrationRefreshesAuthorization(t *testing.T) {
	store, ctx := integrationStore(t, 1)
	firstAt := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Microsecond)
	first, err := store.RegisterCallerID(
		ctx, "+14155550100", "initial", "v1:operator", firstAt)
	if err != nil {
		t.Fatal(err)
	}
	renewedAt := time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)
	renewed, err := store.RegisterCallerID(
		ctx, "+14155550100", "renewed", "v1:operator", renewedAt)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.ID != first.ID || renewed.Authorization != "renewed" ||
		!renewed.AuthorizedAt.Equal(renewedAt) {
		t.Fatalf("first=%+v renewed=%+v", first, renewed)
	}
}
