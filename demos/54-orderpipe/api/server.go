package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type server struct {
	store OrderStore
	peers PeerCaller
}

func newServer(store OrderStore, peers PeerCaller) *server {
	return &server{store: store, peers: peers}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.handleLive)
	mux.HandleFunc("GET /health/ready", s.handleReady)
	mux.HandleFunc("GET /", s.handleRoot)
	mux.HandleFunc("GET /catalog", s.handleCatalog)
	mux.HandleFunc("POST /orders", s.handlePlaceOrder)
	mux.HandleFunc("GET /orders/{id}", s.handleGetOrder)
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
		"orders":   "POST /orders (peers via fulfillment/notify.svc.forge; saga in 54.04/54.05)",
		"catalog":  "GET /catalog",
		"peers":    "FULFILLMENT_URL / NOTIFY_URL → Discovery Ready endpoints",
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
	order, err := s.store.PlaceOrder(r.Context(), email, req.Items)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "required") || strings.Contains(msg, "unknown sku") || strings.Contains(msg, "qty") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "place order failed"})
		return
	}
	if s.peers != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		if err := s.peers.Fulfill(ctx, order.ID); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error":   "fulfillment peer call failed",
				"detail":  err.Error(),
				"orderId": order.ID,
			})
			return
		}
		if err := s.peers.Notify(ctx, order.ID, email); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error":   "notify peer call failed",
				"detail":  err.Error(),
				"orderId": order.ID,
			})
			return
		}
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
