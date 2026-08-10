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
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type ARIClient struct {
	config Config
	http   *http.Client
	dialer *websocket.Dialer
}

type OriginateCommand struct {
	AttemptID, ChannelID, Phone, CallerID, MediaSHA string
}

type OriginateResult struct {
	Accepted   bool
	NotFound   bool
	Outcome    string
	Uncertain  bool
	StatusCode int
}

type ARICommands interface {
	Originate(context.Context, OriginateCommand) (OriginateResult, error)
	Play(context.Context, string, string, string) (OriginateResult, error)
	Hangup(context.Context, string) (OriginateResult, error)
	ChannelExists(context.Context, string) (bool, error)
}

func NewARIClient(config Config) *ARIClient {
	return &ARIClient{
		config: config,
		http:   &http.Client{Timeout: 10 * time.Second},
		dialer: &websocket.Dialer{HandshakeTimeout: 10 * time.Second},
	}
}

func (c *ARIClient) Originate(ctx context.Context, cmd OriginateCommand) (OriginateResult, error) {
	phone, err := normalizeE164(cmd.Phone)
	if err != nil {
		return OriginateResult{Outcome: "invalid"},
			fmt.Errorf("invalid originate recipient: %w", err)
	}
	callerID, err := normalizeE164(cmd.CallerID)
	if err != nil {
		return OriginateResult{Outcome: "invalid"},
			fmt.Errorf("invalid originate caller ID: %w", err)
	}
	query := url.Values{}
	query.Set("endpoint", "Local/"+phone+"@"+c.config.ARIDialContext+"/n")
	query.Set("app", c.config.ARIApp)
	query.Set("appArgs", cmd.AttemptID)
	query.Set("callerId", callerID)
	query.Set("channelId", cmd.ChannelID)
	query.Set("timeout", "90")
	body, _ := json.Marshal(map[string]any{"variables": map[string]string{
		"DIALER_ATTEMPT_ID": cmd.AttemptID,
	}})
	result, requestErr := c.request(ctx, http.MethodPost, "channels", query, body, false)
	return c.resolveConflict(ctx, result, requestErr, path.Join("channels", cmd.ChannelID))
}

func (c *ARIClient) Play(
	ctx context.Context, channelID, playbackID, mediaSHA string,
) (OriginateResult, error) {
	query := url.Values{}
	query.Set("media", mediaURI(mediaSHA))
	query.Set("playbackId", playbackID)
	resource := path.Join("channels", channelID, "play")
	result, err := c.request(ctx, http.MethodPost, resource, query, nil, false)
	return c.resolveConflict(ctx, result, err, path.Join("playbacks", playbackID))
}

func (c *ARIClient) Hangup(ctx context.Context, channelID string) (OriginateResult, error) {
	return c.request(ctx, http.MethodDelete, path.Join("channels", channelID), nil, nil, true)
}

func (c *ARIClient) ChannelExists(ctx context.Context, channelID string) (bool, error) {
	return c.resourceExists(ctx, path.Join("channels", channelID))
}

func (c *ARIClient) resolveConflict(ctx context.Context, result OriginateResult,
	requestErr error, resource string) (OriginateResult, error) {
	if result.StatusCode != http.StatusConflict {
		return result, requestErr
	}
	exists, lookupErr := c.resourceExists(ctx, resource)
	if lookupErr != nil {
		return OriginateResult{Outcome: "ambiguous", Uncertain: true}, lookupErr
	}
	if exists {
		result.Accepted, result.Outcome = true, ""
		return result, nil
	}
	return result, requestErr
}

func (c *ARIClient) resourceExists(ctx context.Context, resource string) (bool, error) {
	result, err := c.request(ctx, http.MethodGet, resource, nil, nil, false)
	if result.NotFound {
		return false, nil
	}
	return result.Accepted, err
}

func (c *ARIClient) request(
	ctx context.Context, method, resource string, query url.Values, body []byte, missingOK bool,
) (OriginateResult, error) {
	var result OriginateResult
	base, err := url.Parse(c.config.ARIURL)
	if err != nil {
		return result, errors.New("invalid ARI base URL")
	}
	base.Path = ariPath(base.Path, resource)
	base.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, base.String(), bytes.NewReader(body))
	if err != nil {
		return result, errors.New("invalid ARI request")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetBasicAuth(c.config.ARIUser, c.config.ARIPassword)
	response, err := c.http.Do(req)
	if err != nil {
		return OriginateResult{Outcome: "ambiguous", Uncertain: true},
			errors.New("ARI transport outcome is ambiguous")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	result.StatusCode = response.StatusCode
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		result.Accepted = true
	case response.StatusCode == http.StatusNotFound:
		result.NotFound = true
		result.Accepted = missingOK
		result.Outcome = "not_found"
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		result.Outcome = "forbidden"
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
		result.Outcome = "temporary"
	case response.StatusCode >= 400 && response.StatusCode < 500:
		result.Outcome = "invalid"
	default:
		result.Outcome, result.Uncertain = "ambiguous", true
	}
	if !result.Accepted {
		return result, fmt.Errorf("ARI request returned status %d", response.StatusCode)
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
	base.Path = ariPath(base.Path, "events")
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

func ariPath(basePath, resource string) string {
	clean := strings.TrimRight(basePath, "/")
	if path.Base(clean) == "ari" {
		return path.Join(clean, resource)
	}
	return path.Join(clean, "ari", resource)
}
