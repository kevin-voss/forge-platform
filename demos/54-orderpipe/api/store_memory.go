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
	mu      sync.Mutex
	catalog map[string]CatalogItem
	orders  map[string]*Order
}

func newMemoryStore() *memoryStore {
	now := time.Now().UTC()
	return &memoryStore{
		catalog: map[string]CatalogItem{
			"mug":   {SKU: "mug", Name: "Forge Mug", UnitCents: 1800, CreatedAt: now},
			"shirt": {SKU: "shirt", Name: "Forge Tee", UnitCents: 2800, CreatedAt: now},
		},
		orders: map[string]*Order{},
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
	return &cp, nil
}
