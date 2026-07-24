package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type server struct {
	store  OrderStore
	peers  PeerCaller
	events *eventsClient
	saga   *sagaRunner
}

func newServer(store OrderStore, peers PeerCaller, events *eventsClient, saga *sagaRunner) *server {
	return &server{store: store, peers: peers, events: events, saga: saga}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.handleLive)
	mux.HandleFunc("GET /health/ready", s.handleReady)
	mux.HandleFunc("GET /", s.handleRoot)
	mux.HandleFunc("GET /catalog", s.handleCatalog)
	mux.HandleFunc("POST /orders", s.handlePlaceOrder)
	mux.HandleFunc("GET /orders/{id}", s.handleGetOrder)
	mux.HandleFunc("POST /saga/validate", s.handleSagaValidate)
	mux.HandleFunc("POST /saga/charge", s.handleSagaCharge)
	mux.HandleFunc("POST /saga/refund", s.handleSagaRefund)
	return withCORS(mux)
}

func (s *server) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "not_ready",
			"error":  "database unavailable",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service":  "orderpipe-api",
		"language": "go",
		"status":   "running",
		"orders":   "POST /orders → order.placed + order-saga (validate→charge; fulfill/notify via events)",
		"catalog":  "GET /catalog",
		"saga":     "POST /saga/validate|charge|refund (Workflow step handlers)",
		"events":   "order.placed/validated/charged/fulfilled/notified",
		"peers":    "FULFILLMENT_URL / NOTIFY_URL → Discovery Ready endpoints (HTTP retained for policy proofs)",
	})
}

func (s *server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListCatalog(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list catalog failed"})
		return
	}
	if items == nil {
		items = []CatalogItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type placeOrderRequest struct {
	CustomerEmail string           `json:"customerEmail"`
	Email         string           `json:"email"`
	Items         []PlaceOrderItem `json:"items"`
	DeclineCharge bool             `json:"declineCharge"`
}

func (s *server) handlePlaceOrder(w http.ResponseWriter, r *http.Request) {
	var req placeOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	email := strings.TrimSpace(req.CustomerEmail)
	if email == "" {
		email = strings.TrimSpace(req.Email)
	}
	order, err := s.store.PlaceOrder(r.Context(), email, req.Items, req.DeclineCharge)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "required") || strings.Contains(msg, "unknown sku") || strings.Contains(msg, "qty") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "place order failed"})
		return
	}
	if _, err := s.store.AppendSagaEvent(r.Context(), order.ID, "place", "ok"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "saga audit failed"})
		return
	}
	if s.events != nil && s.events.enabled() {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		if err := s.events.PublishOrderEvent(ctx, subjectPlaced, order); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error":   "events publish failed",
				"detail":  err.Error(),
				"orderId": order.ID,
			})
			return
		}
	}
	if s.saga != nil {
		s.saga.Start(context.Background(), order.ID)
	}
	// Reload so sagaEvents is present on the create response.
	if refreshed, err := s.store.GetOrder(r.Context(), order.ID); err == nil && refreshed != nil {
		order = refreshed
	}
	writeJSON(w, http.StatusCreated, order)
}

func (s *server) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	order, err := s.store.GetOrder(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "get order failed"})
		return
	}
	if order == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, order)
}

type sagaStepRequest struct {
	OrderID string `json:"orderId"`
	Attempt int    `json:"attempt"`
}

func (s *server) handleSagaValidate(w http.ResponseWriter, r *http.Request) {
	s.runSagaHTTP(w, r, func(ctx context.Context, orderID string, _ int) error {
		if s.saga == nil || s.saga.handlers == nil {
			return errSagaUnavailable
		}
		return s.saga.handlers.Validate(ctx, orderID)
	})
}

func (s *server) handleSagaCharge(w http.ResponseWriter, r *http.Request) {
	s.runSagaHTTP(w, r, func(ctx context.Context, orderID string, attempt int) error {
		if s.saga == nil || s.saga.handlers == nil {
			return errSagaUnavailable
		}
		if attempt < 1 {
			attempt = 1
		}
		order, err := s.store.GetOrder(ctx, orderID)
		if err != nil {
			return err
		}
		if order == nil {
			return errOrderNotFound
		}
		return s.saga.handlers.Charge(ctx, orderID, attempt, order.DeclineCharge)
	})
}

func (s *server) handleSagaRefund(w http.ResponseWriter, r *http.Request) {
	s.runSagaHTTP(w, r, func(ctx context.Context, orderID string, _ int) error {
		if s.saga == nil || s.saga.handlers == nil {
			return errSagaUnavailable
		}
		return s.saga.handlers.Refund(ctx, orderID)
	})
}

var (
	errSagaUnavailable = &sagaHTTPError{status: http.StatusServiceUnavailable, msg: "saga handlers unavailable"}
	errOrderNotFound   = &sagaHTTPError{status: http.StatusNotFound, msg: "order not found"}
)

type sagaHTTPError struct {
	status int
	msg    string
}

func (e *sagaHTTPError) Error() string { return e.msg }

func (s *server) runSagaHTTP(w http.ResponseWriter, r *http.Request, fn func(context.Context, string, int) error) {
	var req sagaStepRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	orderID := strings.TrimSpace(req.OrderID)
	if orderID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "orderId is required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := fn(ctx, orderID, req.Attempt); err != nil {
		var he *sagaHTTPError
		if ok := errorAs(err, &he); ok {
			writeJSON(w, he.status, map[string]string{"error": he.msg})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	order, err := s.store.GetOrder(ctx, orderID)
	if err != nil || order == nil {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true", "orderId": orderID})
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func errorAs(err error, target **sagaHTTPError) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*sagaHTTPError); ok {
		*target = e
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
