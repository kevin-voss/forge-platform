package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

func main() {
	addr := listenAddr()
	databaseURL := os.Getenv("DATABASE_URL")

	store, err := openStoreWithRetry(databaseURL, 60*time.Second)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer func() { _ = store.Close() }()

	storageCfg := loadStorageConfig()
	storage := newStorageClient(storageCfg)
	if err := waitPing(storage.Ping, 60*time.Second, "forge-storage"); err != nil {
		log.Fatalf("storage: %v", err)
	}

	eventsCfg := loadEventsConfig()
	events := newEventsClient(eventsCfg)
	if err := waitPing(events.Ping, 60*time.Second, "forge-events"); err != nil {
		log.Fatalf("events: %v", err)
	}
	if err := waitEnsureConsumer(events, 60*time.Second); err != nil {
		log.Fatalf("consumer: %v", err)
	}

	handler := newJobHandler(store, storage)
	var ready atomic.Bool
	ready.Store(true)
	var processed atomic.Int64

	go runConsumeLoop(events, handler, &processed)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := store.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "error": "database"})
			return
		}
		if err := storage.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "error": "storage"})
			return
		}
		if err := events.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "error": "events"})
			return
		}
		if !ready.Load() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":          "ok",
			"processed_count": processed.Load(),
			"consumer":        eventsCfg.Consumer,
			"subject":         eventsCfg.Subject,
		})
	})

	log.Printf(
		"snapnote-worker listening on %s consumer=%s subject=%s bucket=%s",
		addr, eventsCfg.Consumer, eventsCfg.Subject, storageCfg.Bucket,
	)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func runConsumeLoop(events *eventsClient, handler *jobHandler, processed *atomic.Int64) {
	poll := time.Duration(events.cfg.PollMS) * time.Millisecond
	if poll < 100*time.Millisecond {
		poll = 100 * time.Millisecond
	}
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		msgs, err := events.Consume(ctx)
		if err != nil {
			cancel()
			log.Printf("consume: %v", err)
			time.Sleep(poll)
			continue
		}
		for _, msg := range msgs {
			if err := handleMessage(ctx, events, handler, msg); err == nil {
				processed.Add(1)
			}
		}
		cancel()
		if len(msgs) == 0 {
			time.Sleep(poll)
		}
	}
}

func listenAddr() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return ":" + port
}

func openStoreWithRetry(databaseURL string, budget time.Duration) (*pgStore, error) {
	deadline := time.Now().Add(budget)
	var last error
	for {
		store, err := openStore(databaseURL)
		if err == nil {
			return store, nil
		}
		last = err
		if time.Now().After(deadline) {
			return nil, last
		}
		log.Printf("waiting for DATABASE_URL / postgres: %v", err)
		time.Sleep(2 * time.Second)
	}
}

func waitPing(fn func(context.Context) error, budget time.Duration, label string) error {
	deadline := time.Now().Add(budget)
	var last error
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := fn(ctx)
		cancel()
		if err == nil {
			return nil
		}
		last = err
		if time.Now().After(deadline) {
			return last
		}
		log.Printf("waiting for %s: %v", label, err)
		time.Sleep(2 * time.Second)
	}
}

func waitEnsureConsumer(events *eventsClient, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	var last error
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := events.EnsureConsumer(ctx)
		cancel()
		if err == nil {
			return nil
		}
		last = err
		if time.Now().After(deadline) {
			return last
		}
		log.Printf("waiting to create consumer: %v", err)
		time.Sleep(2 * time.Second)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
