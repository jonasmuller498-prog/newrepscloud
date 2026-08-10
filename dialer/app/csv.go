package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var requiredCSVHeader = []string{"phone_e164", "timezone", "consent_at", "consent_source"}

func parseRecipientCSV(r io.Reader, now time.Time) ([]ImportRow, error) {
	reader := csv.NewReader(r)
	reader.ReuseRecord = false
	reader.FieldsPerRecord = len(requiredCSVHeader)
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	for i, want := range requiredCSVHeader {
		if strings.TrimSpace(header[i]) != want {
			return nil, errors.New("CSV header must be phone_e164,timezone,consent_at,consent_source")
		}
	}
	rows := make([]ImportRow, 0)
	for line := 2; ; line++ {
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("CSV line %d: %w", line, readErr)
		}
		phone, phoneErr := normalizeE164(record[0])
		if phoneErr != nil {
			return nil, fmt.Errorf("CSV line %d: %w", line, phoneErr)
		}
		timezone := strings.TrimSpace(record[1])
		if err := validTimezone(timezone); err != nil {
			return nil, fmt.Errorf("CSV line %d: invalid timezone", line)
		}
		consentAt, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(record[2]))
		if parseErr != nil || consentAt.After(now.Add(5*time.Minute)) {
			return nil, fmt.Errorf("CSV line %d: invalid consent_at", line)
		}
		source := strings.TrimSpace(record[3])
		if source == "" || len(source) > 200 {
			return nil, fmt.Errorf("CSV line %d: invalid consent_source", line)
		}
		rows = append(rows, ImportRow{phone, timezone, source, consentAt.UTC(), line})
		if len(rows) > 100000 {
			return nil, errors.New("CSV exceeds 100000 recipients")
		}
	}
	if len(rows) == 0 {
		return nil, errors.New("CSV contains no recipients")
	}
	return rows, nil
}
