package inventory_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"forge.local/services/forge-infrastructure/internal/provider/inventory"
)

type staticLister struct {
	items []inventory.AdmissionResource
}

func (s *staticLister) List(ctx context.Context, plural, labelSelector string) ([]inventory.AdmissionResource, error) {
	return s.items, nil
}

func TestAdmissionHTTPRejectsDuplicateHost(t *testing.T) {
	h := &inventory.AdmissionHandler{
		Lister: &staticLister{items: []inventory.AdmissionResource{
			{
				Name: "rack1",
				Type: "bare-metal",
				Cfg: map[string]any{
					"inventory": []any{
						map[string]any{
							"address":         "10.0.4.11",
							"sshUser":         "forge",
							"sshKeySecretRef": map[string]any{"name": "k"},
						},
					},
				},
			},
		}},
	}
	mux := http.NewServeMux()
	h.Register(mux)

	body, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{"name": "rack2"},
		"spec": map[string]any{
			"type": "ssh",
			"config": map[string]any{
				"inventory": []any{
					map[string]any{
						"address":         "10.0.4.11",
						"sshUser":         "forge",
						"sshKeySecretRef": map[string]any{"name": "k2"},
					},
				},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/admission/infrastructureproviders", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}
