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
		"DATABASE_URL":         "postgres://dialer:test@localhost/dialer",
		"OPERATOR_API_TOKEN":   "operator-0123456789-ABCDEFGHIJKLMNOPQRSTUVWXYZ",
		"APPROVER_API_TOKEN":   "approver-9876543210-zyxwvutsrqponmlkjihgfedcba",
		"PHONE_HASH_KEY":       "phone-0123456789-ABCDEFGHIJKLMNOPQRSTUVWXYZ",
		"FIELD_ENCRYPTION_KEY": "field-9876543210-ABCDEFGHIJKLMNOPQRSTUVWXYZ",
		"AUDIT_HMAC_KEY":       "audit-ABCDEF0123456789-ghijklmnopqrstuvwxyz",
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
		{"not a number cps", "CPS", "NaN"},
		{"infinite cps", "CPS", "+Inf"},
		{"invalid hours", "CALLING_HOURS_START", "22:00"},
		{"short key", "PHONE_HASH_KEY", "short"},
		{"low entropy key", "AUDIT_HMAC_KEY", strings.Repeat("k", 64)},
		{"short operator token", "OPERATOR_API_TOKEN", "operator-token"},
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
	values["ARI_ENDPOINT"] = "carrier"
	if _, err := loadConfig(configLookup(values)); err != nil {
		t.Fatal(err)
	}
}

func TestLegacySecretAliasesAreRejected(t *testing.T) {
	values := validConfigValues()
	delete(values, "PHONE_HASH_KEY")
	values["HMAC_KEY"] = "legacy-0123456789-ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	if _, err := loadConfig(configLookup(values)); err == nil {
		t.Fatal("legacy shared key unexpectedly accepted")
	}
}

func TestRoleTokensMustDiffer(t *testing.T) {
	values := validConfigValues()
	values["APPROVER_API_TOKEN"] = values["OPERATOR_API_TOKEN"]
	if _, err := loadConfig(configLookup(values)); err == nil {
		t.Fatal("identical role tokens accepted")
	}
}
