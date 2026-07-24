package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

// fakeEventsBroker is an in-memory forge-events stand-in for choreography tests.
// Each durable consumer gets its own queue (JetStream fan-out).
type fakeEventsBroker struct {
	mu        sync.Mutex
	queues    map[string][]deliveredMessage // consumer → pending
	consumers map[string]string             // consumer → subject
	seq       int
}

func newFakeEventsBroker() *fakeEventsBroker {
	return &fakeEventsBroker{
		queues:    map[string][]deliveredMessage{},
		consumers: map[string]string{},
	}
}

func (b *fakeEventsBroker) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/consumers", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name    string `json:"name"`
			Subject string `json:"subject"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		b.mu.Lock()
		b.consumers[body.Name] = body.Subject
		if _, ok := b.queues[body.Name]; !ok {
			b.queues[body.Name] = nil
		}
		b.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("POST /v1/events", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Subject string          `json:"subject"`
			Data    json.RawMessage `json:"data"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		b.mu.Lock()
		b.seq++
		baseID := strconv.Itoa(b.seq)
		for name, subject := range b.consumers {
			if subject != body.Subject {
				continue
			}
			msg := deliveredMessage{
				EventID:  "evt-" + baseID + "-" + name,
				Subject:  body.Subject,
				AckToken: "ack-" + baseID + "-" + name,
				Data:     body.Data,
			}
			b.queues[name] = append(b.queues[name], msg)
		}
		seq := b.seq
		b.mu.Unlock()
		writeJSON(w, http.StatusAccepted, map[string]any{"event_id": "evt-" + baseID, "seq": seq})
	})
	mux.HandleFunc("POST /v1/consume", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Consumer string `json:"consumer"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		b.mu.Lock()
		var msgs []deliveredMessage
		if q := b.queues[body.Consumer]; len(q) > 0 {
			msgs = append([]deliveredMessage{}, q[0])
			b.queues[body.Consumer] = q[1:]
		}
		b.mu.Unlock()
		writeJSON(w, http.StatusOK, consumeResponse{Messages: msgs})
	})
	mux.HandleFunc("POST /v1/processed", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/ack", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/nak", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func TestOrderPlacedDrivesChainToNotified(t *testing.T) {
	broker := newFakeEventsBroker()
	eventsSrv := httptest.NewServer(broker.handler())
	defer eventsSrv.Close()

	// Stand-in fulfillment + notify that react to events.
	var fulfillments []string
	var notifications []string
	var sideMu sync.Mutex

	fulfillEvents := newEventsClient(eventsConfig{
		BaseURL: eventsSrv.URL, Source: "orderpipe-fulfillment",
		PollMS: 50, Batch: 4, AckWaitS: 5, MaxDeliveries: 3,
		ValidateConsumer: "unused-f-v", ChargeConsumer: "unused-f-c",
		FulfilledConsumer: "unused-f-f", NotifiedConsumer: "unused-f-n",
	})
	notifyEvents := newEventsClient(eventsConfig{
		BaseURL: eventsSrv.URL, Source: "orderpipe-notify",
		PollMS: 50, Batch: 4, AckWaitS: 5, MaxDeliveries: 3,
		ValidateConsumer: "unused-n-v", ChargeConsumer: "unused-n-c",
		FulfilledConsumer: "unused-n-f", NotifiedConsumer: "unused-n-n",
	})

	// Register side consumers on the fake broker.
	for _, pair := range []struct {
		name, subject string
	}{
		{"orderpipe-fulfill", subjectCharged},
		{"orderpipe-notify", subjectFulfilled},
	} {
		raw, _ := json.Marshal(map[string]any{"name": pair.name, "subject": pair.subject})
		resp, err := http.Post(eventsSrv.URL+"/v1/consumers", "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	store := newMemoryStore()
	apiEvents := newEventsClient(eventsConfig{
		BaseURL: eventsSrv.URL, Source: "orderpipe-api",
		PollMS: 50, Batch: 4, AckWaitS: 5, MaxDeliveries: 3,
		ValidateConsumer:  "orderpipe-validate",
		ChargeConsumer:    "orderpipe-charge",
		FulfilledConsumer: "orderpipe-mark-fulfilled",
		NotifiedConsumer:  "orderpipe-mark-notified",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := apiEvents.EnsureConsumers(ctx); err != nil {
		t.Fatalf("ensure consumers: %v", err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	newChoreography(store, apiEvents).Start(runCtx)

	// Fulfillment reactor: order.charged → record + order.fulfilled
	go func() {
		for {
			select {
			case <-runCtx.Done():
				return
			default:
			}
			cctx, ccancel := context.WithTimeout(runCtx, 2*time.Second)
			msgs, err := fulfillEvents.Consume(cctx, "orderpipe-fulfill")
			ccancel()
			if err != nil {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			for _, msg := range msgs {
				var data orderEventData
				_ = json.Unmarshal(msg.Data, &data)
				sideMu.Lock()
				fulfillments = append(fulfillments, data.OrderID)
				sideMu.Unlock()
				order := &Order{
					ID: data.OrderID, CustomerEmail: data.CustomerEmail,
					Status: "fulfilled", TotalCents: data.TotalCents,
				}
				_ = fulfillEvents.PublishOrderEvent(runCtx, subjectFulfilled, order)
				_ = fulfillEvents.MarkProcessed(runCtx, "orderpipe-fulfill", msg.EventID)
				_ = fulfillEvents.Ack(runCtx, msg.AckToken)
			}
			if len(msgs) == 0 {
				time.Sleep(20 * time.Millisecond)
			}
		}
	}()

	// Notify reactor: order.fulfilled → record + order.notified
	go func() {
		for {
			select {
			case <-runCtx.Done():
				return
			default:
			}
			cctx, ccancel := context.WithTimeout(runCtx, 2*time.Second)
			msgs, err := notifyEvents.Consume(cctx, "orderpipe-notify")
			ccancel()
			if err != nil {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			for _, msg := range msgs {
				var data orderEventData
				_ = json.Unmarshal(msg.Data, &data)
				sideMu.Lock()
				notifications = append(notifications, data.OrderID)
				sideMu.Unlock()
				order := &Order{
					ID: data.OrderID, CustomerEmail: data.CustomerEmail,
					Status: "notified", TotalCents: data.TotalCents,
				}
				_ = notifyEvents.PublishOrderEvent(runCtx, subjectNotified, order)
				_ = notifyEvents.MarkProcessed(runCtx, "orderpipe-notify", msg.EventID)
				_ = notifyEvents.Ack(runCtx, msg.AckToken)
			}
			if len(msgs) == 0 {
				time.Sleep(20 * time.Millisecond)
			}
		}
	}()

	srv := newServer(store, nil, apiEvents)
	handler := srv.routes()
	body := bytes.NewBufferString(`{"customerEmail":"buyer@example.com","items":[{"sku":"mug","qty":1}]}`)
	req := httptest.NewRequest(http.MethodPost, "/orders", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("place status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created Order
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var got *Order
	for time.Now().Before(deadline) {
		o, err := store.GetOrder(context.Background(), created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if o != nil && o.Status == "notified" {
			got = o
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if got == nil || got.Status != "notified" {
		t.Fatalf("status did not reach notified; last=%+v", got)
	}

	steps := map[string]bool{}
	for _, ev := range got.SagaEvents {
		if ev.Outcome == "ok" {
			steps[ev.Step] = true
		}
	}
	for _, step := range []string{"place", "validate", "charge", "fulfill", "notify"} {
		if !steps[step] {
			t.Fatalf("missing saga step %s in %+v", step, got.SagaEvents)
		}
	}

	sideMu.Lock()
	defer sideMu.Unlock()
	if len(fulfillments) == 0 || fulfillments[0] != created.ID {
		t.Fatalf("fulfillment not reacted: %v", fulfillments)
	}
	if len(notifications) == 0 || notifications[0] != created.ID {
		t.Fatalf("notify not reacted: %v", notifications)
	}
}
