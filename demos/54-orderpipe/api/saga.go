package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"
)

// sagaRunner drives validate → charge (retry) → emit charged, with refund
// compensation on terminal charge failure (epic 54.05). Fulfill/notify remain
// event-driven (54.04) after a successful charge.
type sagaRunner struct {
	store         OrderStore
	events        *eventsClient
	handlers      *stepHandlers
	chargeRetries int
	backoff       time.Duration
}

func loadSagaConfig() (retries int, backoff time.Duration) {
	retries = envInt("ORDERPIPE_CHARGE_RETRIES", 3)
	if retries < 1 {
		retries = 1
	}
	ms := envInt("ORDERPIPE_CHARGE_BACKOFF_MS", 50)
	if ms < 0 {
		ms = 0
	}
	return retries, time.Duration(ms) * time.Millisecond
}

func newSagaRunner(store OrderStore, events *eventsClient) *sagaRunner {
	retries, backoff := loadSagaConfig()
	return &sagaRunner{
		store:         store,
		events:        events,
		handlers:      newStepHandlers(store, events, strings.TrimSpace(os.Getenv("PSP_API_KEY"))),
		chargeRetries: retries,
		backoff:       backoff,
	}
}

func (s *sagaRunner) Start(ctx context.Context, orderID string) {
	if s == nil || strings.TrimSpace(orderID) == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		if err := s.Run(ctx, orderID); err != nil {
			log.Printf("orderpipe saga %s: %v", orderID, err)
		}
	}()
}

// Run executes the order-saga forward path (and compensation on charge failure).
func (s *sagaRunner) Run(ctx context.Context, orderID string) error {
	order, err := s.store.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if order == nil {
		return nil
	}
	switch order.Status {
	case "notified", "refunded", "failed":
		return nil
	}

	if err := s.stepValidate(ctx, order); err != nil {
		return err
	}
	order, err = s.store.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if order == nil {
		return nil
	}

	charged, err := s.stepChargeWithRetry(ctx, order)
	if err != nil {
		return err
	}
	if !charged {
		return s.compensateCharge(ctx, orderID)
	}
	return nil
}

func (s *sagaRunner) stepValidate(ctx context.Context, order *Order) error {
	if hasSagaOutcome(order.SagaEvents, "validate", "ok") {
		return nil
	}
	if statusRank(order.Status) >= statusRank("validated") {
		return s.appendOnce(ctx, order.ID, "validate", "ok")
	}
	return s.handlers.Validate(ctx, order.ID)
}

func (s *sagaRunner) stepChargeWithRetry(ctx context.Context, order *Order) (bool, error) {
	if hasSagaOutcome(order.SagaEvents, "charge", "ok") {
		return true, nil
	}
	if hasSagaOutcome(order.SagaEvents, "charge", "compensated") {
		return false, nil
	}
	if statusRank(order.Status) >= statusRank("charged") && order.Status != "failed" && order.Status != "refunded" {
		_ = s.appendOnce(ctx, order.ID, "charge", "ok")
		return true, nil
	}

	attempts := countSagaOutcomes(order.SagaEvents, "charge", "retry")
	for attempt := attempts + 1; attempt <= s.chargeRetries; attempt++ {
		err := s.handlers.Charge(ctx, order.ID, attempt, order.DeclineCharge)
		if err == nil {
			return true, nil
		}
		log.Printf("orderpipe saga charge order=%s attempt=%d/%d: %v", order.ID, attempt, s.chargeRetries, err)
		if _, aerr := s.store.AppendSagaEvent(ctx, order.ID, "charge", "retry"); aerr != nil {
			return false, aerr
		}
		if attempt < s.chargeRetries && s.backoff > 0 {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(s.backoff):
			}
		}
	}
	return false, nil
}

func (s *sagaRunner) compensateCharge(ctx context.Context, orderID string) error {
	events, err := s.store.ListSagaEvents(ctx, orderID)
	if err != nil {
		return err
	}
	if hasSagaOutcome(events, "charge", "compensated") {
		return nil
	}
	return s.handlers.Refund(ctx, orderID)
}

func (s *sagaRunner) appendOnce(ctx context.Context, orderID, step, outcome string) error {
	events, err := s.store.ListSagaEvents(ctx, orderID)
	if err != nil {
		return err
	}
	if hasSagaOutcome(events, step, outcome) {
		return nil
	}
	_, err = s.store.AppendSagaEvent(ctx, orderID, step, outcome)
	return err
}

func hasSagaOutcome(events []SagaEvent, step, outcome string) bool {
	for _, ev := range events {
		if ev.Step == step && ev.Outcome == outcome {
			return true
		}
	}
	return false
}

func countSagaOutcomes(events []SagaEvent, step, outcome string) int {
	n := 0
	for _, ev := range events {
		if ev.Step == step && ev.Outcome == outcome {
			n++
		}
	}
	return n
}

func declineFromEmail(email string) bool {
	return strings.Contains(strings.ToLower(email), "+declined@")
}
