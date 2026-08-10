package main

import (
	"errors"
	"strings"
)

var errUnsafeARIEvent = errors.New("ARI event contains unsafe journal fields")

type ARIJournalRecord struct {
	Version      int    `json:"version"`
	Key          string `json:"key"`
	Type         string `json:"type"`
	ChannelID    string `json:"channel_id"`
	ChannelState string `json:"channel_state,omitempty"`
	PlaybackID   string `json:"playback_id,omitempty"`
	Digit        string `json:"digit,omitempty"`
	Cause        int    `json:"cause,omitempty"`
}

func newARIJournalRecord(event ARIEvent, key string) (ARIJournalRecord, error) {
	record := ARIJournalRecord{
		Version: 1, Key: key, Type: event.Type, ChannelID: event.ChannelID(),
	}
	switch event.Type {
	case "StasisStart", "ChannelStateChange":
		record.ChannelState = journalChannelState(event.Channel.State)
	case "PlaybackStarted", "PlaybackFinished":
		record.PlaybackID = event.Playback.ID
	case "ChannelDestroyed":
		record.Cause = event.Cause
	case "ChannelDtmfReceived":
		record.Digit = event.Digit
	}
	if err := record.Validate(); err != nil {
		return ARIJournalRecord{}, err
	}
	return record, nil
}

func (r ARIJournalRecord) Validate() error {
	if r.Version != 1 || !validEventKey(r.Key) || !validJournalEventType(r.Type) ||
		!validGeneratedID(r.ChannelID, "dialer-", 32) ||
		r.Cause < 0 || r.Cause > 255 {
		return errUnsafeARIEvent
	}
	if r.ChannelState != "" && r.ChannelState != "up" &&
		r.ChannelState != "ringing" && r.ChannelState != "other" {
		return errUnsafeARIEvent
	}
	hasState := r.Type == "StasisStart" || r.Type == "ChannelStateChange"
	if !hasState && r.ChannelState != "" {
		return errUnsafeARIEvent
	}
	playback := r.Type == "PlaybackStarted" || r.Type == "PlaybackFinished"
	if playback != validGeneratedID(r.PlaybackID, "play-", 32) {
		return errUnsafeARIEvent
	}
	if r.Type != "ChannelDestroyed" && r.Cause != 0 {
		return errUnsafeARIEvent
	}
	if r.Type == "ChannelDtmfReceived" {
		if r.Digit != "9" {
			return errUnsafeARIEvent
		}
	} else if r.Digit != "" {
		return errUnsafeARIEvent
	}
	return nil
}

func (r ARIJournalRecord) Event() ARIEvent {
	event := ARIEvent{Type: r.Type, Digit: r.Digit, Cause: r.Cause}
	event.Channel.ID = r.ChannelID
	event.Playback.ID = r.PlaybackID
	switch r.ChannelState {
	case "up":
		event.Channel.State = "Up"
	case "ringing":
		event.Channel.State = "Ringing"
	}
	return event
}

func (r ARIJournalRecord) IsOptOut() bool {
	return r.Type == "ChannelDtmfReceived" && r.Digit == "9"
}

func validJournalEventType(value string) bool {
	switch value {
	case "StasisStart", "ChannelStateChange", "PlaybackStarted",
		"PlaybackFinished", "StasisEnd", "ChannelDestroyed",
		"ChannelDtmfReceived":
		return true
	}
	return false
}

func validEventKey(value string) bool {
	return len(value) == 64 && validLowerHex(value)
}

func validGeneratedID(value, prefix string, hexLength int) bool {
	return strings.HasPrefix(value, prefix) &&
		len(value) == len(prefix)+hexLength &&
		validLowerHex(strings.TrimPrefix(value, prefix))
}

func validLowerHex(value string) bool {
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return value != ""
}

func journalChannelState(value string) string {
	if strings.EqualFold(value, "up") {
		return "up"
	}
	if strings.Contains(strings.ToLower(value), "ring") {
		return "ringing"
	}
	if value != "" {
		return "other"
	}
	return ""
}
