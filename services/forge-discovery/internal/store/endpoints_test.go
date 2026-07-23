package store

import (
	"context"
	"os"
	"testing"
	"time"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("FORGE_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		dsn = "postgres://forge:forge@127.0.0.1:5001/forge?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	db, err := Open(ctx, dsn, "discovery", 4, true)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func cleanupEndpoint(t *testing.T, db *DB, id string) {
	t.Helper()
	_, _ = db.Pool.Exec(context.Background(), `DELETE FROM discovery.endpoints WHERE id = $1`, id)
}

func TestRegisterIdempotentSameReplica(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	id := "test-replica-idempotent-1"
	cleanupEndpoint(t, db, id)
	t.Cleanup(func() { cleanupEndpoint(t, db, id) })

	in := RegisterInput{
		ID: id, Project: "demo", Environment: "local", Service: "demo-echo",
		NodeID: "node-a", AddressIP: "172.20.0.10", AddressPort: 8080,
		Protocol: "http", LeaseSeconds: 20, Now: time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC),
	}
	a, err := db.Register(ctx, in)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	b, err := db.Register(ctx, in)
	if err != nil {
		t.Fatalf("reregister: %v", err)
	}
	if a.ResourceVersion != b.ResourceVersion {
		t.Fatalf("resource_version changed on identical repeat: %q → %q", a.ResourceVersion, b.ResourceVersion)
	}
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM discovery.endpoints WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("duplicate rows: %d", n)
	}
}

func TestRenewResetsExpiryAndPhase(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	id := "test-replica-renew-1"
	cleanupEndpoint(t, db, id)
	t.Cleanup(func() { cleanupEndpoint(t, db, id) })

	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	_, err := db.Register(ctx, RegisterInput{
		ID: id, Project: "demo", Environment: "local", Service: "demo-echo",
		NodeID: "node-a", AddressIP: "172.20.0.10", AddressPort: 8080,
		LeaseSeconds: 20, Now: now,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	renewAt := now.Add(10 * time.Second)
	row, err := db.Renew(ctx, RenewInput{
		Project: "demo", Environment: "local", ID: id,
		Ready: true, LeaseSeconds: 20, Now: renewAt,
	})
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if row.Phase != "Ready" || !row.Ready {
		t.Fatalf("phase/ready = %s/%v", row.Phase, row.Ready)
	}
	wantExp := renewAt.Add(20 * time.Second)
	if !row.ExpiresAt.Equal(wantExp) {
		t.Fatalf("expires_at = %v want %v", row.ExpiresAt, wantExp)
	}

	unready, err := db.Renew(ctx, RenewInput{
		Project: "demo", Environment: "local", ID: id,
		Ready: false, LeaseSeconds: 20, Now: renewAt.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("renew unready: %v", err)
	}
	if unready.Phase != "Unready" {
		t.Fatalf("phase = %s", unready.Phase)
	}
}

func TestExpireLeasesOncePastExpiresAt(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	id := "test-replica-expire-1"
	cleanupEndpoint(t, db, id)
	t.Cleanup(func() { cleanupEndpoint(t, db, id) })

	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	_, err := db.Register(ctx, RegisterInput{
		ID: id, Project: "demo", Environment: "local", Service: "demo-echo",
		NodeID: "node-a", AddressIP: "172.20.0.10", AddressPort: 8080,
		LeaseSeconds: 20, Now: now,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	_, _ = db.Renew(ctx, RenewInput{
		Project: "demo", Environment: "local", ID: id, Ready: true, LeaseSeconds: 20, Now: now,
	})

	past := now.Add(21 * time.Second)
	ids, err := db.ExpireLeases(ctx, past)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if len(ids) != 1 || ids[0] != id {
		t.Fatalf("expired ids = %v", ids)
	}
	row, err := db.GetEndpoint(ctx, "demo", "local", id)
	if err != nil {
		t.Fatal(err)
	}
	if row.Phase != "Unready" || row.UnreadyReason == nil || *row.UnreadyReason != "LeaseExpired" {
		t.Fatalf("row = %+v", row)
	}
	ids2, err := db.ExpireLeases(ctx, past.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range ids2 {
		if got == id {
			t.Fatal("expired same endpoint twice")
		}
	}
}

func TestMarkNodeUnreadyScopedToNode(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	a := "test-replica-node-a"
	b := "test-replica-node-b"
	cleanupEndpoint(t, db, a)
	cleanupEndpoint(t, db, b)
	t.Cleanup(func() {
		cleanupEndpoint(t, db, a)
		cleanupEndpoint(t, db, b)
	})

	now := time.Now().UTC()
	for _, in := range []RegisterInput{
		{ID: a, Project: "demo", Environment: "local", Service: "demo-echo", NodeID: "node-a", AddressIP: "10.0.0.1", AddressPort: 8080, LeaseSeconds: 60, Now: now},
		{ID: b, Project: "demo", Environment: "local", Service: "demo-echo", NodeID: "node-b", AddressIP: "10.0.0.2", AddressPort: 8080, LeaseSeconds: 60, Now: now},
	} {
		if _, err := db.Register(ctx, in); err != nil {
			t.Fatalf("register: %v", err)
		}
		if _, err := db.Renew(ctx, RenewInput{Project: "demo", Environment: "local", ID: in.ID, Ready: true, LeaseSeconds: 60, Now: now}); err != nil {
			t.Fatalf("renew: %v", err)
		}
	}

	n, err := db.MarkNodeUnready(ctx, "node-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("affected = %d", n)
	}
	ra, _ := db.GetEndpoint(ctx, "demo", "local", a)
	rb, _ := db.GetEndpoint(ctx, "demo", "local", b)
	if ra.Phase != "Unready" || ra.UnreadyReason == nil || *ra.UnreadyReason != "NodeUnreachable" {
		t.Fatalf("node-a endpoint = %+v", ra)
	}
	if rb.Phase != "Ready" {
		t.Fatalf("node-b endpoint unexpectedly %s", rb.Phase)
	}
}

func TestLifecycleRegisterRenewDeregister(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	id := "test-replica-lifecycle-1"
	cleanupEndpoint(t, db, id)
	t.Cleanup(func() { cleanupEndpoint(t, db, id) })

	now := time.Now().UTC()
	if _, err := db.Register(ctx, RegisterInput{
		ID: id, Project: "demo", Environment: "local", Service: "demo-echo",
		NodeID: "node-a", AddressIP: "172.20.0.10", AddressPort: 8080, LeaseSeconds: 20, Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := db.Renew(ctx, RenewInput{
			Project: "demo", Environment: "local", ID: id, Ready: true, LeaseSeconds: 20, Now: now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Deregister(ctx, "demo", "local", id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetEndpoint(ctx, "demo", "local", id); err != ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestReregisterNoDuplicate(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	id := "test-replica-restart-1"
	cleanupEndpoint(t, db, id)
	t.Cleanup(func() { cleanupEndpoint(t, db, id) })

	now := time.Now().UTC()
	in := RegisterInput{
		ID: id, Project: "demo", Environment: "local", Service: "demo-echo",
		NodeID: "node-a", AddressIP: "172.20.0.10", AddressPort: 8080, LeaseSeconds: 20, Now: now,
	}
	if _, err := db.Register(ctx, in); err != nil {
		t.Fatal(err)
	}
	// Simulate restart: same id again.
	if _, err := db.Register(ctx, in); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM discovery.endpoints WHERE id = $1`, id).Scan(&n)
	if n != 1 {
		t.Fatalf("want 1 row, got %d", n)
	}
}
