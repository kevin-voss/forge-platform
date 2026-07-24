package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type storageConfig struct {
	BaseURL   string
	ProjectID string
	Bucket    string
}

func loadStorageConfig() storageConfig {
	base := strings.TrimSpace(os.Getenv("FORGE_STORAGE_URL"))
	if base == "" {
		base = "http://host.docker.internal:4107"
	}
	project := strings.TrimSpace(os.Getenv("FORGE_STORAGE_PROJECT"))
	if project == "" {
		project = "snapnote"
	}
	bucket := strings.TrimSpace(os.Getenv("FORGE_STORAGE_BUCKET"))
	if bucket == "" {
		bucket = "snapnote-attachments"
	}
	return storageConfig{
		BaseURL:   strings.TrimRight(base, "/"),
		ProjectID: project,
		Bucket:    bucket,
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

func (c *storageClient) GetObject(ctx context.Context, key string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+c.objectAPIPath(key), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Forge-Project", c.cfg.ProjectID)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get object HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (c *storageClient) PutObject(ctx context.Context, key string, contentType string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.cfg.BaseURL+c.objectAPIPath(key), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("X-Forge-Project", c.cfg.ProjectID)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("put object HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
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
