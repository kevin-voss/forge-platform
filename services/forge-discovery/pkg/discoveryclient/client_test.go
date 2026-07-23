package discoveryclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveMatchesList(t *testing.T) {
	eps := []Endpoint{
		{ID: "a", Phase: "Ready", Address: Address{IP: "10.0.0.1", Port: 8080}},
		{ID: "b", Phase: "Ready", Address: Address{IP: "10.0.0.2", Port: 8080}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/demo/environments/local/services/echo/endpoints" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(eps)
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, Project: "demo", Environment: "local", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	addrs, err := c.Resolve(context.Background(), "echo")
	if err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 2 || addrs[0].IP != "10.0.0.1" || addrs[1].Port != 8080 {
		t.Fatalf("addrs = %+v", addrs)
	}
	listed, err := c.List(context.Background(), "echo", ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != len(addrs) {
		t.Fatalf("list/resolve mismatch %d vs %d", len(listed), len(addrs))
	}
}
