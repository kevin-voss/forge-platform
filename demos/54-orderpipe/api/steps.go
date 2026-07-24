package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// stepHandlers implement order-saga step + compensate actions (Workflow resource
// actions orderpipe.validate / .charge / .refund). Exposed over HTTP under /saga/*.
type stepHandlers struct {
	store  OrderStore
	events *eventsClient
	pspKey string
}

func newStepHandlers(store OrderStore, events *eventsClient, pspKey string) *stepHandlers {
	return &stepHandlers{store: store, events: events, pspKey: strings.TrimSpace(pspKey)}
}

func (h *stepHandlers) Validate(ctx context.Context, orderID string) error {
	order, err := h.store.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if order == nil {
		return fmt.Errorf("order not found")
	}
	if order.CustomerEmail == "" || len(order.Items) == 0 {
		return fmt.Errorf("order failed validation")
	}
	if _, err := h.store.AdvanceStatus(ctx, orderID, "validated"); err != nil {
		return err
	}
	if err := h.appendOkOnce(ctx, orderID, "validate"); err != nil {
		return err
	}
	order.Status = "validated"
	if h.events != nil && h.events.enabled() {
		if err := h.events.PublishOrderEvent(ctx, subjectValidated, order); err != nil {
			return err
		}
	}
	return nil
}

func (h *stepHandlers) Charge(ctx context.Context, orderID string, attempt int, decline bool) error {
	_ = attempt
	order, err := h.store.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if order == nil {
		return fmt.Errorf("order not found")
	}
	if decline || order.DeclineCharge {
		return errors.New("payment declined")
	}
	if h.pspKey == "" {
		return errors.New("PSP_API_KEY missing (Secrets injection required)")
	}
	// Mock PSP: key present → charge succeeds (idempotent side effect via status).
	if _, err := h.store.AdvanceStatus(ctx, orderID, "charged"); err != nil {
		return err
	}
	if err := h.appendOkOnce(ctx, orderID, "charge"); err != nil {
		return err
	}
	order.Status = "charged"
	if h.events != nil && h.events.enabled() {
		if err := h.events.PublishOrderEvent(ctx, subjectCharged, order); err != nil {
			return err
		}
	}
	return nil
}

func (h *stepHandlers) Refund(ctx context.Context, orderID string) error {
	order, err := h.store.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if order == nil {
		return fmt.Errorf("order not found")
	}
	// Never leave a half-fulfilled order after charge failure.
	if statusRank(order.Status) >= statusRank("fulfilled") && order.Status != "refunded" && order.Status != "failed" {
		return fmt.Errorf("refuse refund of fulfilled order %s (status=%s)", orderID, order.Status)
	}
	if _, err := h.store.AdvanceStatus(ctx, orderID, "refunded"); err != nil {
		return err
	}
	events, err := h.store.ListSagaEvents(ctx, orderID)
	if err != nil {
		return err
	}
	if !hasSagaOutcome(events, "charge", "compensated") {
		if _, err := h.store.AppendSagaEvent(ctx, orderID, "charge", "compensated"); err != nil {
			return err
		}
	}
	return nil
}

func (h *stepHandlers) appendOkOnce(ctx context.Context, orderID, step string) error {
	events, err := h.store.ListSagaEvents(ctx, orderID)
	if err != nil {
		return err
	}
	if hasSagaOutcome(events, step, "ok") {
		return nil
	}
	_, err = h.store.AppendSagaEvent(ctx, orderID, step, "ok")
	return err
}
