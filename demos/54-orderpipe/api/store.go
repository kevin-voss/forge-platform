package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type CatalogItem struct {
	SKU       string    `json:"sku"`
	Name      string    `json:"name"`
	UnitCents int       `json:"unitCents"`
	CreatedAt time.Time `json:"createdAt"`
}

type OrderItem struct {
	ID        string `json:"id"`
	SKU       string `json:"sku"`
	Qty       int    `json:"qty"`
	UnitCents int    `json:"unitCents"`
}

type Order struct {
	ID            string      `json:"id"`
	CustomerEmail string      `json:"customerEmail"`
	Status        string      `json:"status"`
	TotalCents    int         `json:"totalCents"`
	Items         []OrderItem `json:"items"`
	SagaEvents    []SagaEvent `json:"sagaEvents,omitempty"`
	CreatedAt     time.Time   `json:"createdAt"`
	UpdatedAt     time.Time   `json:"updatedAt"`
}

type SagaEvent struct {
	ID      string    `json:"id"`
	OrderID string    `json:"orderId"`
	Step    string    `json:"step"`
	Outcome string    `json:"outcome"`
	At      time.Time `json:"at"`
}

type PlaceOrderItem struct {
	SKU string `json:"sku"`
	Qty int    `json:"qty"`
}

type OrderStore interface {
	Migrate(ctx context.Context) error
	Ping(ctx context.Context) error
	Close() error
	ListCatalog(ctx context.Context) ([]CatalogItem, error)
	PlaceOrder(ctx context.Context, email string, items []PlaceOrderItem) (*Order, error)
	GetOrder(ctx context.Context, id string) (*Order, error)
	AdvanceStatus(ctx context.Context, id, status string) (*Order, error)
	AppendSagaEvent(ctx context.Context, orderID, step, outcome string) (*SagaEvent, error)
	ListSagaEvents(ctx context.Context, orderID string) ([]SagaEvent, error)
}

// statusRank is monotonic for the happy-path choreography (54.04).
func statusRank(status string) int {
	switch status {
	case "placed":
		return 1
	case "validated":
		return 2
	case "charged":
		return 3
	case "fulfilled":
		return 4
	case "notified":
		return 5
	case "failed", "refunded":
		return 100
	default:
		return 0
	}
}

type pgStore struct {
	db            *sql.DB
	migrationsDir string
}

