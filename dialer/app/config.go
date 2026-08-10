package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr, DatabaseURL, MediaDir                   string
	ARIURL, ARIApp, ARIUser, ARIPassword          string
	ARIEndpointTemplate, ARIContext, ARIExtension string
	OperatorToken, ApproverToken                  string
	HMACKey                                       []byte
	DialingEnabled                                bool
	MaxConcurrency                                int
	CPS                                           float64
	WindowStart, WindowEnd                        time.Duration
	AssetMaxDuration                              time.Duration
	MaxBodyBytes                                  int64
}

func LoadConfig() (Config, error) { return loadConfig(os.LookupEnv) }

func loadConfig(get func(string) (string, bool)) (Config, error) {
	c := Config{
		Addr:                value(get, "HTTP_ADDR", ":8080"),
		DatabaseURL:         value(get, "DATABASE_URL", ""),
		MediaDir:            value(get, "MEDIA_DIR", "/var/lib/dialer/media"),
		ARIURL:              strings.TrimRight(value(get, "ARI_URL", ""), "/"),
		ARIApp:              value(get, "ARI_APP", ""),
		ARIUser:             value(get, "ARI_USER", ""),
		ARIPassword:         value(get, "ARI_PASSWORD", ""),
		ARIEndpointTemplate: value(get, "ARI_ENDPOINT_TEMPLATE", "PJSIP/%s@outbound"),
		ARIContext:          value(get, "ARI_CONTEXT", "outbound-compliance"),
		ARIExtension:        value(get, "ARI_EXTENSION", "s"),
		OperatorToken:       value(get, "OPERATOR_API_TOKEN", ""),
		ApproverToken:       value(get, "APPROVER_API_TOKEN", ""),
		MaxBodyBytes:        20 << 20,
		AssetMaxDuration:    10 * time.Minute,
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
	key := value(get, "HMAC_KEY", "")
	if decoded, decErr := base64.StdEncoding.DecodeString(key); decErr == nil && len(decoded) >= 32 {
		c.HMACKey = decoded
	} else {
		c.HMACKey = []byte(key)
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	switch {
	case c.DatabaseURL == "":
		return errors.New("DATABASE_URL is required")
	case c.OperatorToken == "" || c.ApproverToken == "":
		return errors.New("both role API tokens are required")
	case c.OperatorToken == c.ApproverToken:
		return errors.New("operator and approver tokens must differ")
	case len(c.HMACKey) < 32:
		return errors.New("HMAC_KEY must contain at least 32 bytes")
	case c.MaxConcurrency < 1 || c.MaxConcurrency > 100:
		return errors.New("MAX_CONCURRENCY must be between 1 and 100")
	case c.CPS < 0 || c.CPS > 100:
		return errors.New("CPS must be between 0 and 100")
	case c.WindowStart >= c.WindowEnd:
		return errors.New("calling hours must be an increasing same-day window")
	case strings.Count(c.ARIEndpointTemplate, "%s") != 1:
		return errors.New("ARI_ENDPOINT_TEMPLATE must contain exactly one %s")
	}
	if c.DialingEnabled {
		if c.ARIURL == "" || c.ARIApp == "" || c.ARIUser == "" || c.ARIPassword == "" {
			return errors.New("ARI settings are required when dialing is enabled")
		}
		u, err := url.Parse(c.ARIURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("ARI_URL must be an absolute http(s) URL")
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
