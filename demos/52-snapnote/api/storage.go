package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// storageConfig holds Forge Storage connection settings for SnapNote attachments.
type storageConfig struct {
	BaseURL    string // API → storage (e.g. http://host.docker.internal:4107)
	PublicURL  string // Browser-facing base (e.g. http://app.snapnote.localhost:4000/storage)
	ProjectID  string
	Bucket     string
	SignTTLSec int64
}

func loadStorageConfig() storageConfig {
	base := strings.TrimSpace(os.Getenv("FORGE_STORAGE_URL"))
	if base == "" {
		base = "http://host.docker.internal:4107"
	}
	public := strings.TrimSpace(os.Getenv("FORGE_STORAGE_PUBLIC_URL"))
	if public == "" {
		public = "http://app.snapnote.localhost:4000/storage"
	}
	project := strings.TrimSpace(os.Getenv("FORGE_STORAGE_PROJECT"))
	if project == "" {
		project = strings.TrimSpace(os.Getenv("FORGE_PROJECT"))
	}
	if project == "" {
		project = "snapnote"
	}
	bucket := strings.TrimSpace(os.Getenv("FORGE_STORAGE_BUCKET"))
	if bucket == "" {
		bucket = "snapnote-attachments"
	}
	ttl := int64(900)
	if raw := strings.TrimSpace(os.Getenv("FORGE_STORAGE_SIGN_TTL_SECONDS")); raw != "" {
		var parsed int64
		if _, err := fmt.Sscanf(raw, "%d", &parsed); err == nil && parsed > 0 {
			ttl = parsed
		}
	}
	return storageConfig{
		BaseURL:    strings.TrimRight(base, "/"),
		PublicURL:  strings.TrimRight(public, "/"),
		ProjectID:  project,
		Bucket:     bucket,
		SignTTLSec: ttl,
	}
}

type storageClient struct {
	cfg        storageConfig
	httpClient *http.Client
}

func newStorageClient(cfg storageConfig) *storageClient {
	return &storageClient{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *storageClient) enabled() bool {
	return c != nil && c.cfg.BaseURL != "" && c.cfg.ProjectID != "" && c.cfg.Bucket != ""
}

func (c *storageClient) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+"/health/ready", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("storage ready HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (c *storageClient) EnsureBucket(ctx context.Context) error {
	body := []byte(fmt.Sprintf(`{"name":%q}`, c.cfg.Bucket))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/v1/buckets", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forge-Project", c.cfg.ProjectID)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusOK {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("create bucket HTTP %d: %s", resp.StatusCode, string(b))
}

type signResponse struct {
	Token     string `json:"token"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

// Sign issues a method-scoped access token and returns a browser-reachable upload/download URL.
func (c *storageClient) Sign(ctx context.Context, method, key string) (publicURL, expiresAt string, err error) {
	path := c.objectAPIPath(key) + "/sign"
	payload := fmt.Sprintf(`{"method":%q,"ttl_seconds":%d}`, method, c.cfg.SignTTLSec)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+path, strings.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forge-Project", c.cfg.ProjectID)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", "", fmt.Errorf("sign HTTP %d: %s", resp.StatusCode, string(b))
	}
	var signed signResponse
	if err := json.NewDecoder(resp.Body).Decode(&signed); err != nil {
		return "", "", fmt.Errorf("decode sign: %w", err)
	}
	if signed.URL == "" {
		return "", "", fmt.Errorf("sign response missing url")
	}
	return c.cfg.PublicURL + signed.URL, signed.ExpiresAt, nil
}

// GetObject streams an object from storage using project credentials (server-side).
func (c *storageClient) GetObject(ctx context.Context, key string) (io.ReadCloser, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+c.objectAPIPath(key), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("X-Forge-Project", c.cfg.ProjectID)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		return nil, "", fmt.Errorf("get object HTTP %d: %s", resp.StatusCode, string(b))
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	return resp.Body, ct, nil
}

func (c *storageClient) objectAPIPath(key string) string {
	return fmt.Sprintf(
		"/v1/buckets/%s/objects/%s",
		url.PathEscape(c.cfg.Bucket),
		encodeObjectKey(key),
	)
}

func encodeObjectKey(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func attachmentObjectKey(noteID, attachmentID, filename string) string {
	safe := sanitizeFilename(filename)
	return fmt.Sprintf("notes/%s/%s/%s", noteID, attachmentID, safe)
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "..", "_")
	if name == "" {
		return "file.bin"
	}
	if len(name) > 180 {
		name = name[:180]
	}
	return name
}
