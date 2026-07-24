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
	cfg := loadConfig()
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
	log.Printf("taskflow-api migrations applied from %s", migrationsDir)

	var identity IdentityClient
	if cfg.ProductAuth == "enforce" || cfg.IdentityURL != "" {
		identity = newHTTPIdentityClient(cfg.IdentityURL)
		log.Printf("taskflow-api identity url=%s product_auth=%s", cfg.IdentityURL, cfg.ProductAuth)
	}

	srv := newServer(store, cfg, identity)
	log.Printf("taskflow-api listening on %s", addr)
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
