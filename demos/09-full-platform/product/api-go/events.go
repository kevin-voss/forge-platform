package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type eventsPublisher interface {
	PublishIncidentCreated(inc incident) error
}

type httpEventsPublisher struct {
	baseURL string
	source  string
	client  *http.Client
}

func newHTTPEventsPublisher(baseURL, source string) *httpEventsPublisher {
	return &httpEventsPublisher{
		baseURL: stringsTrimRightSlash(baseURL),
		source:  source,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

type incidentCreatedData struct {
	IncidentID string `json:"incident_id"`
	Title      string `json:"title"`
	Severity   string `json:"severity"`
	OccurredAt string `json:"occurred_at"`
	Source     string `json:"source,omitempty"`
	Status     string `json:"status,omitempty"`
}

func (p *httpEventsPublisher) PublishIncidentCreated(inc incident) error {
	if p == nil || p.baseURL == "" {
		return fmt.Errorf("events url not configured")
	}
	body := map[string]any{
		"subject": "incident.created",
		"source":  p.source,
		"data": incidentCreatedData{
			IncidentID: inc.ID,
			Title:      inc.Title,
			Severity:   inc.Severity,
			OccurredAt: inc.CreatedAt.UTC().Format(time.RFC3339),
			Source:     p.source,
			Status:     inc.Status,
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, p.baseURL+"/v1/events", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
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

func stringsTrimRightSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
