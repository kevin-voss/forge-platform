package alerts_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forge.local/services/forge-observe/internal/alerts"
	"forge.local/services/forge-observe/internal/api"
	"forge.local/services/forge-observe/internal/identity"
)

func TestNormalizePromAlertsShape(t *testing.T) {
	am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/-/healthy" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))
	defer am.Close()

	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/alerts" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"alerts": []map[string]any{
					{
						"labels": map[string]string{
							"alertname":     "ServiceDown",
							"severity":      "critical",
							"service_name":  "demo",
							"forge_service": "demo",
							"secret_token":  "should-drop",
						},
						"state":    "firing",
						"activeAt": time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC),
						"value":    "0",
					},
					{
						"labels": map[string]string{
							"alertname":     "HighErrorRate",
							"severity":      "warning",
							"forge_project": "prj_1",
							"service_name":  "api",
						},
						"state":    "pending",
						"activeAt": time.Date(2026, 7, 23, 10, 1, 0, 0, time.UTC),
						"value":    "0.12",
					},
					{
						"labels": map[string]string{"alertname": "Other"},
						"state":  "inactive",
						"value":  "1",
					},
				},
			},
		})
	}))
	defer prom.Close()

	client := &alerts.StatusClient{
		AlertmanagerURL: am.URL,
		PrometheusURL:   prom.URL,
		Timeout:         time.Second,
	}
	got, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d want 2: %#v", len(got), got)
	}
	if got[0].Name != "HighErrorRate" || got[0].State != "pending" {
		t.Fatalf("first=%+v", got[0])
	}
	if got[0].Labels["forge_project"] != "prj_1" {
		t.Fatalf("labels=%v", got[0].Labels)
	}
	if _, ok := got[0].Labels["secret_token"]; ok {
		t.Fatal("secret label must be stripped")
	}
	if got[1].Name != "ServiceDown" || got[1].State != "firing" {
		t.Fatalf("second=%+v", got[1])
	}
	if _, ok := got[1].Labels["secret_token"]; ok {
		t.Fatal("secret label must be stripped")
	}
}

func TestListAlertmanagerDownReturnsUnavailable(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"alerts": []any{}},
		})
	}))
	defer prom.Close()

	client := &alerts.StatusClient{
		AlertmanagerURL: "http://127.0.0.1:1",
		PrometheusURL:   prom.URL,
		Timeout:         200 * time.Millisecond,
	}
	_, err := client.List(context.Background())
	if !errors.Is(err, alerts.ErrUnavailable) {
		t.Fatalf("err=%v want ErrUnavailable", err)
	}
}

func TestAlertsHandler503WhenBackendDown(t *testing.T) {
	client := &alerts.StatusClient{
		AlertmanagerURL: "http://127.0.0.1:1",
		PrometheusURL:   "http://127.0.0.1:1",
		Timeout:         100 * time.Millisecond,
	}
	h := &api.AlertsHandler{
		Client: client,
		Auth:   &identity.Gate{Mode: identity.AuthModeDev},
	}
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/alerts", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "alerting_unavailable") {
		t.Fatalf("body=%s", rr.Body.String())
	}
}

func TestAlertsHandlerOK(t *testing.T) {
	am := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))
	defer am.Close()
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"alerts": []map[string]any{
					{
						"labels":   map[string]string{"alertname": "ServiceDown", "severity": "critical"},
						"state":    "firing",
						"activeAt": time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC),
						"value":    "0",
					},
				},
			},
		})
	}))
	defer prom.Close()

	h := &api.AlertsHandler{
		Client: &alerts.StatusClient{AlertmanagerURL: am.URL, PrometheusURL: prom.URL, Timeout: time.Second},
		Auth:   &identity.Gate{Mode: identity.AuthModeDev},
	}
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/v1/alerts", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got []alerts.Alert
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(got) != 1 || got[0].Name != "ServiceDown" || got[0].State != "firing" {
		t.Fatalf("got=%+v", got)
	}
}

func TestFilterByProjects(t *testing.T) {
	in := []alerts.Alert{
		{Name: "ServiceDown", Labels: map[string]string{}},
		{Name: "HighErrorRate", Labels: map[string]string{"forge_project": "prj_1"}},
		{Name: "HighErrorRate", Labels: map[string]string{"forge_project": "prj_2"}},
	}
	got := alerts.FilterByProjects(in, map[string]struct{}{"prj_1": {}})
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	if got[0].Name != "ServiceDown" || got[1].Labels["forge_project"] != "prj_1" {
		t.Fatalf("got=%+v", got)
	}
}

func TestNormalizeAlertmanagerWebhook(t *testing.T) {
	payload := []byte(`{"status":"firing","alerts":[{"labels":{"alertname":"ServiceDown"}},{"labels":{"alertname":"HighErrorRate"}}]}`)
	status, names, err := alerts.NormalizeAlertmanagerWebhook(payload)
	if err != nil {
		t.Fatal(err)
	}
	if status != "firing" {
		t.Fatalf("status=%q", status)
	}
	if len(names) != 2 || names[0] != "HighErrorRate" || names[1] != "ServiceDown" {
		t.Fatalf("names=%v", names)
	}
}
