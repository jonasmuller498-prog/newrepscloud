package main

import (
	"testing"
	"time"
)

func TestCampaignStateTransitions(t *testing.T) {
	allowed := [][2]string{
		{"DRAFT", "VALIDATING"}, {"VALIDATING", "APPROVED"},
		{"APPROVED", "SCHEDULED"}, {"SCHEDULED", "RUNNING"},
		{"RUNNING", "PAUSED"}, {"PAUSED", "RUNNING"},
		{"RUNNING", "DRAINING"}, {"DRAINING", "CANCELLED"},
		{"RUNNING", "COMPLETED"},
	}
	for _, pair := range allowed {
		if !canTransition(pair[0], pair[1]) {
			t.Errorf("expected %s -> %s", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]string{
		{"DRAFT", "RUNNING"}, {"APPROVED", "RUNNING"},
		{"CANCELLED", "RUNNING"}, {"COMPLETED", "PAUSED"},
	} {
		if canTransition(pair[0], pair[1]) {
			t.Errorf("unexpected %s -> %s", pair[0], pair[1])
		}
	}
}

func TestSuppressionAlwaysTakesPrecedence(t *testing.T) {
	if suppressionWins(true, true, true) {
		t.Fatal("suppressed recipient was eligible")
	}
	if suppressionWins(false, false, true) || suppressionWins(false, true, false) {
		t.Fatal("recipient without all controls was eligible")
	}
	if !suppressionWins(false, true, true) {
		t.Fatal("fully eligible recipient was rejected")
	}
}

func TestRetryPolicyIsExplicit(t *testing.T) {
	for _, outcome := range []string{"busy", "no_answer", "temporary"} {
		if !retryableOutcome(outcome) {
			t.Errorf("%s should retry", outcome)
		}
	}
	for _, outcome := range []string{
		"completed", "answered", "message_started", "ambiguous",
		"invalid", "forbidden", "opt_out",
	} {
		if retryableOutcome(outcome) {
			t.Errorf("%s must be terminal", outcome)
		}
	}
}

func TestGCRALimiter(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	allowed, next := gcraNext(now, now.Add(-time.Second), 4)
	if !allowed || next.Sub(now) != 250*time.Millisecond {
		t.Fatalf("first token: allowed=%v next=%v", allowed, next)
	}
	allowed, blockedUntil := gcraNext(now.Add(100*time.Millisecond), next, 4)
	if allowed || !blockedUntil.Equal(next) {
		t.Fatalf("early token allowed=%v until=%v", allowed, blockedUntil)
	}
	if allowed, _ := gcraNext(now, now, 0); allowed {
		t.Fatal("CPS zero allowed a token")
	}
}
