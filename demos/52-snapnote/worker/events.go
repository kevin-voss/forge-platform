package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type eventsConfig struct {
	BaseURL       string
	Consumer      string
	Identity      string
	Subject       string
	AckWaitS      int
	MaxDeliveries int
	PollMS        int
	Batch         int
}

func loadEventsConfig() eventsConfig {
	base := strings.TrimSpace(os.Getenv("FORGE_EVENTS_URL"))
	if base == "" {
		base = "http://host.docker.internal:4105"
	}
	consumer := strings.TrimSpace(os.Getenv("FORGE_EVENTS_CONSUMER"))
	if consumer == "" {
		consumer = "snapnote-attachments"
	}
	identity := strings.TrimSpace(os.Getenv("FORGE_EVENTS_CONSUMER_IDENTITY"))
	if identity == "" {
		identity = consumer
	}
	subject := strings.TrimSpace(os.Getenv("FORGE_EVENTS_SUBJECT"))
	if subject == "" {
		subject = "attachment.uploaded"
	}
	ackWait := envInt("FORGE_DEFAULT_ACK_WAIT_S", 30)
	maxDel := envInt("FORGE_DEFAULT_MAX_DELIVERIES", 5)
	pollMS := envInt("FORGE_EVENTS_POLL_MS", 500)
	batch := envInt("FORGE_EVENTS_BATCH", 8)
	return eventsConfig{
		BaseURL:       strings.TrimRight(base, "/"),
		Consumer:      consumer,
		Identity:      identity,
		Subject:       subject,
		AckWaitS:      ackWait,
		MaxDeliveries: maxDel,
		PollMS:        pollMS,
		Batch:         batch,
	}
}

func envInt(key string, def int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

type eventsClient struct {
	cfg        eventsConfig
	httpClient *http.Client
}

func newEventsClient(cfg eventsConfig) *eventsClient {
	return &eventsClient{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 45 * time.Second,
		},
	}
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

func (c *eventsClient) EnsureConsumer(ctx context.Context) error {
	body := map[string]any{
		"name":           c.cfg.Consumer,
		"subject":        c.cfg.Subject,
		"ack_wait_s":     c.cfg.AckWaitS,
		"max_deliveries": c.cfg.MaxDeliveries,
		"identity":       c.cfg.Identity,
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/v1/consumers", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("create consumer HTTP %d: %s", resp.StatusCode, string(b))
}

type deliveredMessage struct {
	EventID       string          `json:"event_id"`
	Subject       string          `json:"subject"`
	AckToken      string          `json:"ack_token"`
	DeliveryCount int             `json:"delivery_count"`
	Data          json.RawMessage `json:"data"`
}

type consumeResponse struct {
	Messages []deliveredMessage `json:"messages"`
}

func (c *eventsClient) Consume(ctx context.Context) ([]deliveredMessage, error) {
	body := map[string]any{
		"consumer": c.cfg.Consumer,
		"batch":    c.cfg.Batch,
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/v1/consume", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("consume HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	var out consumeResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, err
	}
	return out.Messages, nil
}

func (c *eventsClient) MarkProcessed(ctx context.Context, eventID string) error {
	body := map[string]any{
		"consumer": c.cfg.Consumer,
		"event_id": eventID,
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/v1/processed", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("processed HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (c *eventsClient) Ack(ctx context.Context, ackToken string) error {
	body := map[string]any{"ack_token": ackToken}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/v1/ack", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("ack HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (c *eventsClient) Nak(ctx context.Context, ackToken string, delayS int) error {
	body := map[string]any{"ack_token": ackToken}
	if delayS > 0 {
		body["delay_s"] = delayS
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/v1/nak", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("nak HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
