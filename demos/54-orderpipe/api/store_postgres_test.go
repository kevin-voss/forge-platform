package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestOrderStorePostgresPlaceOrder(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("ORDERPIPE_TEST_DATABASE_URL"))
	var cleanup func()
	if dsn == "" {
		var err error
		dsn, cleanup, err = startPostgresContainer(t)
		if err != nil {
			t.Skipf("postgres test container unavailable: %v", err)
		}
		if cleanup != nil {
			t.Cleanup(cleanup)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	store, err := openStore(dsn, resolveMigrationsDir(""))
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	_, err = store.db.ExecContext(ctx, `
		INSERT INTO catalog_items (sku, name, unit_cents)
		VALUES ('mug', 'Forge Mug', 1800)
		ON CONFLICT (sku) DO UPDATE SET name = EXCLUDED.name, unit_cents = EXCLUDED.unit_cents
	`)
	if err != nil {
		t.Fatalf("seed catalog: %v", err)
	}

	created, err := store.PlaceOrder(ctx, "buyer@example.com", []PlaceOrderItem{{SKU: "mug", Qty: 1}}, false)
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	if created.ID == "" || created.Status != "placed" || created.TotalCents != 1800 {
		t.Fatalf("unexpected create: %+v", created)
	}

	got, err := store.GetOrder(ctx, created.ID)
	if err != nil || got == nil || got.CustomerEmail != "buyer@example.com" {
		t.Fatalf("get: got=%+v err=%v", got, err)
	}
	if len(got.Items) != 1 || got.Items[0].SKU != "mug" {
		t.Fatalf("items: %+v", got.Items)
	}

	catalog, err := store.ListCatalog(ctx)
	if err != nil || len(catalog) == 0 {
		t.Fatalf("catalog: %+v err=%v", catalog, err)
	}
}

func startPostgresContainer(t *testing.T) (string, func(), error) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		return "", nil, fmt.Errorf("docker not on PATH")
	}
	name := fmt.Sprintf("orderpipe-pg-test-%d", time.Now().UnixNano())
	run := exec.Command(
		"docker", "run", "-d", "--rm",
		"--name", name,
		"-e", "POSTGRES_PASSWORD=test",
		"-e", "POSTGRES_USER=test",
		"-e", "POSTGRES_DB=orderpipe",
		"-p", "127.0.0.1::5432",
		"postgres:16-alpine",
	)
	out, err := run.CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("docker run: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	id := strings.TrimSpace(string(out))
	cleanup := func() {
		_ = exec.Command("docker", "rm", "-f", id).Run()
	}

	portOut, err := exec.Command("docker", "port", id, "5432/tcp").CombinedOutput()
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("docker port: %w (%s)", err, strings.TrimSpace(string(portOut)))
	}
	// "127.0.0.1:54321"
	hostPort := strings.TrimSpace(string(portOut))
	parts := strings.Split(hostPort, ":")
	port := parts[len(parts)-1]
	dsn := fmt.Sprintf("postgres://test:test@127.0.0.1:%s/orderpipe?sslmode=disable", port)

	deadline := time.Now().Add(30 * time.Second)
	for {
		store, err := openStore(dsn, resolveMigrationsDir(""))
		if err == nil {
			_ = store.Close()
			return dsn, cleanup, nil
		}
		if time.Now().After(deadline) {
			cleanup()
			return "", nil, fmt.Errorf("postgres never ready: %w", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
