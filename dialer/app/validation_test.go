package main

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

func TestNormalizeE164(t *testing.T) {
	for _, valid := range []string{"+14155552671", "+442079460000"} {
		if got, err := normalizeE164(valid); err != nil || got != valid {
			t.Fatalf("normalize %q: %q, %v", valid, got, err)
		}
	}
	for _, invalid := range []string{"14155552671", "+0123456789", "+1 4155552671", "+１２３４５６７８"} {
		if _, err := normalizeE164(invalid); err == nil {
			t.Fatalf("accepted invalid number %q", invalid)
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
		"+14155552671,America/New_York,2026-08-01T12:00:00Z,web_form\n"
	rows, err := parseRecipientCSV(strings.NewReader(valid), now)
	if err != nil || len(rows) != 1 || rows[0].Timezone != "America/New_York" {
		t.Fatalf("valid CSV rejected: %#v, %v", rows, err)
	}
	invalid := []string{
		strings.Replace(valid, "phone_e164", "phone", 1),
		strings.Replace(valid, "+14155552671", "4155552671", 1),
		strings.Replace(valid, "America/New_York", "Local", 1),
		strings.Replace(valid, "2026-08-01T12:00:00Z", "2027-08-01T12:00:00Z", 1),
	}
	for _, input := range invalid {
		if _, err := parseRecipientCSV(strings.NewReader(input), now); err == nil {
			t.Fatal("accepted invalid CSV")
		}
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
