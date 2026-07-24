package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthReady(t *testing.T) {
	srv := newServer(newMemoryStore(), nil)
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status = %q, want ok", body["status"])
	}
}

func TestPlaceOrderStub(t *testing.T) {
	srv := newServer(newMemoryStore(), nil)
	handler := srv.routes()

	body := bytes.NewBufferString(`{"customerEmail":"buyer@example.com","items":[{"sku":"mug","qty":2}]}`)
	req := httptest.NewRequest(http.MethodPost, "/orders", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created Order
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ID == "" || created.Status != "placed" || created.TotalCents != 3600 {
		t.Fatalf("unexpected order: %+v", created)
	}
	if len(created.Items) != 1 || created.Items[0].SKU != "mug" || created.Items[0].Qty != 2 {
		t.Fatalf("unexpected items: %+v", created.Items)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/orders/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200", getRec.Code)
	}
}
