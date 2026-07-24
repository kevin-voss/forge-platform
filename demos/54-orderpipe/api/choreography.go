package main

import (
	"context"
	"encoding/json"
	"log"
	"time"
)

// choreography advances order status from forge-events (epic 54.04).
type choreography struct {
	store  OrderStore
	events *eventsClient
}

func newChoreography(store OrderStore, events *eventsClient) *choreography {
	return &choreography{store: store, events: events}
}

func (c *choreography) Start(ctx context.Context) {
	if c == nil || c.events == nil || !c.events.enabled() {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	go c.loop(ctx)
}

func (c *choreography) loop(ctx context.Context) {
	poll := time.Duration(c.events.cfg.PollMS) * time.Millisecond
	if poll < 100*time.Millisecond {
		poll = 100 * time.Millisecond
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		idle := true
		for _, b := range c.events.bindings() {
			reqCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
			msgs, err := c.events.Consume(reqCtx, b.Name)
			if err != nil {
				cancel()
				if ctx.Err() != nil {
					return
				}
				log.Printf("orderpipe choreography consume %s: %v", b.Name, err)
				continue
			}
			for _, msg := range msgs {
				idle = false
				if err := c.handle(reqCtx, b.Name, msg); err != nil {
					log.Printf("orderpipe choreography handle %s/%s: %v", b.Name, msg.EventID, err)
					_ = c.events.Nak(reqCtx, msg.AckToken, 1)
					continue
				}
			}
			cancel()
		}
		if idle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(poll):
			}
		}
	}
}

func (c *choreography) handle(ctx context.Context, consumer string, msg deliveredMessage) error {
	var data orderEventData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		return err
	}
	if data.OrderID == "" {
		return c.ack(ctx, consumer, msg)
	}

	order, err := c.store.GetOrder(ctx, data.OrderID)
	if err != nil {
		return err
	}
	if order == nil {
		// Unknown order — ack to avoid poison loop in demos.
		return c.ack(ctx, consumer, msg)
	}

	switch msg.Subject {
	case subjectPlaced:
		if err := c.advance(ctx, order, "validated", "validate"); err != nil {
			return err
		}
		order.Status = "validated"
		if err := c.events.PublishOrderEvent(ctx, subjectValidated, order); err != nil {
			return err
		}
	case subjectValidated:
		if err := c.advance(ctx, order, "charged", "charge"); err != nil {
			return err
		}
		order.Status = "charged"
		if err := c.events.PublishOrderEvent(ctx, subjectCharged, order); err != nil {
			return err
		}
	case subjectFulfilled:
		if err := c.advance(ctx, order, "fulfilled", "fulfill"); err != nil {
			return err
		}
	case subjectNotified:
		if err := c.advance(ctx, order, "notified", "notify"); err != nil {
			return err
		}
	}

	return c.ack(ctx, consumer, msg)
}

func (c *choreography) advance(ctx context.Context, order *Order, status, step string) error {
	if _, err := c.store.AdvanceStatus(ctx, order.ID, status); err != nil {
		return err
	}
	// Idempotent-ish: skip duplicate saga rows for the same step+ok.
	events, err := c.store.ListSagaEvents(ctx, order.ID)
	if err != nil {
		return err
	}
	for _, ev := range events {
		if ev.Step == step && ev.Outcome == "ok" {
			return nil
		}
	}
	_, err = c.store.AppendSagaEvent(ctx, order.ID, step, "ok")
	return err
}

func (c *choreography) ack(ctx context.Context, consumer string, msg deliveredMessage) error {
	if err := c.events.MarkProcessed(ctx, consumer, msg.EventID); err != nil {
		return err
	}
	return c.events.Ack(ctx, msg.AckToken)
}
