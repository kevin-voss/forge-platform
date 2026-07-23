package policy_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"forge.local/services/forge-autoscaler/internal/policy"
)

func testDB(t *testing.T) *policy.DB {
	t.Helper()
	dsn := os.Getenv("FORGE_AUTOSCALER_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("FORGE_DATABASE_URL")
	}
	if dsn == "" {
		dsn = "postgres://forge:forge@127.0.0.1:5001/forge_autoscaler?sslmode=disable"
	}
	db, err := policy.Open(context.Background(), dsn, 4, true)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func uniqueName(prefix string) string {
	return prefix + "_" + policy.FormatRV(int64(os.Getpid())) + "_" + policy.FormatRV(int64(len(prefix)*97+int(os.Getpid()%1000)))
}

func TestStoreCRUDIdempotencyAndConflict(t *testing.T) {
	db := testDB(t)
	hub := policy.NewHub(db.Pool, 10)
	store := &policy.Store{Pool: db.Pool, Hub: hub}
	ctx := context.Background()

	name := uniqueName("policy")
	util := 65.0
	spec := policy.ScalingPolicySpec{
		TargetRef:   policy.TargetRef{Kind: "Application", Name: "invoice-api"},
		MinReplicas: 2,
		MaxReplicas: 20,
		Metrics:     []policy.MetricSpec{{Type: "cpu", TargetAverageUtilization: &util}},
		Behavior: policy.Behavior{
			ScaleUp:   policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 4},
			ScaleDown: policy.ScaleBehavior{StabilizationWindowSeconds: 300, MaxReplicasPerMinute: 2},
		},
		Schedules: []policy.Schedule{},
	}
	body, _ := json.Marshal(map[string]any{
		"metadata": map[string]string{"name": name},
		"spec":     spec,
	})

	env1, status, err := store.Create(ctx, "invoice-platform", "production", name, spec, "idem-"+name, string(body))
	if err != nil || status != 201 {
		t.Fatalf("create: status=%d err=%v", status, err)
	}
	envReplay, status, err := store.Create(ctx, "invoice-platform", "production", name, spec, "idem-"+name, string(body))
	if err != nil || status != 201 {
		t.Fatalf("idempotent replay: status=%d err=%v", status, err)
	}
	if envReplay.Metadata.ID != env1.Metadata.ID {
		t.Fatalf("idempotent create returned different id")
	}

	otherBody := string(body) + " "
	_, _, err = store.Create(ctx, "invoice-platform", "production", name+"-x", spec, "idem-"+name, otherBody)
	if !errors.Is(err, policy.ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}

	_, err = store.ReplaceSpec(ctx, "invoice-platform", "production", name, 999999, spec)
	if !errors.Is(err, policy.ErrConflict) {
		t.Fatalf("expected resource_version_conflict, got %v", err)
	}

	rv, _ := policy.ParseRV(env1.Metadata.ResourceVersion)
	spec.MaxReplicas = 25
	updated, err := store.ReplaceSpec(ctx, "invoice-platform", "production", name, rv, spec)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Spec.MaxReplicas != 25 || updated.Metadata.Generation != 2 {
		t.Fatalf("unexpected update: %+v", updated)
	}

	_ = store.Delete(ctx, "invoice-platform", "production", name)
}
