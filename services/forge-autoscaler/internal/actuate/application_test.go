package actuate_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		switch r.Method {
		case http.MethodGet:
			gets.Add(1)
			writeApp(w, rv.Load(), int(gets.Load()))
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
			writeApp(w, rv.Load()+1, desired)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	client := &actuate.ApplicationClient{BaseURL: srv.URL}
	view, err := client.SetDesiredReplicas(context.Background(), "demo", "production", "invoice-api", 4, "op-1")
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

func writeApp(w http.ResponseWriter, resourceVersion int64, desired int) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"apiVersion": "forge.dev/v1",
		"kind":       "Application",
		"metadata": map[string]any{
			"name":            "invoice-api",
			"resourceVersion": resourceVersion,
		},
		"spec": map[string]any{
			"scaling": map[string]any{
				"desiredReplicas": desired,
			},
		},
	})
}
