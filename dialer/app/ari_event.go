package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
)

type ARIEvent struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Digit     string `json:"digit"`
	Cause     int    `json:"cause"`
	CauseText string `json:"cause_txt"`
	Channel   struct {
		ID    string `json:"id"`
		State string `json:"state"`
	} `json:"channel"`
	Playback struct {
		ID        string `json:"id"`
		TargetURI string `json:"target_uri"`
	} `json:"playback"`
}

func parseARIEvent(raw []byte) (ARIEvent, error) {
	var event ARIEvent
	err := json.Unmarshal(raw, &event)
	return event, err
}

func (e ARIEvent) ChannelID() string {
	if e.Channel.ID != "" {
		return e.Channel.ID
	}
	return strings.TrimPrefix(e.Playback.TargetURI, "channel:")
}

func (e ARIEvent) Key(raw []byte) string {
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func (e ARIEvent) StateChanging() bool {
	if e.Type == "ChannelDtmfReceived" {
		return e.Digit == "9"
	}
	return validJournalEventType(e.Type)
}

func destroyedOutcome(event ARIEvent, attemptState string) string {
	if attemptState == "ANSWERED" || attemptState == "MESSAGE_STARTED" ||
		attemptState == "TERMINATING" || attemptState == "UNCERTAIN" {
		return "ambiguous"
	}
	switch event.Cause {
	case 17:
		return "busy"
	case 18, 19:
		return "no_answer"
	case 34, 38, 41, 42, 44:
		return "temporary"
	case 21:
		return "forbidden"
	case 1, 3, 28:
		return "invalid"
	default:
		return "ambiguous"
	}
}

func (e ARIEvent) SafeJSON() []byte {
	value := struct {
		Type, ChannelID, ChannelState, PlaybackID, Digit string
		Cause                                            int
	}{
		Type: e.Type, ChannelID: e.ChannelID(), ChannelState: e.Channel.State,
		PlaybackID: e.Playback.ID, Digit: e.Digit, Cause: e.Cause,
	}
	data, _ := json.Marshal(value)
	return data
}

func eventState(event ARIEvent) string {
	switch event.Type {
	case "StasisStart":
		if strings.EqualFold(event.Channel.State, "Up") {
			return "ANSWERED"
		}
		return "RINGING"
	case "ChannelStateChange":
		if strings.EqualFold(event.Channel.State, "Up") {
			return "ANSWERED"
		}
		if strings.Contains(strings.ToLower(event.Channel.State), "ring") {
			return "RINGING"
		}
	case "PlaybackStarted":
		return "MESSAGE_STARTED"
	}
	return ""
}

func causeLabel(event ARIEvent) string {
	return strconv.Itoa(event.Cause) + ":" + strings.ToLower(event.CauseText)
}
