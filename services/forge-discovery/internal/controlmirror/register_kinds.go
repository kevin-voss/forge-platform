package controlmirror

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// KindRegistration is POST /v1/kinds body (epic 21.01 contract).
type KindRegistration struct {
	APIVersion    string `json:"apiVersion"`
	Kind          string `json:"kind"`
	Plural        string `json:"plural"`
	Scope         string `json:"scope"`
	Controller    string `json:"controller"`
	SchemaVersion int    `json:"schemaVersion"`
	IDPrefix      string `json:"idPrefix,omitempty"`
}

// ServiceKindPayload is the documented Service registration body.
func ServiceKindPayload() KindRegistration {
	return KindRegistration{
		APIVersion:    "forge.dev/v1",
		Kind:          "Service",
		Plural:        "services",
		Scope:         "namespaced",
		Controller:    "forge-discovery",
		SchemaVersion: 1,
		IDPrefix:      "svc",
	}
}

// EndpointKindPayload is the documented Endpoint registration body.
func EndpointKindPayload() KindRegistration {
	return KindRegistration{
		APIVersion:    "forge.dev/v1",
		Kind:          "Endpoint",
		Plural:        "endpoints",
		Scope:         "namespaced",
		Controller:    "forge-discovery",
		SchemaVersion: 1,
		IDPrefix:      "end",
	}
}

// Client registers kinds against Control.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	Log        *slog.Logger
}

// New returns a Control kind-registration client.
func New(baseURL string, log *slog.Logger) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		Log: log,
	}
}

// RegisterKinds posts Service and Endpoint kinds with retry/backoff up to maxWait.
func (c *Client) RegisterKinds(ctx context.Context, maxWait time.Duration) error {
	if c == nil || c.BaseURL == "" {
		return fmt.Errorf("control URL is required")
	}
	deadline := time.Now().Add(maxWait)
	backoff := 500 * time.Millisecond
	var lastErr error
	for {
		err := c.registerOnce(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
		if c.Log != nil {
			c.Log.Warn("kind registration attempt failed",
				"event", "discovery.kind_registered",
				"error", err.Error(),
			)
		}
		if time.Now().Add(backoff).After(deadline) {
			return fmt.Errorf("kind registration failed after %s: %w", maxWait, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
}

func (c *Client) registerOnce(ctx context.Context) error {
	for _, payload := range []KindRegistration{ServiceKindPayload(), EndpointKindPayload()} {
		result, err := c.postKind(ctx, payload)
		if err != nil {
			return err
		}
		if c.Log != nil {
			c.Log.Info("kind registered",
				"event", "discovery.kind_registered",
				"kind", payload.Kind,
				"plural", payload.Plural,
				"result", result,
			)
		}
	}
	return nil
}

func (c *Client) postKind(ctx context.Context, payload KindRegistration) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/kinds", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST /v1/kinds: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
		return "already_registered", nil
	case http.StatusCreated:
		return "created", nil
	default:
		return "", fmt.Errorf("POST /v1/kinds %s: status %d body %s", payload.Kind, resp.StatusCode, truncate(string(data), 256))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
