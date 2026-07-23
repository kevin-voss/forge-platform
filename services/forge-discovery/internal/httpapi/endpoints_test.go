package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"forge.local/services/forge-discovery/internal/store"
)

type memEndpointStore struct {
	row   store.EndpointRow
	exist bool
}

func (m *memEndpointStore) Register(_ context.Context, in store.RegisterInput) (store.EndpointRow, error) {
	m.exist = true
	m.row = store.EndpointRow{
		ID: in.ID, Project: in.Project, Environment: in.Environment, Service: in.Service,
		NodeID: in.NodeID, AddressIP: in.AddressIP, AddressPort: in.AddressPort,
		Protocol: in.Protocol, Phase: "Pending", LeaseSeconds: in.LeaseSeconds,
		ExpiresAt:       time.Now().UTC().Add(time.Duration(in.LeaseSeconds) * time.Second),
		ResourceVersion: "1",
	}
	return m.row, nil
}

func (m *memEndpointStore) Renew(_ context.Context, in store.RenewInput) (store.EndpointRow, error) {
	if !m.exist || m.row.ID != in.ID {
		return store.EndpointRow{}, store.ErrNotFound
	}
	phase := "Ready"
	if !in.Ready {
		phase = "Unready"
	}
	m.row.Ready = in.Ready
	m.row.Phase = phase
	m.row.ExpiresAt = time.Now().UTC().Add(time.Duration(in.LeaseSeconds) * time.Second)
	return m.row, nil
}

func (m *memEndpointStore) Deregister(_ context.Context, _, _, id string) error {
	if !m.exist || m.row.ID != id {
		return store.ErrNotFound
	}
	m.exist = false
	return nil
}

func (m *memEndpointStore) ListServiceEndpoints(_ context.Context, _, _, _ string) ([]store.EndpointRow, error) {
	if !m.exist {
		return nil, nil
	}
	return []store.EndpointRow{m.row}, nil
}

func TestRegisterRenewDeregisterHTTP(t *testing.T) {
	st := &memEndpointStore{}
	h := &EndpointsHandler{Store: st, DefaultLease: 20}
	mux := NewRouterWith(RouterDeps{Ready: NewReadiness(nil), Endpoints: h})

	body := []byte(`{"id":"ep-1","node":"node-a","address":{"ip":"10.0.0.1","port":8080},"leaseSeconds":20}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/projects/demo/environments/local/services/demo-echo/endpoints", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("register status %d body %s", rr.Code, rr.Body.String())
	}
	var reg registerResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &reg); err != nil {
		t.Fatal(err)
	}
	if reg.ID != "ep-1" || reg.Phase != "Pending" {
		t.Fatalf("reg = %+v", reg)
	}

	renewBody := []byte(`{"ready":true,"leaseSeconds":20}`)
	req = httptest.NewRequest(http.MethodPost, "/v1/projects/demo/environments/local/endpoints/ep-1/renew", bytes.NewReader(renewBody))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("renew status %d", rr.Code)
	}
	var ren renewResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &ren)
	if ren.Phase != "Ready" {
		t.Fatalf("phase = %s", ren.Phase)
	}

	req = httptest.NewRequest(http.MethodDelete, "/v1/projects/demo/environments/local/endpoints/ep-1", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete status %d", rr.Code)
	}
}
