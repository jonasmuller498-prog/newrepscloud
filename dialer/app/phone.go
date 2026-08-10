package main

import (
	"errors"
	"strings"
	"time"
	"unicode"
)

func normalizeE164(input string) (string, error) {
	value := strings.TrimSpace(input)
	if len(value) < 9 || len(value) > 16 || value[0] != '+' || value[1] == '0' {
		return "", errors.New("phone_e164 must be + followed by 8 to 15 digits")
	}
	for _, r := range value[1:] {
		if !unicode.IsDigit(r) || r > unicode.MaxASCII {
			return "", errors.New("phone_e164 contains non-ASCII digits")
		}
	}
	return value, nil
}

func maskPhone(phone string) string {
	if len(phone) < 5 {
		return "****"
	}
	return strings.Repeat("*", len(phone)-4) + phone[len(phone)-4:]
}

func validTimezone(name string) error {
	if name == "" || name == "Local" {
		return errors.New("an explicit IANA timezone is required")
	}
	_, err := time.LoadLocation(name)
	if err != nil {
		return errors.New("invalid IANA timezone")
	}
	return nil
}
