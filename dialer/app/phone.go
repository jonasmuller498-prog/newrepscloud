package main

import (
	"errors"
	"strings"
	"time"

	"github.com/nyaruka/phonenumbers"
)

var errInvalidUSPhone = errors.New(
	"phone_e164 must be a valid US number in canonical E.164 format (+1XXXXXXXXXX)",
)

func normalizeE164(input string) (string, error) {
	if len(input) < 2 || input[0] != '+' {
		return "", errInvalidUSPhone
	}
	for i := 1; i < len(input); i++ {
		if input[i] < '0' || input[i] > '9' {
			return "", errInvalidUSPhone
		}
	}
	number, err := phonenumbers.Parse(input, "US")
	if err != nil || number.GetCountryCode() != 1 ||
		!phonenumbers.IsValidNumberForRegion(number, "US") {
		return "", errInvalidUSPhone
	}
	normalized := phonenumbers.Format(number, phonenumbers.E164)
	if normalized != input {
		return "", errInvalidUSPhone
	}
	return normalized, nil
}

func maskPhone(phone string) string {
	if len(phone) < 5 {
		return "****"
	}
	return strings.Repeat("*", len(phone)-4) + phone[len(phone)-4:]
}

func validTimezone(name string) error {
	switch name {
	case "America/Adak", "America/Anchorage", "America/Boise",
		"America/Chicago", "America/Denver", "America/Detroit",
		"America/Indiana/Indianapolis", "America/Indiana/Knox",
		"America/Indiana/Marengo", "America/Indiana/Petersburg",
		"America/Indiana/Tell_City", "America/Indiana/Vevay",
		"America/Indiana/Vincennes", "America/Indiana/Winamac",
		"America/Juneau", "America/Kentucky/Louisville",
		"America/Kentucky/Monticello", "America/Los_Angeles",
		"America/Menominee", "America/Metlakatla", "America/New_York",
		"America/Nome", "America/North_Dakota/Beulah",
		"America/North_Dakota/Center", "America/North_Dakota/New_Salem",
		"America/Phoenix", "America/Sitka", "America/Yakutat",
		"Pacific/Honolulu":
	default:
		return errInvalidTimezone
	}
	_, err := time.LoadLocation(name)
	if err != nil {
		return errInvalidTimezone
	}
	return nil
}
