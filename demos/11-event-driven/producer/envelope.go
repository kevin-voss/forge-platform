package main

import (
	"encoding/json"
	"fmt"
	"time"
)

const subjectApplicationCrashed = "application.crashed"

// CrashPayload is the schema-valid application.crashed data object.
type CrashPayload struct {
	Service    string `json:"service"`
	Reason     string `json:"reason"`
	OccurredAt string `json:"occurred_at"`
	ExitCode   int    `json:"exit_code,omitempty"`
	NodeID     string `json:"node_id,omitempty"`
}

// ValidCrashPayload builds a schema-valid application.crashed payload.
func ValidCrashPayload(service, reason string, index int) CrashPayload {
	if service == "" {
		service = "demo-api"
	}
	if reason == "" {
		reason = "oom"
	}
	return CrashPayload{
		Service:    service,
		Reason:     reason,
		OccurredAt: time.Now().UTC().Truncate(time.Second).Format(time.RFC3339),
		ExitCode:   137,
		NodeID:     fmt.Sprintf("node-demo-%d", index),
	}
}

// PoisonCrashPayload is a schema-valid event the Elixir consumer naks.
func PoisonCrashPayload() CrashPayload {
	return CrashPayload{
		Service:    "poison-service",
		Reason:     "poison",
		OccurredAt: time.Now().UTC().Truncate(time.Second).Format(time.RFC3339),
		ExitCode:   1,
		NodeID:     "node-poison",
	}
}

// MalformedCrashPayload intentionally violates the schema (missing required fields).
func MalformedCrashPayload() json.RawMessage {
	return json.RawMessage(`{"service":"","reason":"","extra":true}`)
}

type publishBody struct {
	Subject string          `json:"subject"`
	Data    json.RawMessage `json:"data"`
	Source  string          `json:"source"`
}

func encodePublishBody(data any) ([]byte, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(publishBody{
		Subject: subjectApplicationCrashed,
		Data:    raw,
		Source:  "demo-events-producer",
	})
}
