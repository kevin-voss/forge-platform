package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// memoryStore backs health/unit tests without Postgres.
type memoryStore struct {
	mu         sync.Mutex
	catalog    map[string]CatalogItem
	orders     map[string]*Order
	sagaEvents map[string][]SagaEvent
}

func newMemoryStore() *memoryStore {
	now := time.Now().UTC()
	return &memoryStore{
		catalog: map[string]CatalogItem{
			"mug":   {SKU: "mug", Name: "Forge Mug", UnitCents: 1800, CreatedAt: now},
			"shirt": {SKU: "shirt", Name: "Forge Tee", UnitCents: 2800, CreatedAt: now},
		},
		orders:     map[string]*Order{},
		sagaEvents: map[string][]SagaEvent{},
	}
}

func (s *memoryStore) Migrate(context.Context) error { return nil }
func (s *memoryStore) Ping(context.Context) error    { return nil }
func (s *memoryStore) Close() error                  { return nil }

func (s *memoryStore) ListCatalog(context.Context) ([]CatalogItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CatalogItem, 0, len(s.catalog))
	for _, item := range s.catalog {
		out = append(out, item)
	}
	return out, nil
}

func (s *memoryStore) PlaceOrder(_ context.Context, email string, items []PlaceOrderItem) (*Order, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return nil, errors.New("customer email is required")
	}
	if len(items) == 0 {
		return nil, errors.New("at least one item is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	orderItems := make([]OrderItem, 0, len(items))
	total := 0
	for _, it := range items {
		sku := strings.TrimSpace(it.SKU)
		cat, ok := s.catalog[sku]
		if !ok {
			return nil, fmt.Errorf("unknown sku: %s", sku)
		}
		if it.Qty <= 0 {
			return nil, errors.New("each item needs sku and qty > 0")
		}
		total += cat.UnitCents * it.Qty
		orderItems = append(orderItems, OrderItem{
			ID: newID("oli"), SKU: sku, Qty: it.Qty, UnitCents: cat.UnitCents,
		})
	}
	now := time.Now().UTC()
	o := &Order{
		ID:            newID("ord"),
		CustomerEmail: email,
		Status:        "placed",
		TotalCents:    total,
		Items:         orderItems,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	s.orders[o.ID] = o
	return o, nil
}

func (s *memoryStore) GetOrder(_ context.Context, id string) (*Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[id]
	if !ok {
		return nil, nil
	}
	cp := *o
	cp.Items = append([]OrderItem{}, o.Items...)
	cp.SagaEvents = append([]SagaEvent{}, s.sagaEvents[id]...)
	return &cp, nil
}

func (s *memoryStore) AdvanceStatus(_ context.Context, id, status string) (*Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[id]
	if !ok {
		return nil, nil
	}
	if statusRank(status) == 0 {
		return nil, fmt.Errorf("unknown status %q", status)
	}
	if statusRank(status) > statusRank(o.Status) {
		o.Status = status
		o.UpdatedAt = time.Now().UTC()
	}
	cp := *o
	cp.Items = append([]OrderItem{}, o.Items...)
	cp.SagaEvents = append([]SagaEvent{}, s.sagaEvents[id]...)
	return &cp, nil
}

func (s *memoryStore) AppendSagaEvent(_ context.Context, orderID, step, outcome string) (*SagaEvent, error) {
	orderID = strings.TrimSpace(orderID)
	step = strings.TrimSpace(step)
	outcome = strings.TrimSpace(outcome)
	if orderID == "" || step == "" {
		return nil, errors.New("order_id and step are required")
	}
	if outcome != "ok" && outcome != "retry" && outcome != "compensated" {
		return nil, fmt.Errorf("invalid outcome %q", outcome)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.orders[orderID]; !ok {
		return nil, fmt.Errorf("unknown order %s", orderID)
	}
	ev := SagaEvent{
		ID:      newID("sge"),
		OrderID: orderID,
		Step:    step,
		Outcome: outcome,
		At:      time.Now().UTC(),
	}
	s.sagaEvents[orderID] = append(s.sagaEvents[orderID], ev)
	return &ev, nil
}

func (s *memoryStore) ListSagaEvents(_ context.Context, orderID string) ([]SagaEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]SagaEvent{}, s.sagaEvents[orderID]...)
	if out == nil {
		out = []SagaEvent{}
	}
	return out, nil
}
