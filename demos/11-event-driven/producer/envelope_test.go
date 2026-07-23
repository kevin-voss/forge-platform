package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestValidCrashPayloadSchemaShape(t *testing.T) {
	p := ValidCrashPayload("api", "oom", 1)
	if p.Service != "api" {
		t.Fatalf("service = %q", p.Service)
	}
	if p.Reason != "oom" {
		t.Fatalf("reason = %q", p.Reason)
	}
	if _, err := time.Parse(time.RFC3339, p.OccurredAt); err != nil {
		t.Fatalf("occurred_at not RFC3339: %v", err)
	}

	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"service", "reason", "occurred_at"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("missing required key %q", key)
		}
	}
}

func TestPoisonCrashPayload(t *testing.T) {
	p := PoisonCrashPayload()
	if p.Reason != "poison" {
		t.Fatalf("reason = %q, want poison", p.Reason)
	}
}

func TestMalformedCrashPayload(t *testing.T) {
	raw := MalformedCrashPayload()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["service"] != "" {
		t.Fatalf("expected empty service for malformed payload")
	}
}

func TestEncodePublishBody(t *testing.T) {
	body, err := encodePublishBody(ValidCrashPayload("api", "oom", 1))
	if err != nil {
		t.Fatal(err)
	}
	var env publishBody
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	if env.Subject != subjectApplicationCrashed {
		t.Fatalf("subject = %q", env.Subject)
	}
	if env.Source != "demo-events-producer" {
		t.Fatalf("source = %q", env.Source)
	}
	if len(env.Data) == 0 {
		t.Fatal("data empty")
	}
}
