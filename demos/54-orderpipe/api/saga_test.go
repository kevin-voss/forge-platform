package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestSagaHappyPathReachesNotified(t *testing.T) {
	t.Setenv("PSP_API_KEY", "test-psp-key")
	t.Setenv("ORDERPIPE_CHARGE_RETRIES", "3")
	t.Setenv("ORDERPIPE_CHARGE_BACKOFF_MS", "1")

	broker := newFakeEventsBroker()
	eventsSrv := httptest.NewServer(broker.handler())
	defer eventsSrv.Close()

	var fulfillments []string
	var notifications []string
	var sideMu sync.Mutex

	fulfillEvents := newEventsClient(eventsConfig{
		BaseURL: eventsSrv.URL, Source: "orderpipe-fulfillment",
		PollMS: 50, Batch: 4, AckWaitS: 5, MaxDeliveries: 3,
		FulfilledConsumer: "unused-f-f", NotifiedConsumer: "unused-f-n",
	})
	notifyEvents := newEventsClient(eventsConfig{
		BaseURL: eventsSrv.URL, Source: "orderpipe-notify",
		PollMS: 50, Batch: 4, AckWaitS: 5, MaxDeliveries: 3,
		FulfilledConsumer: "unused-n-f", NotifiedConsumer: "unused-n-n",
	})
	for _, pair := range []struct{ name, subject string }{
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
	saga := newSagaRunner(store, apiEvents)

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

	srv := newServer(store, nil, apiEvents, saga)
	body := bytes.NewBufferString(`{"customerEmail":"buyer@example.com","items":[{"sku":"mug","qty":1}]}`)
	req := httptest.NewRequest(http.MethodPost, "/orders", body)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
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

func TestSagaDeclinedChargeRetriesThenCompensates(t *testing.T) {
	t.Setenv("PSP_API_KEY", "test-psp-key")
	t.Setenv("ORDERPIPE_CHARGE_RETRIES", "3")
	t.Setenv("ORDERPIPE_CHARGE_BACKOFF_MS", "1")

	store := newMemoryStore()
	saga := newSagaRunner(store, nil)

	order, err := store.PlaceOrder(context.Background(), "buyer@example.com",
		[]PlaceOrderItem{{SKU: "mug", Qty: 1}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendSagaEvent(context.Background(), order.ID, "place", "ok"); err != nil {
		t.Fatal(err)
	}

	if err := saga.Run(context.Background(), order.ID); err != nil {
		t.Fatalf("saga run: %v", err)
	}
	// Idempotent re-run must not duplicate compensation side effects.
	if err := saga.Run(context.Background(), order.ID); err != nil {
		t.Fatalf("saga rerun: %v", err)
	}

	got, err := store.GetOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "refunded" {
		t.Fatalf("status=%q want refunded; saga=%+v", got.Status, got.SagaEvents)
	}
	retries := countSagaOutcomes(got.SagaEvents, "charge", "retry")
	if retries != 3 {
		t.Fatalf("charge retries=%d want 3; saga=%+v", retries, got.SagaEvents)
	}
	if !hasSagaOutcome(got.SagaEvents, "charge", "compensated") {
		t.Fatalf("missing charge compensated; saga=%+v", got.SagaEvents)
	}
	if hasSagaOutcome(got.SagaEvents, "fulfill", "ok") || hasSagaOutcome(got.SagaEvents, "notify", "ok") {
		t.Fatalf("fulfill/notify must not run after decline; saga=%+v", got.SagaEvents)
	}
	compensated := countSagaOutcomes(got.SagaEvents, "charge", "compensated")
	if compensated != 1 {
		t.Fatalf("compensated count=%d want 1 (idempotent)", compensated)
	}
}

func TestChargeRequiresPSPKey(t *testing.T) {
	_ = os.Unsetenv("PSP_API_KEY")
	t.Setenv("ORDERPIPE_CHARGE_RETRIES", "1")
	t.Setenv("ORDERPIPE_CHARGE_BACKOFF_MS", "0")

	store := newMemoryStore()
	saga := newSagaRunner(store, nil)
	order, err := store.PlaceOrder(context.Background(), "buyer@example.com",
		[]PlaceOrderItem{{SKU: "mug", Qty: 1}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := saga.Run(context.Background(), order.ID); err != nil {
		t.Fatalf("saga run: %v", err)
	}
	got, err := store.GetOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "refunded" {
		t.Fatalf("status=%q want refunded when PSP key missing", got.Status)
	}
}
