package main

import (
	"errors"
	"time"
)

var (
	errInvalidTimezone = errors.New("invalid US IANA timezone")
	errNotFound        = errors.New("not found")
	errConflict        = errors.New("state conflict")
	errForbidden       = errors.New("forbidden")
	errSafetyBlocked   = errors.New("safety requirements are not satisfied")
)

type Campaign struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	State         string     `json:"state"`
	MessageAsset  *string    `json:"message_asset_id,omitempty"`
	CallerID      *string    `json:"caller_id_id,omitempty"`
	DNCAttestedAt *time.Time `json:"dnc_attested_at,omitempty"`
	ScheduledAt   *time.Time `json:"scheduled_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

type SafetyBlock struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Count   int64  `json:"count,omitempty"`
}

type Attempt struct {
	ID                  string     `json:"id"`
	CampaignRecipientID string     `json:"campaign_recipient_id"`
	CampaignID          string     `json:"campaign_id"`
	MaskedPhone         string     `json:"phone"`
	AttemptNo           int        `json:"attempt_no"`
	State               string     `json:"state"`
	Outcome             *string    `json:"outcome,omitempty"`
	ChannelID           string     `json:"channel_id"`
	CreatedAt           time.Time  `json:"created_at"`
	EndedAt             *time.Time `json:"ended_at,omitempty"`
}

type CallEvent struct {
	ID          int64     `json:"id"`
	AttemptID   string    `json:"attempt_id"`
	Type        string    `json:"type"`
	MaskedPhone string    `json:"phone"`
	CreatedAt   time.Time `json:"created_at"`
}

type ImportRow struct {
	Phone, Timezone, ConsentSource string
	ConsentAt                      time.Time
	Line                           int
}

func canTransition(from, to string) bool {
	allowed := map[string]map[string]bool{
		"DRAFT":      {"VALIDATING": true, "DRAINING": true},
		"VALIDATING": {"APPROVED": true, "DRAFT": true, "DRAINING": true},
		"APPROVED":   {"SCHEDULED": true, "DRAINING": true},
		"SCHEDULED":  {"RUNNING": true, "DRAINING": true},
		"RUNNING":    {"PAUSED": true, "DRAINING": true, "COMPLETED": true},
		"PAUSED":     {"RUNNING": true, "DRAINING": true},
		"DRAINING":   {"CANCELLED": true, "COMPLETED": true},
	}
	return allowed[from][to]
}

func retryableOutcome(outcome string) bool {
	return outcome == "busy" || outcome == "no_answer" || outcome == "temporary"
}
