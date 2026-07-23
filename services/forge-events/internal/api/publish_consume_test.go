package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"forge.local/services/forge-events/internal/api"
	"forge.local/services/forge-events/internal/events"
)

type stubPublisher struct {
	res events.PublishResult
	err error
	req events.PublishRequest
}

func (s *stubPublisher) Publish(_ context.Context, req events.PublishRequest) (events.PublishResult, error) {
	s.req = req
	return s.res, s.err
}

type stubConsumer struct {
	res events.ConsumeResult
	err error
}

func (s *stubConsumer) Consume(_ context.Context, _ events.ConsumeRequest) (events.ConsumeResult, error) {
	return s.res, s.err
}

func TestPublishHandlerAccepted(t *testing.T) {
	pub := &stubPublisher{res: events.PublishResult{EventID: "evt_1", Stream: "application", Seq: 42}}
	h := &api.PublishHandler{Publisher: pub, MaxBytes: 1024}
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"subject":"application.crashed","data":{"service":"demo"},"source":"runtime"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	if got["event_id"] != "evt_1" || got["stream"] != "application" {
		t.Fatalf("response = %#v", got)
	}
	if pub.req.Subject != "application.crashed" || pub.req.Source != "runtime" {
		t.Fatalf("publish req = %#v", pub.req)
	}
}

func TestPublishHandlerInvalidSubject(t *testing.T) {
	pub := &stubPublisher{err: events.ErrInvalidSubject}
	h := &api.PublishHandler{Publisher: pub, MaxBytes: 1024}
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewBufferString(`{"subject":"nope.bad","data":{}}`))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestPublishHandlerPayloadTooLarge(t *testing.T) {
	pub := &stubPublisher{err: events.ErrPayloadTooLarge}
	h := &api.PublishHandler{Publisher: pub, MaxBytes: 1024}
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewBufferString(`{"subject":"application.crashed","data":{}}`))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rr.Code)
	}
}

func TestConsumeHandlerOK(t *testing.T) {
	cons := &stubConsumer{res: events.ConsumeResult{Messages: []events.DeliveredMessage{{
		EventID:  "evt_1",
		Subject:  "application.crashed",
		Time:     time.Unix(0, 0).UTC(),
		Data:     json.RawMessage(`{"ok":true}`),
		AckToken: "ack",
	}}}}
	h := &api.ConsumeHandler{Consumer: cons, Wait: 100 * time.Millisecond}
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/consume", bytes.NewBufferString(`{"subject":"application.crashed","batch":10}`))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0]["event_id"] != "evt_1" {
		t.Fatalf("got %#v", got)
	}
}
