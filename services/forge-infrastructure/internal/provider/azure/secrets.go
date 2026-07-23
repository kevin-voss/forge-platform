package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// HTTPSecrets resolves Azure credentials JSON from Forge Secrets :access.
type HTTPSecrets struct {
	BaseURL    string
	Project    string
	Env        string
	HTTPClient *http.Client
	AuthToken  string
}

// ResolveSecret loads the secret plaintext for secretName.
func (h *HTTPSecrets) ResolveSecret(ctx context.Context, secretName string) (string, error) {
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

// MapSecrets is an in-memory CredentialResolver for tests.
type MapSecrets struct {
	Values map[string]string
}

func (m *MapSecrets) ResolveSecret(ctx context.Context, secretName string) (string, error) {
	_ = ctx
	if m == nil || m.Values == nil {
		return "", fmt.Errorf("secret %q not found", secretName)
	}
	v, ok := m.Values[secretName]
	if !ok || strings.TrimSpace(v) == "" {
		return "", fmt.Errorf("secret %q not found", secretName)
	}
	return v, nil
}

// EnvFallbackSecrets tries MapSecrets, then FORGE_INFRA_AZURE_CREDENTIALS_JSON.
type EnvFallbackSecrets struct {
	Inner CredentialResolver
}

func (e *EnvFallbackSecrets) ResolveSecret(ctx context.Context, secretName string) (string, error) {
	if e != nil && e.Inner != nil {
		if v, err := e.Inner.ResolveSecret(ctx, secretName); err == nil && strings.TrimSpace(v) != "" {
			return v, nil
		}
	}
	if v := strings.TrimSpace(os.Getenv("FORGE_INFRA_AZURE_CREDENTIALS_JSON")); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("credential secret %q not found", secretName)
}
