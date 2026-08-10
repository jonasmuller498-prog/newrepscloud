package main

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr, MetricsAddr, DatabaseURL, MediaDir, EventJournalDir string
	ARIURL, ARIApp, ARIUser, ARIPassword                          string
	ARIDialContext, OperatorToken, ApproverToken                  string
	PhoneHashKey, FieldEncryptionKey                              []byte
	AuditHMACKey                                                  []byte
	DialingEnabled                                                bool
	MaxConcurrency                                                int
	CPS                                                           float64
	WindowStart, WindowEnd                                        time.Duration
	AssetMaxDuration                                              time.Duration
	MaxBodyBytes                                                  int64
}

func LoadConfig() (Config, error) { return loadConfig(os.LookupEnv) }

func loadConfig(get func(string) (string, bool)) (Config, error) {
	c := Config{
		HTTPAddr:         value(get, "HTTP_ADDR", ":8080"),
		MetricsAddr:      value(get, "METRICS_ADDR", ":9090"),
		DatabaseURL:      value(get, "DATABASE_URL", ""),
		MediaDir:         value(get, "MEDIA_DIR", "/var/lib/dialer/media"),
		EventJournalDir:  value(get, "EVENT_JOURNAL_DIR", ""),
		ARIURL:           strings.TrimRight(value(get, "ARI_URL", ""), "/"),
		ARIApp:           value(get, "ARI_APP", ""),
		ARIUser:          value(get, "ARI_USER", ""),
		ARIPassword:      value(get, "ARI_PASSWORD", ""),
		ARIDialContext:   value(get, "ARI_DIAL_CONTEXT", ""),
		OperatorToken:    value(get, "OPERATOR_API_TOKEN", ""),
		ApproverToken:    value(get, "APPROVER_API_TOKEN", ""),
		MaxBodyBytes:     20 << 20,
		AssetMaxDuration: 10 * time.Minute,
	}
	var err error
	if c.DialingEnabled, err = boolValue(get, "DIALING_ENABLED", false); err != nil {
		return c, err
	}
	if c.MaxConcurrency, err = intValue(get, "MAX_CONCURRENCY", 20); err != nil {
		return c, err
	}
	if c.CPS, err = floatValue(get, "CPS", 0); err != nil {
		return c, err
	}
	if c.WindowStart, err = clockValue(get, "CALLING_HOURS_START", "08:00"); err != nil {
		return c, err
	}
	if c.WindowEnd, err = clockValue(get, "CALLING_HOURS_END", "21:00"); err != nil {
		return c, err
	}
	keys := []struct {
		name   string
		target *[]byte
	}{
		{"PHONE_HASH_KEY", &c.PhoneHashKey},
		{"FIELD_ENCRYPTION_KEY", &c.FieldEncryptionKey},
		{"AUDIT_HMAC_KEY", &c.AuditHMACKey},
	}
	for _, key := range keys {
		if *key.target, err = keyValue(get, key.name); err != nil {
			return c, err
		}
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	switch {
	case c.DatabaseURL == "":
		return errors.New("DATABASE_URL is required")
	case c.HTTPAddr == "" || c.MetricsAddr == "" || c.HTTPAddr == c.MetricsAddr:
		return errors.New("HTTP_ADDR and METRICS_ADDR must be distinct")
	case !highEntropy([]byte(c.OperatorToken)):
		return errors.New("OPERATOR_API_TOKEN must be at least 32 high-entropy bytes")
	case !highEntropy([]byte(c.ApproverToken)):
		return errors.New("APPROVER_API_TOKEN must be at least 32 high-entropy bytes")
	case c.OperatorToken == c.ApproverToken:
		return errors.New("operator and approver tokens must differ")
	case !highEntropy(c.PhoneHashKey):
		return errors.New("PHONE_HASH_KEY must contain at least 32 high-entropy bytes")
	case !highEntropy(c.FieldEncryptionKey):
		return errors.New("FIELD_ENCRYPTION_KEY must contain at least 32 high-entropy bytes")
	case !highEntropy(c.AuditHMACKey):
		return errors.New("AUDIT_HMAC_KEY must contain at least 32 high-entropy bytes")
	case !distinctKeys(c.PhoneHashKey, c.FieldEncryptionKey, c.AuditHMACKey):
		return errors.New("phone, encryption, and audit keys must differ")
	case c.MaxConcurrency < 1 || c.MaxConcurrency > 100:
		return errors.New("MAX_CONCURRENCY must be between 1 and 100")
	case math.IsNaN(c.CPS) || math.IsInf(c.CPS, 0) || c.CPS < 0 || c.CPS > 100:
		return errors.New("CPS must be between 0 and 100")
	case c.WindowStart >= c.WindowEnd:
		return errors.New("calling hours must be an increasing same-day window")
	}
	if c.DialingEnabled {
		if c.ARIURL == "" || c.ARIApp == "" || c.ARIUser == "" ||
			c.ARIPassword == "" || !validRouteName(c.ARIDialContext) {
			return errors.New("ARI settings are required when dialing is enabled")
		}
		if !filepath.IsAbs(c.EventJournalDir) ||
			filepath.Clean(c.EventJournalDir) == "/" {
			return errors.New("EVENT_JOURNAL_DIR must be a non-root absolute path when dialing is enabled")
		}
		u, err := url.Parse(c.ARIURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
			u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("ARI_URL must be an absolute http(s) base URL without credentials or query")
		}
	}
	return nil
}

func value(get func(string) (string, bool), key, fallback string) string {
	if v, ok := get(key); ok {
		return strings.TrimSpace(v)
	}
	return fallback
}

func boolValue(get func(string) (string, bool), key string, fallback bool) (bool, error) {
	v := value(get, key, strconv.FormatBool(fallback))
	n, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

func intValue(get func(string) (string, bool), key string, fallback int) (int, error) {
	v := value(get, key, strconv.Itoa(fallback))
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

func floatValue(get func(string) (string, bool), key string, fallback float64) (float64, error) {
	v := value(get, key, strconv.FormatFloat(fallback, 'f', -1, 64))
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

func clockValue(get func(string) (string, bool), key, fallback string) (time.Duration, error) {
	t, err := time.Parse("15:04", value(get, key, fallback))
	if err != nil {
		return 0, fmt.Errorf("%s must use HH:MM", key)
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}

func clockString(value time.Duration) string {
	return fmt.Sprintf("%02d:%02d", int(value/time.Hour), int(value%time.Hour/time.Minute))
}