func openStore(databaseURL, migrationsDir string) (*pgStore, error) {
	url := strings.TrimSpace(databaseURL)
	if url == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	if strings.Contains(url, "postgres:5432/forge") || strings.Contains(url, ":5001/forge") {
		return nil, errors.New("refusing Control database URL")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &pgStore{db: db, migrationsDir: migrationsDir}, nil
}

func (s *pgStore) Close() error { return s.db.Close() }

func (s *pgStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *pgStore) Migrate(ctx context.Context) error {
	return applyMigrations(ctx, s.db, s.migrationsDir)
}

func (s *pgStore) ListCatalog(ctx context.Context) ([]CatalogItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sku, name, unit_cents, created_at
		FROM catalog_items
		ORDER BY sku ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CatalogItem
	for rows.Next() {
		var item CatalogItem
		if err := rows.Scan(&item.SKU, &item.Name, &item.UnitCents, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *pgStore) PlaceOrder(ctx context.Context, email string, items []PlaceOrderItem) (*Order, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return nil, errors.New("customer email is required")
	}
	if len(items) == 0 {
		return nil, errors.New("at least one item is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	type resolved struct {
		sku, name string
		qty       int
		unit      int
	}
	resolvedItems := make([]resolved, 0, len(items))
	total := 0
	for _, it := range items {
		sku := strings.TrimSpace(it.SKU)
		if sku == "" || it.Qty <= 0 {
			return nil, errors.New("each item needs sku and qty > 0")
		}
		var name string
		var unit int
		err := tx.QueryRowContext(ctx, `
			SELECT name, unit_cents FROM catalog_items WHERE sku = $1
		`, sku).Scan(&name, &unit)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("unknown sku: %s", sku)
		}
		if err != nil {
			return nil, err
		}
		resolvedItems = append(resolvedItems, resolved{sku: sku, name: name, qty: it.Qty, unit: unit})
		total += unit * it.Qty
	}

	orderID := newID("ord")
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO orders (id, customer_email, status, total_cents, created_at, updated_at)
		VALUES ($1, $2, 'placed', $3, $4, $4)
	`, orderID, email, total, now)
	if err != nil {
		return nil, fmt.Errorf("insert order: %w", err)
	}

	orderItems := make([]OrderItem, 0, len(resolvedItems))
	for _, it := range resolvedItems {
		itemID := newID("oli")
		_, err = tx.ExecContext(ctx, `
			INSERT INTO order_items (id, order_id, sku, qty, unit_cents)
			VALUES ($1, $2, $3, $4, $5)
		`, itemID, orderID, it.sku, it.qty, it.unit)
		if err != nil {
			return nil, fmt.Errorf("insert order item: %w", err)
		}
		orderItems = append(orderItems, OrderItem{
			ID: itemID, SKU: it.sku, Qty: it.qty, UnitCents: it.unit,
		})
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Order{
		ID:            orderID,
		CustomerEmail: email,
		Status:        "placed",
		TotalCents:    total,
		Items:         orderItems,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

func (s *pgStore) GetOrder(ctx context.Context, id string) (*Order, error) {
	var o Order
	err := s.db.QueryRowContext(ctx, `
		SELECT id, customer_email, status, total_cents, created_at, updated_at
		FROM orders WHERE id = $1
	`, id).Scan(&o.ID, &o.CustomerEmail, &o.Status, &o.TotalCents, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, sku, qty, unit_cents FROM order_items WHERE order_id = $1 ORDER BY sku
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it OrderItem
		if err := rows.Scan(&it.ID, &it.SKU, &it.Qty, &it.UnitCents); err != nil {
			return nil, err
		}
		o.Items = append(o.Items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	events, err := s.ListSagaEvents(ctx, id)
	if err != nil {
		return nil, err
	}
	o.SagaEvents = events
	return &o, nil
}

func (s *pgStore) AdvanceStatus(ctx context.Context, id, status string) (*Order, error) {
	status = strings.TrimSpace(status)
	if statusRank(status) == 0 {
		return nil, fmt.Errorf("unknown status %q", status)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var current string
	err = tx.QueryRowContext(ctx, `SELECT status FROM orders WHERE id = $1 FOR UPDATE`, id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if statusRank(status) > statusRank(current) {
		now := time.Now().UTC()
		_, err = tx.ExecContext(ctx, `
			UPDATE orders SET status = $2, updated_at = $3 WHERE id = $1
		`, id, status, now)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetOrder(ctx, id)
}

func (s *pgStore) AppendSagaEvent(ctx context.Context, orderID, step, outcome string) (*SagaEvent, error) {
	orderID = strings.TrimSpace(orderID)
	step = strings.TrimSpace(step)
	outcome = strings.TrimSpace(outcome)
	if orderID == "" || step == "" {
		return nil, errors.New("order_id and step are required")
	}
	if outcome != "ok" && outcome != "retry" && outcome != "compensated" {
		return nil, fmt.Errorf("invalid outcome %q", outcome)
	}
	ev := &SagaEvent{
		ID:      newID("sge"),
		OrderID: orderID,
		Step:    step,
		Outcome: outcome,
		At:      time.Now().UTC(),
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO saga_events (id, order_id, step, outcome, at)
		VALUES ($1, $2, $3, $4, $5)
	`, ev.ID, ev.OrderID, ev.Step, ev.Outcome, ev.At)
	if err != nil {
		return nil, err
	}
	return ev, nil
}

func (s *pgStore) ListSagaEvents(ctx context.Context, orderID string) ([]SagaEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, order_id, step, outcome, at
		FROM saga_events WHERE order_id = $1
		ORDER BY at ASC, id ASC
	`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SagaEvent
	for rows.Next() {
		var ev SagaEvent
		if err := rows.Scan(&ev.ID, &ev.OrderID, &ev.Step, &ev.Outcome, &ev.At); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	if out == nil {
		out = []SagaEvent{}
	}
	return out, rows.Err()
}

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + "-" + hex.EncodeToString(b[:])
}
