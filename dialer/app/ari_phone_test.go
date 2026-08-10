package main

import (
	"context"
	"errors"
	"testing"
)

func TestARIOriginateRejectsNonUSRecipientAndCallerID(t *testing.T) {
	tests := []struct {
		name     string
		phone    string
		callerID string
	}{
		{"UK recipient", "+442079460000", "+14155550100"},
		{"Canadian caller ID", "+14155552671", "+14165551234"},
		{"Caribbean caller ID", "+14155552671", "+18765551234"},
		{"noncanonical caller ID", "+14155552671", " +14155550100"},
	}
	client := NewARIClient(ariTestConfig("http://127.0.0.1"))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := ariTestCommand()
			cmd.Phone, cmd.CallerID = test.phone, test.callerID
			result, err := client.Originate(context.Background(), cmd)
			if result.Outcome != "invalid" || !errors.Is(err, errInvalidUSPhone) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}
