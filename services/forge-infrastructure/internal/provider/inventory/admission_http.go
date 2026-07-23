package inventory

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	awsprovider "forge.local/services/forge-infrastructure/internal/provider/aws"
	azureprovider "forge.local/services/forge-infrastructure/internal/provider/azure"
	"forge.local/services/forge-infrastructure/internal/provider/hetzner"
)

// ProviderLister lists existing InfrastructureProvider resources for admission.
type ProviderLister interface {
	List(ctx context.Context, plural, labelSelector string) ([]AdmissionResource, error)
}

// AdmissionResource is the minimal envelope needed for duplicate-host checks.
type AdmissionResource struct {
	Name string
	Type string
	Cfg  map[string]any
}

// AdmissionHandler rejects InfrastructureProvider creates with invalid inventory
// or host addresses already declared by another provider.
type AdmissionHandler struct {
	Lister ProviderLister
}

// Register mounts POST /v1/admission/infrastructureproviders.
func (h *AdmissionHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/admission/infrastructureproviders", h.admit)
}

func (h *AdmissionHandler) admit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec map[string]any `json:"spec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(body.Metadata.Name)
	if name == "" {
		http.Error(w, `{"error":"metadata.name is required"}`, http.StatusBadRequest)
		return
	}
	typeName, _ := body.Spec["type"].(string)
	cfg, _ := body.Spec["config"].(map[string]any)
	if cfg == nil {
		cfg = map[string]any{}
	}

	if strings.EqualFold(typeName, "hetzner") {
		if err := hetzner.ValidateConfig(cfg); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":   err.Error(),
				"code":    "invalid_hetzner_config",
				"message": err.Error(),
			})
			return
		}
	}
	if strings.EqualFold(typeName, "aws") {
		if err := awsprovider.ValidateConfig(cfg); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":   err.Error(),
				"code":    "invalid_aws_config",
				"message": err.Error(),
			})
			return
		}
	}
	if strings.EqualFold(typeName, "azure") {
		if err := azureprovider.ValidateConfig(cfg); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":   err.Error(),
				"code":    "invalid_azure_config",
				"message": err.Error(),
			})
			return
		}
	}

	var existing []ProviderInventory
	if h.Lister != nil {
		items, err := h.Lister.List(r.Context(), "infrastructureproviders", "")
		if err != nil {
			http.Error(w, `{"error":"list providers failed"}`, http.StatusBadGateway)
			return
		}
		for _, it := range items {
			hosts, err := ParseConfig(it.Cfg)
			if err != nil {
				continue
			}
			existing = append(existing, ProviderInventory{Name: it.Name, Type: it.Type, Hosts: hosts})
		}
	}

	if err := AdmitCreate(existing, typeName, name, cfg); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   err.Error(),
			"code":    "inventory_address_conflict",
			"message": err.Error(),
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"allowed":true}`))
}
