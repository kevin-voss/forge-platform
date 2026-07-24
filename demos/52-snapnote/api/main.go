package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	addr := listenAddr()
	migrationsDir := resolveMigrationsDir(os.Getenv("MIGRATIONS_DIR"))
	databaseURL := os.Getenv("DATABASE_URL")

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
	log.Printf("snapnote-api migrations applied from %s", migrationsDir)

	storageCfg := loadStorageConfig()
	storage := newStorageClient(storageCfg)
	if err := ensureStorageReady(storage, 60*time.Second); err != nil {
		log.Fatalf("storage: %v", err)
	}
	log.Printf(
		"snapnote-api storage ready bucket=%s project=%s url=%s public=%s",
		storageCfg.Bucket, storageCfg.ProjectID, storageCfg.BaseURL, storageCfg.PublicURL,
	)

	eventsCfg := loadEventsConfig()
	events := newEventsClient(eventsCfg)
	if err := ensureEventsReady(events, 60*time.Second); err != nil {
		log.Fatalf("events: %v", err)
	}
	log.Printf(
		"snapnote-api events ready subject=%s url=%s",
		eventsCfg.Subject, eventsCfg.BaseURL,
	)

	srv := newServer(store, storage, events)
	log.Printf("snapnote-api listening on %s", addr)
	if err := http.ListenAndServe(addr, srv.routes()); err != nil {
		log.Fatal(err)
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

func ensureStorageReady(storage *storageClient, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	var last error
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := storage.Ping(ctx)
		if err == nil {
			err = storage.EnsureBucket(ctx)
		}
		cancel()
		if err == nil {
			return nil
		}
		last = err
		if time.Now().After(deadline) {
			return last
		}
		log.Printf("waiting for forge-storage: %v", err)
		time.Sleep(2 * time.Second)
	}
}

func ensureEventsReady(events *eventsClient, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	var last error
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := events.Ping(ctx)
		cancel()
		if err == nil {
			return nil
		}
		last = err
		if time.Now().After(deadline) {
			return last
		}
		log.Printf("waiting for forge-events: %v", err)
		time.Sleep(2 * time.Second)
	}
}
