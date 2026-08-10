package main

import (
	"testing"
	"time"
)

func TestCallingWindowRecipientLocal(t *testing.T) {
	start, end := 8*time.Hour, 21*time.Hour
	tests := []struct {
		at   string
		want bool
	}{
		{"2026-01-15T12:59:00Z", false},
		{"2026-01-15T13:00:00Z", true},
		{"2026-01-16T01:59:59Z", true},
		{"2026-01-16T02:00:00Z", false},
	}
	for _, test := range tests {
		at, _ := time.Parse(time.RFC3339, test.at)
		got, err := withinCallingWindow(at, "America/New_York", start, end)
		if err != nil || got != test.want {
			t.Errorf("%s: got %v, %v; want %v", test.at, got, err, test.want)
		}
	}
}

func TestCallingWindowDSTTransitions(t *testing.T) {
	springBefore, _ := time.Parse(time.RFC3339, "2026-03-08T06:30:00Z")
	springAfter, _ := time.Parse(time.RFC3339, "2026-03-08T07:30:00Z")
	if inside, _ := withinCallingWindow(springBefore, "America/New_York", 3*time.Hour, 4*time.Hour); inside {
		t.Fatal("01:30 EST should be outside 03:00-04:00")
	}
	if inside, _ := withinCallingWindow(springAfter, "America/New_York", 3*time.Hour, 4*time.Hour); !inside {
		t.Fatal("03:30 EDT should be inside 03:00-04:00")
	}
	for _, value := range []string{"2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z"} {
		at, _ := time.Parse(time.RFC3339, value)
		if inside, _ := withinCallingWindow(at, "America/New_York", time.Hour, 2*time.Hour); !inside {
			t.Fatalf("%s should represent a local 01:30 inside the window", value)
		}
	}
}

func TestNextWindowPreservesWallClockAcrossDST(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-03-07T22:00:00Z")
	next, err := nextCallingWindow(now, "America/New_York", 8*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	local, _ := time.LoadLocation("America/New_York")
	if got := next.In(local).Format("15:04 MST"); got != "08:00 EDT" {
		t.Fatalf("next window = %s", got)
	}
}
