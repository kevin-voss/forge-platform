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
	"forge.local/services/forge-discovery/internal/watchhub"
)

type memEndpointStore struct {
	rows     map[string]store.EndpointRow
	services []store.ServiceRow
}

func newMemStore() *memEndpointStore {
	return &memEndpointStore{rows: map[string]store.EndpointRow{}}
}

func (m *memEndpointStore) ListServices(_ context.Context) ([]store.ServiceRow, error) {
	out := make([]store.ServiceRow, len(m.services))
	copy(out, m.services)
	return out, nil
}

func (m *memEndpointStore) Register(ctx context.Context, in store.RegisterInput) (store.EndpointRow, error) {
	row, _, err := m.RegisterWithAction(ctx, in)
	return row, err
}

func (m *memEndpointStore) RegisterWithAction(_ context.Context, in store.RegisterInput) (store.EndpointRow, store.RegisterAction, error) {
	if m.rows == nil {
		m.rows = map[string]store.EndpointRow{}
	}
	if existing, ok := m.rows[in.ID]; ok {
		identical := existing.Project == in.Project && existing.Environment == in.Environment &&
			existing.Service == in.Service && existing.NodeID == in.NodeID &&
			existing.AddressIP == in.AddressIP && existing.AddressPort == in.AddressPort &&
			existing.Revision == in.Revision
		if identical {
			existing.ExpiresAt = time.Now().UTC().Add(time.Duration(in.LeaseSeconds) * time.Second)
			m.rows[in.ID] = existing
			return existing, store.RegisterUnchanged, nil
		}
		existing.NodeID = in.NodeID
		existing.AddressIP = in.AddressIP
		existing.AddressPort = in.AddressPort
		existing.Revision = in.Revision
		existing.Phase = "Pending"
		existing.Ready = false
		existing.ResourceVersion = "2"
		m.rows[in.ID] = existing
		return existing, store.RegisterUpdated, nil
	}
	row := store.EndpointRow{
		ID: in.ID, Project: in.Project, Environment: in.Environment, Service: in.Service,
		NodeID: in.NodeID, AddressIP: in.AddressIP, AddressPort: in.AddressPort,
		Protocol: in.Protocol, Revision: in.Revision, Phase: "Pending", LeaseSeconds: in.LeaseSeconds,
		ExpiresAt:       time.Now().UTC().Add(time.Duration(in.LeaseSeconds) * time.Second),
		ResourceVersion: "1",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
	m.rows[in.ID] = row
	return row, store.RegisterCreated, nil
}

func (m *memEndpointStore) Renew(_ context.Context, in store.RenewInput) (store.EndpointRow, error) {
	row, ok := m.rows[in.ID]
	if !ok || row.Project != in.Project || row.Environment != in.Environment {
		return store.EndpointRow{}, store.ErrNotFound
	}
	phase := "Ready"
	if !in.Ready {
		phase = "Unready"
	}
	row.Ready = in.Ready
	row.Phase = phase
	row.ExpiresAt = time.Now().UTC().Add(time.Duration(in.LeaseSeconds) * time.Second)
	row.ResourceVersion = "2"
	m.rows[in.ID] = row
	return row, nil
}

func (m *memEndpointStore) Deregister(ctx context.Context, project, environment, id string) error {
	_, err := m.DeregisterReturning(ctx, project, environment, id)
	return err
}

func (m *memEndpointStore) DeregisterReturning(_ context.Context, project, environment, id string) (store.EndpointRow, error) {
	row, ok := m.rows[id]
	if !ok || row.Project != project || row.Environment != environment {
		return store.EndpointRow{}, store.ErrNotFound
	}
	delete(m.rows, id)
	return row, nil
}

func (m *memEndpointStore) ListServiceEndpoints(_ context.Context, project, environment, service string) ([]store.EndpointRow, error) {
	var out []store.EndpointRow
	for _, row := range m.rows {
		if row.Project == project && row.Environment == environment && row.Service == service {
			out = append(out, row)
		}
	}
	return out, nil
}

func (m *memEndpointStore) ListEndpoints(_ context.Context, f store.ListFilter) ([]store.EndpointRow, error) {
	var out []store.EndpointRow
	for _, row := range m.rows {
		if row.Project != f.Project || row.Environment != f.Environment || row.Service != f.Service {
			continue
		}
		if f.ReadyOnly && row.Phase != "Ready" {
			continue
		}
		if f.Revision != "" && row.Revision != f.Revision {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

func TestRegisterRenewDeregisterHTTP(t *testing.T) {
	st := newMemStore()
	h := &EndpointsHandler{Store: st, DefaultLease: 20, Watch: watchhub.New(watchhub.Config{})}
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

func TestListReadyOnlyDefaultAndRevisionFilter(t *testing.T) {
	st := newMemStore()
	now := time.Now().UTC()
	st.rows["a"] = store.EndpointRow{
		ID: "a", Project: "demo", Environment: "local", Service: "svc",
		Phase: "Ready", Ready: true, Revision: "v2", NodeID: "n1",
		AddressIP: "10.0.0.1", AddressPort: 8080, ExpiresAt: now, ResourceVersion: "2",
	}
	st.rows["b"] = store.EndpointRow{
		ID: "b", Project: "demo", Environment: "local", Service: "svc",
		Phase: "Unready", Ready: false, Revision: "v2", NodeID: "n1",
		AddressIP: "10.0.0.2", AddressPort: 8080, ExpiresAt: now, ResourceVersion: "3",
	}
	st.rows["c"] = store.EndpointRow{
		ID: "c", Project: "demo", Environment: "local", Service: "svc",
		Phase: "Pending", Ready: false, Revision: "v1", NodeID: "n1",
		AddressIP: "10.0.0.3", AddressPort: 8080, ExpiresAt: now, ResourceVersion: "1",
	}
	st.rows["d"] = store.EndpointRow{
		ID: "d", Project: "demo", Environment: "local", Service: "svc",
		Phase: "Ready", Ready: true, Revision: "v1", NodeID: "n1",
		AddressIP: "10.0.0.4", AddressPort: 8080, ExpiresAt: now, ResourceVersion: "4",
	}
	st.rows["other"] = store.EndpointRow{
		ID: "other", Project: "other", Environment: "local", Service: "svc",
		Phase: "Ready", Ready: true, Revision: "v2", NodeID: "n1",
		AddressIP: "10.0.0.9", AddressPort: 8080, ExpiresAt: now, ResourceVersion: "1",
	}

	h := &EndpointsHandler{Store: st, Watch: watchhub.New(watchhub.Config{})}
	mux := NewRouterWith(RouterDeps{Ready: NewReadiness(nil), Endpoints: h})

	req := httptest.NewRequest(http.MethodGet, "/v1/projects/demo/environments/local/services/svc/endpoints", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var ready []endpointListItem
	if err := json.Unmarshal(rr.Body.Bytes(), &ready); err != nil {
		t.Fatal(err)
	}
	if len(ready) != 2 {
		t.Fatalf("ready-only default len=%d body=%s", len(ready), rr.Body.String())
	}
	for _, item := range ready {
		if item.Phase != "Ready" {
			t.Fatalf("non-ready leaked: %+v", item)
		}
		if item.ID == "other" {
			t.Fatal("cross-project leak")
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/projects/demo/environments/local/services/svc/endpoints?ready=false", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	var all []endpointListItem
	_ = json.Unmarshal(rr.Body.Bytes(), &all)
	if len(all) != 4 {
		t.Fatalf("ready=false len=%d", len(all))
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/projects/demo/environments/local/services/svc/endpoints?revision=v2", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	var rev []endpointListItem
	_ = json.Unmarshal(rr.Body.Bytes(), &rev)
	if len(rev) != 1 || rev[0].ID != "a" {
		t.Fatalf("revision filter = %+v", rev)
	}
}

func TestWatchReplayAndResync(t *testing.T) {
	broker := watchhub.New(watchhub.Config{BufferSize: 3, MaxConnections: 10})
	st := newMemStore()
	st.rows["ready-1"] = store.EndpointRow{
		ID: "ready-1", Project: "demo", Environment: "local", Service: "svc",
		Phase: "Ready", Ready: true, NodeID: "n", AddressIP: "10.0.0.1", AddressPort: 1,
		ExpiresAt: time.Now().UTC(), ResourceVersion: "9",
	}
	h := &EndpointsHandler{Store: st, Watch: broker, WatchHeartbeat: time.Hour}
	mux := NewRouterWith(RouterDeps{Ready: NewReadiness(nil), Endpoints: h})

	for i := 0; i < 5; i++ {
		broker.Publish(watchhub.Event{
			Type: watchhub.EventUpdated, Project: "demo", Environment: "local", Service: "svc",
			Payload: watchhub.EndpointPayload{ID: "e"},
		})
	}
	// Buffer has RV 3,4,5. since=1 → miss → resync emits Ready as added.
	req := httptest.NewRequest(http.MethodGet, "/v1/projects/demo/environments/local/services/svc/endpoints/watch?since=1", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req = req.WithContext(ctx)
	rr := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(rr, req)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	body := rr.Body.String()
	if !containsAll(body, "event: added", `"id":"ready-1"`) {
		t.Fatalf("resync body = %q", body)
	}

	// Within-buffer replay: publish then watch since last-1.
	broker2 := watchhub.New(watchhub.Config{BufferSize: 10})
	h.Watch = broker2
	mux = NewRouterWith(RouterDeps{Ready: NewReadiness(nil), Endpoints: h})
	broker2.Publish(watchhub.Event{Type: watchhub.EventAdded, Project: "demo", Environment: "local", Service: "svc", Payload: watchhub.EndpointPayload{ID: "x1"}})
	broker2.Publish(watchhub.Event{Type: watchhub.EventUpdated, Project: "demo", Environment: "local", Service: "svc", Payload: watchhub.EndpointPayload{ID: "x2"}})
	broker2.Publish(watchhub.Event{Type: watchhub.EventRemoved, Project: "demo", Environment: "local", Service: "svc", Payload: watchhub.EndpointPayload{ID: "x3"}})

	req = httptest.NewRequest(http.MethodGet, "/v1/projects/demo/environments/local/services/svc/endpoints/watch?since=1", nil)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	req = req.WithContext(ctx)
	rr = &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	done = make(chan struct{})
	go func() {
		mux.ServeHTTP(rr, req)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	body = rr.Body.String()
	if !containsAll(body, "event: updated", "event: removed", `"id":"x2"`, `"id":"x3"`) {
		t.Fatalf("replay body = %q", body)
	}
	if containsAll(body, `"id":"x1"`) {
		// x1 has RV=1; since=1 means rv>1, so x1 must not appear.
		t.Fatalf("replay included since-boundary event: %q", body)
	}
}

func TestListServicesHTTP(t *testing.T) {
	st := newMemStore()
	st.services = []store.ServiceRow{{
		Project: "invoice-platform", Environment: "production", Name: "invoice-api",
		Aliases: []string{"legacy-invoice"},
	}}
	h := &EndpointsHandler{Store: st, Watch: watchhub.New(watchhub.Config{})}
	mux := NewRouterWith(RouterDeps{Ready: NewReadiness(nil), Endpoints: h})

	req := httptest.NewRequest(http.MethodGet, "/v1/services", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var got []serviceListItem
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "invoice-api" || got[0].Project != "invoice-platform" {
		t.Fatalf("got=%+v", got)
	}
	if len(got[0].Aliases) != 1 || got[0].Aliases[0] != "legacy-invoice" {
		t.Fatalf("aliases=%v", got[0].Aliases)
	}
}

func containsAll(s string, needles ...string) bool {
	for _, n := range needles {
		if !bytes.Contains([]byte(s), []byte(n)) {
			return false
		}
	}
	return true
}

// flushRecorder wraps httptest.ResponseRecorder with http.Flusher for SSE tests.
type flushRecorder struct {
	*httptest.ResponseRecorder
}

func (f *flushRecorder) Flush() {}
