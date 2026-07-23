package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPublishIncidentCreatedEnvelope(t *testing.T) {
	var gotSubject string
	var gotData map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/events" || r.Method != http.MethodPost {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		gotSubject, _ = body["subject"].(string)
		gotData, _ = body["data"].(map[string]any)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"event_id":"e1","stream":"incident","seq":1}`))
	}))
	defer srv.Close()

	pub := newHTTPEventsPublisher(srv.URL, "incident-api")
	inc := incident{
		ID:        "abc",
		Title:     "latency",
		Severity:  "high",
		Status:    "open",
		CreatedAt: time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC),
	}
	if err := pub.PublishIncidentCreated(inc); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if gotSubject != "incident.created" {
		t.Fatalf("subject=%q", gotSubject)
	}
	if gotData["incident_id"] != "abc" || gotData["severity"] != "high" {
		t.Fatalf("data=%v", gotData)
	}
}

func TestCreateIncidentPublishesEvent(t *testing.T) {
	published := make(chan incident, 1)
	srv := testServer(t)
	srv.events = publishFunc(func(inc incident) error {
		published <- inc
		return nil
	})
	handler := srv.routes()

	payload := []byte(`{"title":"API latency","severity":"high"}`)
	req := httptest.NewRequest(http.MethodPost, "/incidents", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d", rec.Code)
	}
	select {
	case inc := <-published:
		if inc.Title != "API latency" {
			t.Fatalf("title=%q", inc.Title)
		}
	default:
		t.Fatal("expected publish")
	}
}

type publishFunc func(incident) error

func (f publishFunc) PublishIncidentCreated(inc incident) error { return f(inc) }
