package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeE164(t *testing.T) {
	tests := []struct {
		name  string
		phone string
		valid bool
	}{
		{"US geographic", "+14155552671", true},
		{"US toll-free", "+18335167239", true},
		{"UK", "+442079460000", false},
		{"Canadian NANP", "+14165551234", false},
		{"Caribbean NANP", "+18765551234", false},
		{"missing plus", "14155552671", false},
		{"leading space", " +14155552671", false},
		{"embedded space", "+1 4155552671", false},
		{"Unicode digits", "+１4155552671", false},
		{"too short", "+1415555", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeE164(test.phone)
			if test.valid && (err != nil || got != test.phone) {
				t.Fatalf("normalize %q: %q, %v", test.phone, got, err)
			}
			if !test.valid && !errors.Is(err, errInvalidUSPhone) {
				t.Fatalf("normalize %q error = %v", test.phone, err)
			}
		})
	}
	for _, valid := range []string{"+14155552671", "+18335167239"} {
		if got, err := normalizeE164(valid); err != nil || got != valid {
			t.Fatalf("normalize %q: %q, %v", valid, got, err)
		}
	}
	if got := maskPhone("+14155552671"); !strings.HasSuffix(got, "2671") ||
		strings.Contains(got, "415555") {
		t.Fatalf("unsafe mask: %q", got)
	}
}

func TestRecipientCSVValidation(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	valid := "phone_e164,timezone,consent_at,consent_source\n" +
		"+14155552671,America/New_York,2026-08-01T12:00:00Z,web_form\n" +
		"+18335167239,America/Chicago,2026-08-02T12:00:00Z,web_form\n"
	rows, err := parseRecipientCSV(strings.NewReader(valid), now)
	if err != nil || len(rows) != 2 || rows[0].Timezone != "America/New_York" {
		t.Fatalf("valid CSV rejected: %#v, %v", rows, err)
	}
	invalid := []string{
		strings.Replace(valid, "phone_e164", "phone", 1),
		strings.Replace(valid, "America/New_York", "Local", 1),
		strings.Replace(valid, "America/New_York", "UTC", 1),
		strings.Replace(valid, "America/New_York", "Asia/Tokyo", 1),
		strings.Replace(valid, "2026-08-01T12:00:00Z", "2027-08-01T12:00:00Z", 1),
	}
	for _, input := range invalid {
		if _, err := parseRecipientCSV(strings.NewReader(input), now); err == nil {
			t.Fatal("accepted invalid CSV")
		}
	}
	for _, phone := range []string{
		"+442079460000", "+14165551234", "+18765551234",
		"4155552671", " +14155552671", "+１4155552671",
	} {
		input := strings.Replace(valid, "+14155552671", phone, 1)
		if _, err = parseRecipientCSV(strings.NewReader(input), now); !errors.Is(err, errInvalidUSPhone) {
			t.Fatalf("CSV phone %q error = %v", phone, err)
		}
	}
}

func TestCallerIDUsesUSPhoneValidation(t *testing.T) {
	store := &Store{}
	_, err := store.RegisterCallerID(context.Background(), "+14165551234",
		"authorization", "test", time.Now().Add(-time.Hour))
	if !errors.Is(err, errInvalidUSPhone) {
		t.Fatalf("caller ID error = %v", err)
	}
}

func TestWAVValidation(t *testing.T) {
	data := testWAV(16000, 1, 8000, 16)
	info, err := validateWAV(data, 2*time.Second)
	if err != nil || info.Duration != time.Second {
		t.Fatalf("valid WAV rejected: %+v, %v", info, err)
	}
	for _, bad := range [][]byte{
		[]byte("not a wav"),
		testWAV(16000, 2, 8000, 16),
		testWAV(16000, 1, 44100, 16),
		testWAV(16000, 1, 8000, 8),
	} {
		if _, err := validateWAV(bad, 2*time.Second); err == nil {
			t.Fatal("accepted invalid WAV")
		}
	}
	if _, err := validateWAV(testWAV(48000, 1, 8000, 16), 2*time.Second); err == nil {
		t.Fatal("accepted over-duration WAV")
	}
}

func testWAV(dataSize int, channels, rate, bits uint16) []byte {
	buf := bytes.NewBuffer(make([]byte, 0, dataSize+44))
	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(dataSize+36))
	buf.WriteString("WAVEfmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, channels)
	_ = binary.Write(buf, binary.LittleEndian, uint32(rate))
	byteRate := uint32(rate) * uint32(channels) * uint32(bits) / 8
	_ = binary.Write(buf, binary.LittleEndian, byteRate)
	_ = binary.Write(buf, binary.LittleEndian, channels*bits/8)
	_ = binary.Write(buf, binary.LittleEndian, bits)
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(dataSize))
	buf.Write(make([]byte, dataSize))
	return buf.Bytes()
}
