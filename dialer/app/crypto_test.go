package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestProtectionResponsibilitiesAreVersioned(t *testing.T) {
	phone := []byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef")
	field := []byte("abcdef0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	auditKey := []byte("9876543210abcdefghijklmnopqrstuvwxyzABCDEF")
	protector, err := NewProtector(phone, field, auditKey)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := protector.Encrypt("+14155552671")
	if err != nil || ciphertext[0] != cryptoVersion {
		t.Fatalf("ciphertext version=%d err=%v", ciphertext[0], err)
	}
	if hash := protector.LookupHash("+14155552671"); hash[0] != cryptoVersion {
		t.Fatalf("phone hash version=%d", hash[0])
	}
	if actor := protector.ActorID("token"); !strings.HasPrefix(actor, "v1:") {
		t.Fatalf("actor ID is not versioned: %s", actor)
	}
	body := []byte(`{"attempt_id":"safe-id"}`)
	mac := hmac.New(sha256.New, auditKey)
	_, _ = mac.Write([]byte("callback:v1:"))
	_, _ = mac.Write(body)
	signature := "v1=" + hex.EncodeToString(mac.Sum(nil))
	if !protector.VerifySignature(body, signature) ||
		protector.VerifySignature(body, strings.TrimPrefix(signature, "v1=")) {
		t.Fatal("callback signature version handling failed")
	}
}

func TestProtectorRejectsSharedKeys(t *testing.T) {
	key := []byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef")
	if _, err := NewProtector(key, key, key); err == nil {
		t.Fatal("shared protection key was accepted")
	}
}
