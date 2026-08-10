package main

import (
	"strings"
	"testing"
)

func configLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func validConfigValues() map[string]string {
	return map[string]string{
		"DATABASE_URL":       "postgres://dialer:test@localhost/dialer",
		"OPERATOR_API_TOKEN": "operator-token",
		"APPROVER_API_TOKEN": "approver-token",
		"HMAC_KEY":           strings.Repeat("k", 32),
	}
}

func TestConfigFailClosedDefaults(t *testing.T) {
	config, err := loadConfig(configLookup(validConfigValues()))
	if err != nil {
		t.Fatal(err)
	}
	if config.DialingEnabled || config.CPS != 0 {
		t.Fatalf("unsafe defaults: enabled=%v cps=%v", config.DialingEnabled, config.CPS)
	}
	if config.MaxConcurrency != 20 ||
		config.WindowStart.String() != "8h0m0s" || config.WindowEnd.String() != "21h0m0s" {
		t.Fatalf("unexpected defaults: %+v", config)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name, key, value string
	}{
		{"concurrency high", "MAX_CONCURRENCY", "101"},
		{"concurrency zero", "MAX_CONCURRENCY", "0"},
		{"negative cps", "CPS", "-1"},
		{"invalid hours", "CALLING_HOURS_START", "22:00"},
		{"short key", "HMAC_KEY", "short"},
		{"same role token", "APPROVER_API_TOKEN", "operator-token"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := validConfigValues()
			values[test.key] = test.value
			if _, err := loadConfig(configLookup(values)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestEnabledConfigRequiresARI(t *testing.T) {
	values := validConfigValues()
	values["DIALING_ENABLED"] = "true"
	values["CPS"] = "1"
	if _, err := loadConfig(configLookup(values)); err == nil {
		t.Fatal("expected missing ARI settings to fail")
	}
	values["ARI_URL"], values["ARI_APP"] = "https://ari.example", "dialer"
	values["ARI_USER"], values["ARI_PASSWORD"] = "user", "password"
	if _, err := loadConfig(configLookup(values)); err != nil {
		t.Fatal(err)
	}
}
