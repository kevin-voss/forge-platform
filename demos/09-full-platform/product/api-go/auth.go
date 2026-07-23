package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type introspectResponse struct {
	Active    bool   `json:"active"`
	Role      string `json:"role"`
	ProjectID string `json:"project_id"`
	Subject   string `json:"subject"`
}

type identityClient struct {
	baseURL    string
	httpClient *http.Client
	projectID  string
}

func newIdentityClient(baseURL, projectID string) *identityClient {
	return &identityClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		projectID: projectID,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
	}
}

func (c *identityClient) Introspect(ctx context.Context, token string) (introspectResponse, error) {
	body, _ := json.Marshal(map[string]string{"token": token})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/auth/introspect", bytes.NewReader(body))
	if err != nil {
		return introspectResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return introspectResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return introspectResponse{}, fmt.Errorf("introspect HTTP %d", resp.StatusCode)
	}
	var out introspectResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return introspectResponse{}, err
	}
	return out, nil
}

func (s *server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.ProductAuth != "enforce" {
			next(w, r)
			return
		}
		raw := r.Header.Get("Authorization")
		if !strings.HasPrefix(strings.ToLower(raw), "bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
			return
		}
		token := strings.TrimSpace(raw[7:])
		if token == "" || s.identity == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
			return
		}
		info, err := s.identity.Introspect(r.Context(), token)
		if err != nil || !info.Active {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
			return
		}
		if s.cfg.ProjectID != "" && info.ProjectID != "" && info.ProjectID != s.cfg.ProjectID {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		next(w, r)
	}
}
