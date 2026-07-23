package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type storageClient struct {
	baseURL    string
	projectID  string
	bucket     string
	httpClient *http.Client
}

func newStorageClient(baseURL, projectID, bucket string) *storageClient {
	return &storageClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		projectID: projectID,
		bucket:    bucket,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *storageClient) enabled() bool {
	return c != nil && c.baseURL != "" && c.projectID != "" && c.bucket != ""
}

func (c *storageClient) EnsureBucket(ctx context.Context) error {
	body := []byte(fmt.Sprintf(`{"name":%q}`, c.bucket))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/buckets", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forge-Project", c.projectID)
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

func (c *storageClient) PutObject(ctx context.Context, key string, data []byte) (string, error) {
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	u := fmt.Sprintf("%s/v1/buckets/%s/objects/%s", c.baseURL, url.PathEscape(c.bucket), url.PathEscape(key))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Forge-Project", c.projectID)
	req.Header.Set("X-Expected-SHA256", digest)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("put object HTTP %d: %s", resp.StatusCode, string(b))
	}
	return digest, nil
}

func (c *storageClient) GetObject(ctx context.Context, key string) ([]byte, error) {
	u := fmt.Sprintf("%s/v1/buckets/%s/objects/%s", c.baseURL, url.PathEscape(c.bucket), url.PathEscape(key))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Forge-Project", c.projectID)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("get object HTTP %d: %s", resp.StatusCode, string(b))
	}
	return io.ReadAll(resp.Body)
}
