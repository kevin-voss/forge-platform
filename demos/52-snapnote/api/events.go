package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type eventsConfig struct {
	BaseURL string
	Source  string
	Subject string
}

func loadEventsConfig() eventsConfig {
	base := strings.TrimSpace(os.Getenv("FORGE_EVENTS_URL"))
	if base == "" {
		base = "http://host.docker.internal:4105"
	}
	source := strings.TrimSpace(os.Getenv("FORGE_SERVICE_NAME"))
	if source == "" {
		source = "snapnote-api"
	}
	subject := strings.TrimSpace(os.Getenv("FORGE_EVENTS_SUBJECT"))
	if subject == "" {
		subject = "attachment.uploaded"
	}
	return eventsConfig{
		BaseURL: strings.TrimRight(base, "/"),
		Source:  source,
		Subject: subject,
	}
}

type eventsClient struct {
	cfg        eventsConfig
	httpClient *http.Client
}

func newEventsClient(cfg eventsConfig) *eventsClient {
	return &eventsClient{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *eventsClient) enabled() bool {
	return c != nil && c.cfg.BaseURL != "" && c.cfg.Subject != ""
}

func (c *eventsClient) Ping(ctx context.Context) error {
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
		return fmt.Errorf("events ready HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

type attachmentUploadedData struct {
	AttachmentID string `json:"attachment_id"`
	NoteID       string `json:"note_id"`
	ObjectKey    string `json:"object_key"`
	ContentType  string `json:"content_type"`
	UploadedAt   string `json:"uploaded_at"`
	Source       string `json:"source,omitempty"`
}

// PublishAttachmentUploaded publishes attachment.uploaded with Idempotency-Key=attachment_id.
func (c *eventsClient) PublishAttachmentUploaded(ctx context.Context, att *Attachment) error {
	if !c.enabled() {
		return fmt.Errorf("events url not configured")
	}
	body := map[string]any{
		"subject": c.cfg.Subject,
		"source":  c.cfg.Source,
		"data": attachmentUploadedData{
			AttachmentID: att.ID,
			NoteID:       att.NoteID,
			ObjectKey:    att.ObjectKey,
			ContentType:  att.ContentType,
			UploadedAt:   time.Now().UTC().Format(time.RFC3339),
			Source:       c.cfg.Source,
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/v1/events", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// Business key for publish dedup + consumer idempotency correlation.
	req.Header.Set("Idempotency-Key", att.ID)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("events publish HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
