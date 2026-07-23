package hetzner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// HTTPSecrets resolves a Hetzner API token from Forge Secrets :access.
// Convention: POST {BaseURL}/v1/projects/{Project}/envs/{Env}/secrets/{name}:access
type HTTPSecrets struct {
	BaseURL    string
	Project    string
	Env        string
	HTTPClient *http.Client
	AuthToken  string // optional bearer for forge-secrets
}

// ResolveToken loads the secret plaintext for secretName.
func (h *HTTPSecrets) ResolveToken(ctx context.Context, secretName string) (string, error) {
	if h == nil || strings.TrimSpace(h.BaseURL) == "" {
		return "", fmt.Errorf("forge secrets URL is not configured")
	}
	project := h.Project
	if project == "" {
		project = "forge"
	}
	env := h.Env
	if env == "" {
		env = "default"
	}
	client := h.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	url := strings.TrimRight(h.BaseURL, "/") +
		"/v1/projects/" + project + "/envs/" + env + "/secrets/" + secretName + ":access"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	if tok := strings.TrimSpace(h.AuthToken); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("secrets access %s: %s", secretName, resp.Status)
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if strings.TrimSpace(body.Value) == "" {
		return "", fmt.Errorf("secret %q has empty value", secretName)
	}
	return body.Value, nil
}

// EnvFallbackTokens tries MapTokens, then FORGE_INFRA_HETZNER_API_TOKEN for local demos.
type EnvFallbackTokens struct {
	Inner TokenResolver
}

func (e *EnvFallbackTokens) ResolveToken(ctx context.Context, secretName string) (string, error) {
	if e != nil && e.Inner != nil {
		if v, err := e.Inner.ResolveToken(ctx, secretName); err == nil && strings.TrimSpace(v) != "" {
			return v, nil
		}
	}
	if v := strings.TrimSpace(os.Getenv("FORGE_INFRA_HETZNER_API_TOKEN")); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("token secret %q not found", secretName)
}
