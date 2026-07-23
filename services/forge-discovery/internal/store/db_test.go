package store

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestMigrateAppliesServicesAndEndpoints(t *testing.T) {
	dsn := os.Getenv("FORGE_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		dsn = "postgres://forge:forge@127.0.0.1:5001/forge?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := Open(ctx, dsn, "discovery", 4, true)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer db.Close()

	// Re-run is idempotent.
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var services, endpoints int
	if err := db.Pool.QueryRow(ctx, `
SELECT
  (SELECT COUNT(*) FROM information_schema.tables
     WHERE table_schema = 'discovery' AND table_name = 'services'),
  (SELECT COUNT(*) FROM information_schema.tables
     WHERE table_schema = 'discovery' AND table_name = 'endpoints')
`).Scan(&services, &endpoints); err != nil {
		t.Fatalf("query tables: %v", err)
	}
	if services != 1 || endpoints != 1 {
		t.Fatalf("tables missing: services=%d endpoints=%d", services, endpoints)
	}
}
