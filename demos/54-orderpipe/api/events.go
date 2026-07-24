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

const (
	subjectPlaced    = "order.placed"
	subjectValidated = "order.validated"
	subjectCharged   = "order.charged"
	subjectFulfilled = "order.fulfilled"
	subjectNotified  = "order.notified"
)

type eventsConfig struct {
	BaseURL       string
	Source        string
	AckWaitS      int
	MaxDeliveries int
	PollMS        int
	Batch         int
	// Consumer names (one durable per subject stage).
	ValidateConsumer  string
	ChargeConsumer    string
	FulfilledConsumer string
	NotifiedConsumer  string
}

func loadEventsConfig() eventsConfig {
	base := strings.TrimSpace(os.Getenv("FORGE_EVENTS_URL"))
	if base == "" {
		base = "http://host.docker.internal:4105"
	}
	source := strings.TrimSpace(os.Getenv("FORGE_SERVICE_NAME"))
	if source == "" {
		source = "orderpipe-api"
	}
	return eventsConfig{
		BaseURL:           strings.TrimRight(base, "/"),
		Source:            source,
		AckWaitS:          envInt("FORGE_DEFAULT_ACK_WAIT_S", 30),
		MaxDeliveries:     envInt("FORGE_DEFAULT_MAX_DELIVERIES", 5),
		PollMS:            envInt("FORGE_EVENTS_POLL_MS", 400),
		Batch:             envInt("FORGE_EVENTS_BATCH", 8),
		ValidateConsumer:  envOr("FORGE_EVENTS_CONSUMER_VALIDATE", "orderpipe-validate"),
		ChargeConsumer:    envOr("FORGE_EVENTS_CONSUMER_CHARGE", "orderpipe-charge"),
		FulfilledConsumer: envOr("FORGE_EVENTS_CONSUMER_FULFILLED", "orderpipe-mark-fulfilled"),
		NotifiedConsumer:  envOr("FORGE_EVENTS_CONSUMER_NOTIFIED", "orderpipe-mark-notified"),
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

func (c *eventsClient) enabled() bool {
	return c != nil && strings.TrimSpace(c.cfg.BaseURL) != ""
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

type orderEventData struct {
	OrderID       string `json:"order_id"`
	CustomerEmail string `json:"customer_email"`
	Status        string `json:"status"`
	TotalCents    int    `json:"total_cents"`
	OccurredAt    string `json:"occurred_at"`
	Source        string `json:"source,omitempty"`
}

func (c *eventsClient) PublishOrderEvent(ctx context.Context, subject string, order *Order) error {
	if !c.enabled() {
		return fmt.Errorf("events url not configured")
	}
	if order == nil {
		return fmt.Errorf("order is required")
	}
	status := strings.TrimPrefix(subject, "order.")
	body := map[string]any{
		"subject": subject,
		"source":  c.cfg.Source,
		"data": orderEventData{
			OrderID:       order.ID,
			CustomerEmail: order.CustomerEmail,
			Status:        status,
			TotalCents:    order.TotalCents,
			OccurredAt:    time.Now().UTC().Format(time.RFC3339),
			Source:        c.cfg.Source,
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
	req.Header.Set("Idempotency-Key", order.ID+":"+subject)
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

type consumerBinding struct {
	Name    string
	Subject string
}

func (c *eventsClient) bindings() []consumerBinding {
	// validate/charge are owned by the order-saga driver (54.05); API only
	// mirrors fulfill/notify completions onto order status + saga_events.
	return []consumerBinding{
		{Name: c.cfg.FulfilledConsumer, Subject: subjectFulfilled},
		{Name: c.cfg.NotifiedConsumer, Subject: subjectNotified},
	}
}

func (c *eventsClient) EnsureConsumers(ctx context.Context) error {
	for _, b := range c.bindings() {
		if err := c.ensureConsumer(ctx, b); err != nil {
			return err
		}
	}
	return nil
}

func (c *eventsClient) ensureConsumer(ctx context.Context, b consumerBinding) error {
	body := map[string]any{
		"name":           b.Name,
		"subject":        b.Subject,
		"ack_wait_s":     c.cfg.AckWaitS,
		"max_deliveries": c.cfg.MaxDeliveries,
		"identity":       b.Name,
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
	bbody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("create consumer %s HTTP %d: %s", b.Name, resp.StatusCode, string(bbody))
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

func (c *eventsClient) Consume(ctx context.Context, consumer string) ([]deliveredMessage, error) {
	body := map[string]any{
		"consumer": consumer,
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

func (c *eventsClient) MarkProcessed(ctx context.Context, consumer, eventID string) error {
	body := map[string]any{"consumer": consumer, "event_id": eventID}
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
