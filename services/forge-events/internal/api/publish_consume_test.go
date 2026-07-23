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
	"forge.local/services/forge-events/internal/consumers"
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
	req consumers.ConsumeRequest
}

func (s *stubConsumer) Consume(_ context.Context, req consumers.ConsumeRequest) (events.ConsumeResult, error) {
	s.req = req
	return s.res, s.err
}

type stubStore struct {
	info consumers.ConsumerInfo
	err  error
}

func (s *stubStore) Create(_ consumers.CreateRequest) (consumers.ConsumerInfo, error) {
	return s.info, s.err
}

type stubAcker struct {
	ackErr error
	nakErr error
	token  string
	delay  time.Duration
}

func (s *stubAcker) Ack(token string) error {
	s.token = token
	return s.ackErr
}

func (s *stubAcker) Nak(token string, delay time.Duration) error {
	s.token = token
	s.delay = delay
	return s.nakErr
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
		EventID:       "evt_1",
		Subject:       "application.crashed",
		Time:          time.Unix(0, 0).UTC(),
		Data:          json.RawMessage(`{"ok":true}`),
		AckToken:      "ack",
		DeliveryCount: 1,
	}}}}
	h := &api.ConsumeHandler{Consumer: cons, Wait: 100 * time.Millisecond}
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/consume", bytes.NewBufferString(`{"consumer":"deploy-worker","batch":10}`))
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
	if got.Messages[0]["delivery_count"] != float64(1) {
		t.Fatalf("delivery_count = %#v", got.Messages[0]["delivery_count"])
	}
	if cons.req.Consumer != "deploy-worker" {
		t.Fatalf("consumer = %q", cons.req.Consumer)
	}
}

func TestConsumeHandlerRequiresConsumer(t *testing.T) {
	h := &api.ConsumeHandler{Consumer: &stubConsumer{}, Wait: 100 * time.Millisecond}
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/v1/consume", bytes.NewBufferString(`{"batch":10}`))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestCreateConsumerHandler(t *testing.T) {
	store := &stubStore{info: consumers.ConsumerInfo{
		Name: "deploy-worker", Subject: "deployment.completed",
		AckWaitS: 30, MaxDeliveries: 5, Stream: "deployment",
		CreatedAt: time.Unix(0, 0).UTC(),
	}}
	h := &api.ConsumersHandler{Store: store, Acker: &stubAcker{}, MaxBytes: 1024}
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/consumers", bytes.NewBufferString(
		`{"name":"deploy-worker","subject":"deployment.completed","ack_wait_s":30,"max_deliveries":5}`,
	))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateConsumerConflict(t *testing.T) {
	store := &stubStore{err: consumers.ErrConflict}
	h := &api.ConsumersHandler{Store: store, Acker: &stubAcker{}, MaxBytes: 1024}
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/v1/consumers", bytes.NewBufferString(
		`{"name":"deploy-worker","subject":"deployment.completed"}`,
	))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
}

func TestAckNakHandlers(t *testing.T) {
	acker := &stubAcker{}
	h := &api.ConsumersHandler{Store: &stubStore{}, Acker: acker, MaxBytes: 1024}
	mux := http.NewServeMux()
	h.Register(mux)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/ack", bytes.NewBufferString(`{"ack_token":"tok1"}`)))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("ack status = %d", rr.Code)
	}
	if acker.token != "tok1" {
		t.Fatalf("ack token = %q", acker.token)
	}

	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/nak", bytes.NewBufferString(`{"ack_token":"tok2","delay_s":5}`)))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("nak status = %d", rr.Code)
	}
	if acker.token != "tok2" || acker.delay != 5*time.Second {
		t.Fatalf("nak token=%q delay=%v", acker.token, acker.delay)
	}
}

func TestAckNotFound(t *testing.T) {
	acker := &stubAcker{ackErr: consumers.ErrAckNotFound}
	h := &api.ConsumersHandler{Store: &stubStore{}, Acker: acker, MaxBytes: 1024}
	mux := http.NewServeMux()
	h.Register(mux)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/ack", bytes.NewBufferString(`{"ack_token":"missing"}`)))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestAckExpired(t *testing.T) {
	acker := &stubAcker{ackErr: consumers.ErrAckExpired}
	h := &api.ConsumersHandler{Store: &stubStore{}, Acker: acker, MaxBytes: 1024}
	mux := http.NewServeMux()
	h.Register(mux)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/ack", bytes.NewBufferString(`{"ack_token":"old"}`)))
	if rr.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rr.Code)
	}
}
