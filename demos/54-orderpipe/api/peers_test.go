package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestServiceFromDiscoveryHost(t *testing.T) {
	cases := []struct {
		host string
		want string
		ok   bool
	}{
		{host: "fulfillment.svc.forge", want: "fulfillment", ok: true},
		{host: "notify.local.orderpipe.svc.forge", want: "notify", ok: true},
		{host: "fulfillment.orderpipe.localhost", ok: false},
		{host: "172.17.0.2", ok: false},
		{host: "fulfillment", ok: false},
	}
	for _, tc := range cases {
		got, err := serviceFromDiscoveryHost(tc.host)
		if tc.ok {
			if err != nil {
				t.Fatalf("host %q: unexpected err %v", tc.host, err)
			}
			if got != tc.want {
				t.Fatalf("host %q: got %q want %q", tc.host, got, tc.want)
			}
			continue
		}
		if err == nil {
			t.Fatalf("host %q: expected error", tc.host)
		}
	}
}

func TestDiscoveryPeersResolveAndPost(t *testing.T) {
	var gotHost string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		if r.URL.Path != "/fulfill" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer peer.Close()

	peerURL, err := http.NewRequest(http.MethodGet, peer.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	ip := peerURL.URL.Hostname()
	port := peerURL.URL.Port()
	if port == "" {
		t.Fatal("peer server missing port")
	}
	portNum, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}

	disc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/orderpipe/environments/local/services/fulfillment/endpoints" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"ready": true,
				"phase": "Ready",
				"address": map[string]any{
					"ip":   ip,
					"port": portNum,
				},
			},
		})
	}))
	defer disc.Close()

	p := newDiscoveryPeers(peerConfig{
		FulfillmentURL: "http://fulfillment.svc.forge:8080",
		NotifyURL:      "http://notify.svc.forge:8080",
		DiscoveryURL:   disc.URL,
		Project:        "orderpipe",
		Environment:    "local",
	})
	if err := p.Fulfill(context.Background(), "ord-1"); err != nil {
		t.Fatalf("Fulfill: %v", err)
	}
	if gotHost != "fulfillment.svc.forge:8080" {
		t.Fatalf("Host = %q, want fulfillment.svc.forge:8080", gotHost)
	}
}

func TestPlaceOrderDoesNotCallPeersSync(t *testing.T) {
	// 54.04: placement publishes order.placed; fulfillment/notify react via events.
	calls := 0
	peers := &stubPeers{onFulfill: func(string) error {
		calls++
		return nil
	}, onNotify: func(string, string) error {
		calls++
		return nil
	}}
	srv := newServer(newMemoryStore(), peers, nil)
	req := httptest.NewRequest(http.MethodPost, "/orders",
		bytes.NewBufferString(`{"customerEmail":"buyer@example.com","items":[{"sku":"mug","qty":1}]}`))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if calls != 0 {
		t.Fatalf("peer calls = %d, want 0 (async events own the chain)", calls)
	}
}

type stubPeers struct {
	onFulfill func(string) error
	onNotify  func(string, string) error
}

func (s *stubPeers) Fulfill(_ context.Context, orderID string) error {
	return s.onFulfill(orderID)
}

func (s *stubPeers) Notify(_ context.Context, orderID, email string) error {
	return s.onNotify(orderID, email)
}
