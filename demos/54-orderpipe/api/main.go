package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	addr := listenAddr()
	migrationsDir := resolveMigrationsDir(os.Getenv("MIGRATIONS_DIR"))
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		log.Fatal("config: DATABASE_URL is required (inject via managed-db attach)")
	}

	store, err := openStoreWithRetry(databaseURL, migrationsDir, 60*time.Second)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := store.Migrate(ctx); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Printf("orderpipe-api migrations applied from %s", migrationsDir)

	peers := newDiscoveryPeers(loadPeerConfig())
	log.Printf("orderpipe-api peers fulfillment=%s notify=%s discovery=%s project=%s/%s",
		peers.cfg.FulfillmentURL, peers.cfg.NotifyURL, peers.cfg.DiscoveryURL,
		peers.cfg.Project, peers.cfg.Environment)

	eventsCfg := loadEventsConfig()
	events := newEventsClient(eventsCfg)
	if err := waitPing(events.Ping, 60*time.Second, "forge-events"); err != nil {
		log.Fatalf("forge-events: %v", err)
	}
	if err := waitEnsureConsumers(events, 60*time.Second); err != nil {
		log.Fatalf("events consumers: %v", err)
	}
	log.Printf("orderpipe-api events url=%s source=%s", eventsCfg.BaseURL, eventsCfg.Source)
	newChoreography(store, events).Start(context.Background())

	srv := newServer(store, peers, events)
	log.Printf("orderpipe-api listening on %s", addr)
	if err := http.ListenAndServe(addr, srv.routes()); err != nil {
		log.Fatal(err)
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

func waitEnsureConsumers(events *eventsClient, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	var last error
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := events.EnsureConsumers(ctx)
		cancel()
		if err == nil {
			return nil
		}
		last = err
		if time.Now().After(deadline) {
			return last
		}
		log.Printf("waiting to create order consumers: %v", err)
		time.Sleep(2 * time.Second)
	}
}

func listenAddr() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return ":" + port
}

func openStoreWithRetry(databaseURL, migrationsDir string, budget time.Duration) (*pgStore, error) {
	deadline := time.Now().Add(budget)
	var last error
	for {
		store, err := openStore(databaseURL, migrationsDir)
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
