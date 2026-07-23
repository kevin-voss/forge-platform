package actuate_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"forge.local/services/forge-autoscaler/internal/actuate"
)

func TestSetDesiredReplicasRetriesConflict(t *testing.T) {
	var gets atomic.Int64
	var patches atomic.Int64
	var rv atomic.Int64
	rv.Store(1)
	var lastOp string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/applications/invoice-api") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
			gets.Add(1)
			writeWorkload(w, "Application", "invoice-api", rv.Load(), int(gets.Load()))
		case http.MethodPatch:
			patches.Add(1)
			lastOp = r.Header.Get("X-Forge-Operation-Id")
			if patches.Load() == 1 {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"conflict"}`))
				rv.Add(1)
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			spec := body["spec"].(map[string]any)
			scaling := spec["scaling"].(map[string]any)
			desired := int(scaling["desiredReplicas"].(float64))
			writeWorkload(w, "Application", "invoice-api", rv.Load()+1, desired)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	client := &actuate.WorkloadClient{BaseURL: srv.URL}
	view, err := client.SetDesiredReplicas(context.Background(), "demo", "production", "Application", "invoice-api", 4, "op-1")
	if err != nil {
		t.Fatalf("SetDesiredReplicas: %v", err)
	}
	if view.DesiredReplicas != 4 {
		t.Fatalf("desired=%d", view.DesiredReplicas)
	}
	if patches.Load() < 2 {
		t.Fatalf("expected conflict retry, patches=%d", patches.Load())
	}
	if lastOp != "op-1" {
		t.Fatalf("operation id not reused: %q", lastOp)
	}
}

func TestWorkerSetDesiredReplicas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/workers/invoice-worker") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeWorkload(w, "Worker", "invoice-worker", 1, 1)
		case http.MethodPatch:
			writeWorkload(w, "Worker", "invoice-worker", 2, 8)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	client := &actuate.WorkloadClient{BaseURL: srv.URL}
	view, err := client.SetDesiredReplicas(context.Background(), "demo", "production", "Worker", "invoice-worker", 8, "op-w")
	if err != nil {
		t.Fatalf("SetDesiredReplicas: %v", err)
	}
	if view.DesiredReplicas != 8 || view.Kind != "Worker" {
		t.Fatalf("view=%+v", view)
	}
}

func writeWorkload(w http.ResponseWriter, kind, name string, resourceVersion int64, desired int) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"apiVersion": "forge.dev/v1",
		"kind":       kind,
		"metadata": map[string]any{
			"name":            name,
			"resourceVersion": resourceVersion,
		},
		"spec": map[string]any{
			"scaling": map[string]any{
				"desiredReplicas": desired,
			},
		},
	})
}
