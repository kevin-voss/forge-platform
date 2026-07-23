package bootstraptoken

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Issued is a single-use NodePool-scoped bootstrap token from epic 22 / Control.
type Issued struct {
	Token     string
	ExpiresAt string
	ID        string
	NodePool  string
}

// Client requests bootstrap tokens from Control's POST /v1/nodes/bootstrap-tokens.
type Client struct {
	BaseURL      string
	Organization string
	AuthMode     string
	HTTPClient   *http.Client
	// DevToken is returned when AuthMode=dev and the token API is unreachable.
	DevToken string
}

// New constructs a Client.
func New(baseURL, organization, authMode string) *Client {
	return &Client{
		BaseURL:      strings.TrimRight(baseURL, "/"),
		Organization: organization,
		AuthMode:     strings.ToLower(strings.TrimSpace(authMode)),
		HTTPClient:   &http.Client{Timeout: 10 * time.Second},
		DevToken:     "dev-bootstrap-token",
	}
}

type issueRequest struct {
	Organization string `json:"organization"`
	NodePool     string `json:"node_pool"`
	TTLSeconds   int64  `json:"ttl_seconds,omitempty"`
}

type issueResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	ID        string `json:"id"`
	Scope     struct {
		Organization string  `json:"organization"`
		NodePool     *string `json:"node_pool"`
	} `json:"scope"`
}

// Issue requests one single-use token scoped to nodePool.
func (c *Client) Issue(ctx context.Context, nodePool string, ttlSeconds int64) (*Issued, error) {
	if c == nil {
		return nil, fmt.Errorf("bootstraptoken client is nil")
	}
	nodePool = strings.TrimSpace(nodePool)
	if nodePool == "" {
		return nil, fmt.Errorf("node_pool is required")
	}
	org := strings.TrimSpace(c.Organization)
	if org == "" {
		org = "forge"
	}
	if ttlSeconds <= 0 {
		ttlSeconds = 900
	}

	body, err := json.Marshal(issueRequest{
		Organization: org,
		NodePool:     nodePool,
		TTLSeconds:   ttlSeconds,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/nodes/bootstrap-tokens", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if c.AuthMode == "dev" {
			return &Issued{Token: c.devToken(), NodePool: nodePool, ID: "bst_dev"}, nil
		}
		return nil, fmt.Errorf("issue bootstrap token: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if c.AuthMode == "dev" {
			return &Issued{Token: c.devToken(), NodePool: nodePool, ID: "bst_dev"}, nil
		}
		return nil, fmt.Errorf("issue bootstrap token: status %d body %s", resp.StatusCode, truncate(string(data), 256))
	}
	var out issueResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode bootstrap token response: %w", err)
	}
	if strings.TrimSpace(out.Token) == "" {
		return nil, fmt.Errorf("bootstrap token response missing token")
	}
	return &Issued{
		Token:     out.Token,
		ExpiresAt: out.ExpiresAt,
		ID:        out.ID,
		NodePool:  nodePool,
	}, nil
}

func (c *Client) devToken() string {
	if strings.TrimSpace(c.DevToken) != "" {
		return c.DevToken
	}
	return "dev-bootstrap-token"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
