package main

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func integrationStore(t *testing.T, concurrency int) (*Store, context.Context) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	suffix, _ := newUUID()
	schema := "dialer_test_" + strings.ReplaceAll(suffix, "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		cancel()
		admin.Close()
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
		DatabaseURL: parsed.String(), MediaDir: t.TempDir(), MaxConcurrency: concurrency,
		CPS: 100, WindowStart: 0, WindowEnd: 24*time.Hour - time.Second,
		PhoneHashKey:       []byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef"),
		FieldEncryptionKey: []byte("abcdef0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"),
		AuditHMACKey:       []byte("9876543210abcdefghijklmnopqrstuvwxyzABCDEF"),
		MaxBodyBytes:       20 << 20, AssetMaxDuration: 10 * time.Minute,
	}
	protector, err := NewProtector(
		config.PhoneHashKey, config.FieldEncryptionKey, config.AuditHMACKey)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openStore(ctx, config, protector)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		_, _ = admin.Exec(context.Background(),
			"DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
		cancel()
	})
	if err = runMigrations(ctx, store.pool); err != nil {
		t.Fatal(err)
	}
	return store, ctx
}
