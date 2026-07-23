package operations_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"forge.local/services/forge-infrastructure/internal/operations"
)

func TestLedgerBeginIdempotent(t *testing.T) {
	dsn := os.Getenv("FORGE_INFRA_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("FORGE_DATABASE_URL")
	}
	if dsn == "" {
		dsn = "postgres://forge:forge@127.0.0.1:5001/forge?sslmode=disable"
	}

	ctx := context.Background()
	db, err := operations.Open(ctx, dsn, "infrastructure", 2, true)
	if err != nil {
		t.Skipf("postgres not available: %v", err)
	}
	defer db.Close()

	naturalKey := fmt.Sprintf("idem-pool-%d#0", time.Now().UnixNano())
	_, _ = db.Pool.Exec(ctx, `DELETE FROM infrastructure.provider_operations WHERE natural_key = $1`, naturalKey)

	ledger := &operations.Ledger{Pool: db.Pool, IDs: operations.NewGenerator(), Schema: "infrastructure"}
	req := map[string]any{"name": "n1"}

	first, err := ledger.Begin(ctx, "docker-local", operations.KindCreateNode, operations.TargetNode, naturalKey, req)
	if err != nil {
		t.Fatalf("first Begin: %v", err)
	}
	if first.AlreadyExists || first.SkipProvider || first.Op == nil || first.Op.ID == "" {
		t.Fatalf("unexpected first Begin: %+v", first)
	}

	second, err := ledger.Begin(ctx, "docker-local", operations.KindCreateNode, operations.TargetNode, naturalKey, req)
	if err != nil {
		t.Fatalf("second Begin: %v", err)
	}
	if !second.AlreadyExists || !second.SkipProvider {
		t.Fatalf("second Begin should reuse pending row: %+v", second)
	}
	if second.Op.ID != first.Op.ID {
		t.Fatalf("ids differ: %s vs %s", first.Op.ID, second.Op.ID)
	}

	var count int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM infrastructure.provider_operations WHERE provider_name=$1 AND natural_key=$2`,
		"docker-local", naturalKey,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
}
