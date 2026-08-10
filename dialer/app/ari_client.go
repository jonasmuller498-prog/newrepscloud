package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/gorilla/websocket"
)

type ARIClient struct {
	config Config
	http   *http.Client
	dialer *websocket.Dialer
}

type OriginateCommand struct {
	AttemptID, ChannelID, Phone, CallerID, Media string
}

type OriginateResult struct {
	Accepted  bool
	Outcome   string
	Uncertain bool
}

func NewARIClient(config Config) *ARIClient {
	return &ARIClient{
		config: config,
		http:   &http.Client{Timeout: 10 * time.Second},
		dialer: &websocket.Dialer{HandshakeTimeout: 10 * time.Second},
	}
}

func (c *ARIClient) Originate(ctx context.Context, cmd OriginateCommand) (OriginateResult, error) {
	var result OriginateResult
	base, err := url.Parse(c.config.ARIURL)
	if err != nil {
		return result, err
	}
	base.Path = path.Join(base.Path, "ari/channels")
	query := base.Query()
	query.Set("endpoint", fmt.Sprintf(c.config.ARIEndpointTemplate, cmd.Phone))
	query.Set("extension", c.config.ARIExtension)
	query.Set("context", c.config.ARIContext)
	query.Set("priority", "1")
	query.Set("app", c.config.ARIApp)
	query.Set("appArgs", cmd.AttemptID)
	query.Set("callerId", cmd.CallerID)
	query.Set("channelId", cmd.ChannelID)
	base.RawQuery = query.Encode()
	body, _ := json.Marshal(map[string]any{"variables": map[string]string{
		"DIALER_ATTEMPT_ID": cmd.AttemptID,
		"DIALER_MEDIA":      cmd.Media,
	}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.config.ARIUser, c.config.ARIPassword)
	response, err := c.http.Do(req)
	if err != nil {
		result.Uncertain, result.Outcome = true, "ambiguous"
		return result, errors.New("ARI originate transport outcome is ambiguous")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		result.Accepted = true
	case response.StatusCode == 401 || response.StatusCode == 403:
		result.Outcome = "forbidden"
	case response.StatusCode == 429 || response.StatusCode >= 500:
		result.Outcome = "temporary"
	case response.StatusCode >= 400 && response.StatusCode < 500:
		result.Outcome = "invalid"
	default:
		result.Uncertain, result.Outcome = true, "ambiguous"
	}
	if !result.Accepted {
		return result, fmt.Errorf("ARI originate returned status %d", response.StatusCode)
	}
	return result, nil
}

func (c *ARIClient) ConnectEvents(ctx context.Context) (*websocket.Conn, error) {
	base, err := url.Parse(c.config.ARIURL)
	if err != nil {
		return nil, err
	}
	if base.Scheme == "https" {
		base.Scheme = "wss"
	} else {
		base.Scheme = "ws"
	}
	base.Path = path.Join(base.Path, "ari/events")
	query := base.Query()
	query.Set("app", c.config.ARIApp)
	query.Set("subscribeAll", "false")
	base.RawQuery = query.Encode()
	header := http.Header{}
	credentials := base64.StdEncoding.EncodeToString(
		[]byte(c.config.ARIUser + ":" + c.config.ARIPassword))
	header.Set("Authorization", "Basic "+credentials)
	conn, _, err := c.dialer.DialContext(ctx, base.String(), header)
	return conn, err
}
